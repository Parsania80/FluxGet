package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRetryPolicy(t *testing.T) {
	t.Run("bounded", func(t *testing.T) {
		calls := 0
		err := withRetries(context.Background(), time.Millisecond, func() error { calls++; return &httpStatusError{code: 503} })
		if err == nil || calls != 4 {
			t.Fatalf("calls %d error %v", calls, err)
		}
	})
	t.Run("permanent", func(t *testing.T) {
		calls := 0
		err := withRetries(context.Background(), time.Millisecond, func() error { calls++; return errors.New("permanent") })
		if err == nil || calls != 1 {
			t.Fatalf("calls %d error %v", calls, err)
		}
	})
	t.Run("checkpoint failure", func(t *testing.T) {
		calls := 0
		err := withRetries(context.Background(), time.Millisecond, func() error { calls++; return errors.Join(io.ErrUnexpectedEOF, errors.New("checkpoint write failed")) })
		if err == nil || calls != 1 {
			t.Fatalf("calls %d error %v", calls, err)
		}
	})
	t.Run("cancel backoff", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		calls := 0
		err := withRetries(ctx, time.Hour, func() error { calls++; cancel(); return &httpStatusError{code: 503} })
		if !errors.Is(err, context.Canceled) || calls != 1 {
			t.Fatalf("calls %d error %v", calls, err)
		}
	})
	t.Run("honor server delay", func(t *testing.T) {
		calls := 0
		start := time.Now()
		err := withRetries(context.Background(), time.Millisecond, func() error {
			calls++
			if calls == 1 {
				return &httpStatusError{code: 503, retryAfter: 30 * time.Millisecond}
			}
			return nil
		})
		if err != nil || calls != 2 || time.Since(start) < 30*time.Millisecond {
			t.Fatalf("delay not honored: calls %d, error %v", calls, err)
		}
	})
	t.Run("retry after", func(t *testing.T) {
		for _, v := range []string{"61", "999999999999999999999999"} {
			r := &http.Response{StatusCode: 429, Header: http.Header{"Retry-After": []string{v}}}
			if retryable(transientStatus(r)) {
				t.Fatalf("accepted %q", v)
			}
		}
		r := &http.Response{StatusCode: 503, Header: http.Header{"Retry-After": []string{"1"}}}
		var e *httpStatusError
		if !errors.As(transientStatus(r), &e) || e.retryAfter != time.Second {
			t.Fatal("Retry-After was not parsed")
		}
	})
}

func TestTimeouts(t *testing.T) {
	for _, mode := range []string{"headers", "body"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "body" {
					w.Header().Set("Content-Length", "10")
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
				}
				<-r.Context().Done()
			}))
			defer server.Close()
			client := newDownloadClient(time.Second, time.Second, 30*time.Millisecond, 30*time.Millisecond)
			defer client.CloseIdleConnections()
			resp, err := client.Get(server.URL)
			if err == nil {
				_, err = io.ReadAll(resp.Body)
				resp.Body.Close()
			}
			if !retryable(err) {
				t.Fatalf("expected retryable timeout: %v", err)
			}
		})
	}
}

func TestRetryFreshHTTPFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			w.WriteHeader(503)
			return
		}
		io.WriteString(w, "success")
	}))
	defer server.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dest := filepath.Join(t.TempDir(), "file")
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	err := withRetries(context.Background(), time.Millisecond, func() error {
		_, err := runDownloadAttempt(context.Background(), client, queueJob{URL: server.URL, Destination: dest}, "", io.Discard, 1)
		return err
	})
	if err != nil || calls.Load() != 3 {
		t.Fatalf("calls %d: %v", calls.Load(), err)
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != "success" {
		t.Fatalf("data %q: %v", b, err)
	}
}

func TestRetryResumesValidatedPartial(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := []byte("some complete data")
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("ETag", `"v1"`)
		switch r.Header.Get("Range") {
		case "":
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.Write(payload[:6])
		case "bytes=0-0":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.WriteHeader(206)
			w.Write(payload[:1])
		case "bytes=6-":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 6-%d/%d", len(payload)-1, len(payload)))
			w.WriteHeader(206)
			w.Write(payload[6:])
		default:
			t.Errorf("unexpected range %q", r.Header.Get("Range"))
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "file")
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	err := withRetries(context.Background(), time.Millisecond, func() error {
		_, err := runDownloadAttempt(context.Background(), client, queueJob{URL: server.URL, Destination: dest}, "", io.Discard, 1)
		return err
	})
	if err != nil || calls.Load() != 3 {
		t.Fatalf("calls %d: %v", calls.Load(), err)
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != string(payload) {
		t.Fatalf("data %q: %v", b, err)
	}
}

func TestRetryKeepsUnresumablePartial(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Length", "10")
		io.WriteString(w, "partial")
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "file")
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	err := withRetries(context.Background(), time.Millisecond, func() error {
		_, err := runDownloadAttempt(context.Background(), client, queueJob{URL: server.URL, Destination: dest}, "", io.Discard, 1)
		return err
	})
	if err == nil || calls.Load() != 1 {
		t.Fatalf("calls %d: %v", calls.Load(), err)
	}
	b, err := os.ReadFile(dest + ".part")
	if err != nil || string(b) != "partial" {
		t.Fatalf("partial %q: %v", b, err)
	}
}

func TestInactivityTimeoutAllowsSlowContinuousDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "5")
		for i := 0; i < 5; i++ {
			io.WriteString(w, "x")
			w.(http.Flusher).Flush()
			time.Sleep(15 * time.Millisecond)
		}
	}))
	defer server.Close()
	client := newDownloadClient(time.Second, time.Second, time.Second, 60*time.Millisecond)
	defer client.CloseIdleConnections()
	response, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || string(data) != "xxxxx" {
		t.Fatalf("data %q: %v", data, err)
	}
}
