package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func takeLease(t *testing.T, b *requestBudget, job int, origin string) *requestLease {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := b.acquire(ctx, job, origin)
	if err != nil {
		t.Fatal(err)
	}
	return lease
}
func waitPending(t *testing.T, b *requestBudget, n int) {
	t.Helper()
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		s, _ := b.snapshot()
		if s.Waiting == n {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("waiting requests did not reach %d", n)
}
func TestRequestBudgetsAtomicAdmissionAndOriginIsolation(t *testing.T) {
	b := newRequestBudget(requestPolicy{Mode: "automatic", Total: 2, PerOrigin: 1})
	a := takeLease(t, b, 1, "a")
	defer a.release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	waiting := make(chan error, 1)
	go func() {
		l, e := b.acquire(ctx, 2, "a")
		if l != nil {
			l.release()
		}
		waiting <- e
	}()
	waitPending(t, b, 1)
	other := takeLease(t, b, 3, "b")
	s, _ := b.snapshot()
	if s.Active != 2 || s.Waiting != 1 {
		t.Fatalf("partial grant or blocked origin stalled another: %+v", s)
	}
	other.release()
	cancel()
	if err := <-waiting; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	a.release()
	a.release()
	s, _ = b.snapshot()
	if s.Active != 0 || s.Waiting != 0 {
		t.Fatalf("leaked/double lease: %+v", s)
	}
}
func TestRequestBudgetFairnessAndLiveModeChanges(t *testing.T) {
	b := newRequestBudget(requestPolicy{Mode: "automatic", Total: 1, PerOrigin: 1})
	first := takeLease(t, b, 1, "a")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	grants := make(chan int, 3)
	leases := make(chan *requestLease, 3)
	enqueue := func(job int) {
		go func() {
			l, e := b.acquire(ctx, job, "a")
			if e == nil {
				grants <- job
				leases <- l
			}
		}()
	}
	enqueue(1)
	waitPending(t, b, 1)
	enqueue(2)
	waitPending(t, b, 2)
	first.release()
	if job := <-grants; job != 1 {
		t.Fatalf("FIFO tie: %d", job)
	}
	// Job 1's new grant cannot prevent job 2 from receiving the next slot.
	enqueue(1)
	waitPending(t, b, 2)
	(<-leases).release()
	if job := <-grants; job != 2 {
		t.Fatalf("waiting job starved: %d", job)
	}
	second := <-leases
	if err := b.configure(requestPolicy{Mode: "manual", Total: 1, PerOrigin: 1}); err != nil {
		t.Fatal(err)
	}
	if job := <-grants; job != 1 {
		t.Fatal("manual did not unblock queued work")
	}
	third := <-leases
	if err := b.configure(requestPolicy{Mode: "automatic", Total: 1, PerOrigin: 1}); err != nil {
		t.Fatal(err)
	}
	enqueue(3)
	waitPending(t, b, 1)
	second.release()
	select {
	case <-grants:
		t.Fatal("new grant exceeded a reduced maximum")
	default:
	}
	third.release()
	if job := <-grants; job != 3 {
		t.Fatal("drained budget failed to admit waiting work")
	}
	(<-leases).release()
	before, _ := b.snapshot()
	if err := b.configure(requestPolicy{Mode: "broken", Total: 0}); err == nil {
		t.Fatal("invalid policy accepted")
	}
	after, _ := b.snapshot()
	if before != after {
		t.Fatal("rejected configuration changed state")
	}
}
func TestRequestBudgetCancellationRacingRelease(t *testing.T) {
	b := newRequestBudget(requestPolicy{Mode: "automatic", Total: 1, PerOrigin: 1})
	for range 100 {
		first := takeLease(t, b, 1, "a")
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			l, _ := b.acquire(ctx, 2, "a")
			if l != nil {
				l.release()
			}
		}()
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); first.release() }()
		go func() { defer wg.Done(); cancel() }()
		wg.Wait()
		<-done
		s, _ := b.snapshot()
		if s.Active != 0 || s.Waiting != 0 {
			t.Fatalf("cancel/grant race leaked: %+v", s)
		}
	}
}
func TestRequestOriginNormalization(t *testing.T) {
	for raw, want := range map[string]string{"https://EXAMPLE.com/a?x=1": "https://example.com:443", "https://example.com:443/b": "https://example.com:443", "http://[::1]/a": "http://[::1]:80"} {
		u, _ := url.Parse(raw)
		got, e := requestOrigin(u)
		if e != nil || got != want {
			t.Fatalf("origin %s: %s %v", raw, got, e)
		}
	}
}
func TestBudgetTransportRedirectsEOFAndCancellation(t *testing.T) {
	b := newRequestBudget(requestPolicy{Mode: "automatic", Total: 1, PerOrigin: 1})
	m := &transferMeasurements{}
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "payload") }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
			return
		}
		http.Redirect(w, r, target.URL, http.StatusFound)
	}))
	defer origin.Close()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: &requestTransport{base: transport, budget: b, job: 1, metrics: m}, Timeout: 2 * time.Second}
	response, err := client.Get(origin.URL)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != "payload" {
		t.Fatalf("redirect body: %q %v", data, err)
	}
	response.Body.Close()
	s, _ := b.snapshot()
	if s.Active != 0 || m.snapshot().Requests != 2 {
		t.Fatalf("redirect or EOF leaked: %+v metrics=%+v", s, m.snapshot())
	}
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, "GET", origin.URL+"/hold", nil)
	response, err = client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	cancel() // Even an abandoned response returns its slot after cancellation.
	until := time.Now().Add(time.Second)
	for time.Now().Before(until) {
		s, _ = b.snapshot()
		if s.Active == 0 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if s.Active != 0 {
		t.Fatal("canceled unclosed body held its lease")
	}
	response.Body.Close()
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type failingReadCloser struct{}

func (failingReadCloser) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
func (failingReadCloser) Close() error             { return nil }
func TestBudgetTransportFailureAndQueueBound(t *testing.T) {
	b := newRequestBudget(defaultRequestPolicy())
	m := &transferMeasurements{}
	req, _ := http.NewRequest("GET", "http://example.com/file", nil)
	for _, rt := range []http.RoundTripper{
		roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("header failure") }),
		roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Body: failingReadCloser{}, Header: http.Header{}}, nil
		}),
	} {
		tpt := &requestTransport{base: rt, budget: b, job: 1, metrics: m}
		resp, err := tpt.RoundTrip(req)
		if err == nil {
			_, _ = io.ReadAll(resp.Body)
			resp.Body.Close()
		}
		snapshot, _ := b.snapshot()
		if snapshot.Active != 0 {
			t.Fatal("failed request leaked a slot")
		}
	}
	for range 2048 {
		b.pending = append(b.pending, &budgetWaiter{ctx: context.Background(), ready: make(chan *requestLease, 1)})
	}
	if _, err := b.acquire(context.Background(), 1, "a"); err == nil || !strings.Contains(err.Error(), "full") {
		t.Fatal("admission queue is unbounded")
	}
}

