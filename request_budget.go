package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The queue owner configures this broker. Workers only acquire/release leases.
// A single lock grants global and origin capacity together; no partial grants.
type requestPolicy struct {
	Mode      string `json:"mode"`
	Total     int    `json:"total"`
	PerOrigin int    `json:"per_origin"`
}

func (p requestPolicy) validate() error {
	if (p.Mode != "automatic" && p.Mode != "manual") || p.Total < 1 || p.Total > 64 || p.PerOrigin < 1 || p.PerOrigin > 16 {
		return errors.New("choose Automatic or Manual, 1–64 total requests and 1–16 per server")
	}
	return nil
}
func defaultRequestPolicy() requestPolicy {
	return requestPolicy{Mode: "automatic", Total: 8, PerOrigin: 4}
}

type budgetWaiter struct {
	ctx    context.Context
	job    int
	origin string
	ready  chan *requestLease
}
type budgetSnapshot struct {
	Policy  requestPolicy `json:"policy"`
	Active  int           `json:"active"`
	Waiting int           `json:"waiting"`
}
type jobBudgetSnapshot struct {
	Active  int `json:"active"`
	Waiting int `json:"waiting"`
}
type requestBudget struct {
	mu      sync.Mutex
	policy  requestPolicy
	active  int
	origins map[string]int
	jobs    map[int]int
	pending []*budgetWaiter
}

func newRequestBudget(p requestPolicy) *requestBudget {
	return &requestBudget{policy: p, origins: map[string]int{}, jobs: map[int]int{}}
}
func (b *requestBudget) configure(p requestPolicy) error {
	if err := p.validate(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.policy = p
	b.grantLocked()
	return nil
}
func (b *requestBudget) snapshot() (budgetSnapshot, map[int]jobBudgetSnapshot) {
	b.mu.Lock()
	defer b.mu.Unlock()
	snapshot := budgetSnapshot{Policy: b.policy, Active: b.active}
	jobs := map[int]jobBudgetSnapshot{}
	for id, n := range b.jobs {
		jobs[id] = jobBudgetSnapshot{Active: n}
	}
	for _, p := range b.pending {
		if p.ctx.Err() == nil {
			j := jobs[p.job]
			j.Waiting++
			jobs[p.job] = j
			snapshot.Waiting++
		}
	}
	return snapshot, jobs
}
func (b *requestBudget) acquire(ctx context.Context, job int, origin string) (*requestLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w := &budgetWaiter{ctx: ctx, job: job, origin: origin, ready: make(chan *requestLease, 1)}
	b.mu.Lock()
	// At most 64 jobs x 16 parts, plus metadata; never allow an unbounded queue.
	if len(b.pending) >= 2048 {
		b.mu.Unlock()
		return nil, errors.New("request admission queue is full")
	}
	b.pending = append(b.pending, w)
	b.grantLocked()
	b.mu.Unlock()
	select {
	case lease := <-w.ready:
		if err := ctx.Err(); err != nil {
			lease.release()
			return nil, err
		}
		return lease, nil
	case <-ctx.Done():
		b.mu.Lock()
		for i, p := range b.pending {
			if p == w {
				b.pending = append(b.pending[:i], b.pending[i+1:]...)
				break
			}
		}
		b.grantLocked()
		b.mu.Unlock()
		select {
		case lease := <-w.ready:
			lease.release()
		default:
		}
		return nil, ctx.Err()
	}
}
func (b *requestBudget) grantLocked() {
	for {
		// Prefer a waiting job with fewer current grants, FIFO for ties. Blocked
		// origins do not hold global slots or prevent unrelated work from starting.
		best := -1
		for i, w := range b.pending {
			if w.ctx.Err() != nil {
				continue
			}
			if b.policy.Mode == "automatic" && (b.active >= b.policy.Total || b.origins[w.origin] >= b.policy.PerOrigin) {
				continue
			}
			if best < 0 || b.jobs[w.job] < b.jobs[b.pending[best].job] {
				best = i
			}
		}
		if best < 0 {
			return
		}
		w := b.pending[best]
		b.pending = append(b.pending[:best], b.pending[best+1:]...)
		b.active++
		b.origins[w.origin]++
		b.jobs[w.job]++
		w.ready <- &requestLease{budget: b, job: w.job, origin: w.origin}
	}
}

type requestLease struct {
	budget *requestBudget
	job    int
	origin string
	once   sync.Once
}

func (l *requestLease) release() {
	l.once.Do(func() {
		b := l.budget
		b.mu.Lock()
		defer b.mu.Unlock()
		b.active--
		b.origins[l.origin]--
		b.jobs[l.job]--
		if b.origins[l.origin] == 0 {
			delete(b.origins, l.origin)
		}
		if b.jobs[l.job] == 0 {
			delete(b.jobs, l.job)
		}
		b.grantLocked()
	})
}
func requestOrigin(u *url.URL) (string, error) {
	if u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", errors.New("request requires an HTTP or HTTPS origin")
	}
	port := u.Port()
	if port == "" {
		port = "80"
		if u.Scheme == "https" {
			port = "443"
		}
	}
	number, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return "", errors.New("invalid HTTP origin port")
	}
	return u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.FormatUint(number, 10)), nil
}

type budgetContextKey struct{}
type requestSegmentKey struct{}
type requestTransport struct {
	base    http.RoundTripper
	budget  *requestBudget
	job     int
	metrics *transferMeasurements
}

func (t *requestTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	origin, err := requestOrigin(req.URL)
	if err != nil {
		return nil, err
	}
	part, _ := req.Context().Value(requestSegmentKey{}).(*segmentPart)
	part.setState(segmentWaiting)
	started := time.Now()
	lease, err := t.budget.acquire(req.Context(), t.job, origin)
	t.metrics.addDuration(metricBudgetWait, time.Since(started))
	if err != nil {
		return nil, err
	}
	part.setState(segmentConnecting)
	t.metrics.requests.Add(1)
	started = time.Now()
	response, err := t.base.RoundTrip(req)
	t.metrics.addDuration(metricHeaderWait, time.Since(started))
	if err != nil {
		lease.release()
		t.metrics.failures.Add(1)
		return nil, err
	}
	if response.StatusCode >= 400 {
		t.metrics.httpErrors.Add(1)
	}
	body := &measuredRequestBody{ReadCloser: response.Body, lease: lease, metrics: t.metrics}
	body.stop = context.AfterFunc(req.Context(), func() { body.close() })
	response.Body = body
	return response, nil
}
func (t *requestTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

type measuredRequestBody struct {
	io.ReadCloser
	lease   *requestLease
	metrics *transferMeasurements
	stop    func() bool
	once    sync.Once
	err     error
}

func (b *measuredRequestBody) Read(p []byte) (int, error) {
	start := time.Now()
	n, err := b.ReadCloser.Read(p)
	b.metrics.addDuration(metricNetworkRead, time.Since(start))
	b.metrics.networkBytes.Add(int64(n))
	if err != nil {
		if !errors.Is(err, io.EOF) {
			b.metrics.failures.Add(1)
		}
		b.lease.release()
	}
	return n, err
}
func (b *measuredRequestBody) close() {
	b.once.Do(func() { b.err = b.ReadCloser.Close(); b.lease.release() })
}
func (b *measuredRequestBody) Close() error {
	b.close()
	// The callback may itself be calling Close. Stopping it never waits.
	if b.stop != nil {
		b.stop()
	}
	return b.err
}