func TestRequestBudgetCountsHTTP2Streams(t *testing.T) {
	b := newRequestBudget(requestPolicy{Mode: "automatic", Total: 2, PerOrigin: 2})
	m := &transferMeasurements{}
	gate := make(chan struct{})
	started := make(chan int, 3)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started <- r.ProtoMajor
		select {
		case <-gate:
			_, _ = io.WriteString(w, "ok")
		case <-r.Context().Done():
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	client := server.Client()
	client.Timeout = 3 * time.Second
	client.Transport = &requestTransport{base: client.Transport, budget: b, job: 1, metrics: m}
	done := make(chan error, 3)
	for range 3 {
		go func() {
			r, e := client.Get(server.URL)
			if e == nil {
				_, e = io.Copy(io.Discard, r.Body)
				r.Body.Close()
			}
			done <- e
		}()
	}
	for range 2 {
		select {
		case protocol := <-started:
			if protocol != 2 {
				t.Fatal("test did not negotiate HTTP/2")
			}
		case <-time.After(time.Second):
			t.Fatal("streams did not start")
		}
	}
	waitPending(t, b, 1)
	s, _ := b.snapshot()
	if s.Active != 2 {
		t.Fatalf("streams not counted independently: %+v", s)
	}
	close(gate)
	for range 3 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	s, _ = b.snapshot()
	if s.Active != 0 || s.Waiting != 0 {
		t.Fatal("HTTP/2 stream leases leaked")
	}
}
