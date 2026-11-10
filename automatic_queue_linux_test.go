//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type automaticHTTPFixture struct {
	server  *httptest.Server
	payload []byte
	mu      sync.Mutex
	streams map[chan int]bool
	finish  chan struct{}
}

func newAutomaticHTTPFixture(t *testing.T) *automaticHTTPFixture {
	s := &automaticHTTPFixture{payload: bytes.Repeat([]byte("adaptive"), 4<<20), streams: map[chan int]bool{}, finish: make(chan struct{})}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"adaptive-v1"`)
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || start < 0 || end >= len(s.payload) || start > end {
			http.Error(w, "invalid range", 416)
			return
		}
		if r.Header.Get("Range") == "bytes=0-0" {
			http.ServeContent(w, r, "fixture", time.Time{}, bytes.NewReader(s.payload))
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(s.payload)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(206)
		w.(http.Flusher).Flush()
		stream := make(chan int, 1)
		s.mu.Lock()
		s.streams[stream] = true
		s.mu.Unlock()
		defer func() { s.mu.Lock(); delete(s.streams, stream); s.mu.Unlock() }()
		for start <= end {
			select {
			case n := <-stream:
				n = min(n, end-start+1)
				written, err := w.Write(s.payload[start : start+n])
				start += written
				w.(http.Flusher).Flush()
				if err != nil {
					return
				}
			case <-s.finish:
				_, _ = w.Write(s.payload[start : end+1])
				return
			case <-r.Context().Done():
				return
			}
		}
	}))
	t.Cleanup(s.server.Close)
	return s
}
func (s *automaticHTTPFixture) count() int { s.mu.Lock(); defer s.mu.Unlock(); return len(s.streams) }
func (s *automaticHTTPFixture) pulse(gain bool) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 64 << 10
	if !gain && len(s.streams) > 0 {
		n /= len(s.streams)
	}
	for stream := range s.streams {
		stream <- n
	}
	return int64(n * len(s.streams))
}

type automaticQueueHarness struct {
	commands  *io.PipeWriter
	output    *queueOutput
	ticks     chan time.Time
	at        atomic.Int64
	done      chan error
	requestID int
}

func startAutomaticQueueHarness(t *testing.T, source ...func(time.Time) hardwareSnapshot) *automaticQueueHarness {
	h := &automaticQueueHarness{output: &queueOutput{}, ticks: make(chan time.Time), done: make(chan error, 1)}
	h.at.Store(time.Unix(100, 0).UnixNano())
	input, commands := io.Pipe()
	h.commands = commands
	ctx, cancel := context.WithCancel(context.Background())
	if len(source) > 0 {
		ctx = context.WithValue(ctx, hardwareSourceKey{}, source[0])
	}
	go func() {
		h.done <- runQueueClock(ctx, input, h.output, 3, 4, func() time.Time { return time.Unix(0, h.at.Load()) }, h.ticks)
	}()
	t.Cleanup(func() {
		cancel()
		commands.Close()
		select {
		case err := <-h.done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("queue failed to stop")
		}
	})
	policy := defaultRequestPolicy()
	h.request(t, desktopRequest{Operation: "initialize", Management: &policy})
	return h
}
func (h *automaticQueueHarness) request(t *testing.T, req desktopRequest) desktopResponse {
	t.Helper()
	h.requestID++
	req.ID = h.requestID
	data, _ := json.Marshal(req)
	if _, err := fmt.Fprintf(h.commands, "desktop-json %s\n", data); err != nil {
		t.Fatal(err)
	}
	var response desktopResponse
	waitQueue(t, func() bool {
		h.output.mu.Lock()
		defer h.output.mu.Unlock()
		for _, line := range strings.Split(h.output.text, "\n") {
			if strings.HasPrefix(line, "DESKTOP_JSON ") && json.Unmarshal([]byte(strings.TrimPrefix(line, "DESKTOP_JSON ")), &response) == nil && response.ID == req.ID {
				return true
			}
		}
		return false
	})
	if response.Error != "" {
		t.Fatal(response.Error)
	}
	return response
}
func acceptedEntries(jobs []desktopEntry) int64 {
	var n int64
	for _, j := range jobs {
		if j.Measurements != nil {
			n += j.Measurements.Accepted
		}
	}
	return n
}
func (h *automaticQueueHarness) pulse(t *testing.T, s *automaticHTTPFixture, gain bool) desktopResponse {
	t.Helper()
	before := acceptedEntries(h.request(t, desktopRequest{Operation: "snapshot"}).Jobs)
	n := s.pulse(gain)
	waitQueue(t, func() bool {
		return acceptedEntries(h.request(t, desktopRequest{Operation: "snapshot"}).Jobs) >= before+n
	})
	at := time.Unix(0, h.at.Add(int64(time.Second)))
	select {
	case h.ticks <- at:
	case <-time.After(5 * time.Second):
		t.Fatal("scheduler tick blocked")
	}
	return h.request(t, desktopRequest{Operation: "snapshot"})
}
func TestAutomaticQueueMeasuredHTTPTrials(t *testing.T) {
	for _, files := range []bool{false, true} {
		for _, gain := range []bool{false, true} {
			name := fmt.Sprintf("files=%v/gain=%v", files, gain)
			t.Run(name, func(t *testing.T) {
				t.Setenv("XDG_CONFIG_HOME", t.TempDir())
				s := newAutomaticHTTPFixture(t)
				h := startAutomaticQueueHarness(t)
				dir := t.TempDir()
				h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: s.server.URL, Destination: filepath.Join(dir, "first")}})
				if files {
					h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: s.server.URL, Destination: filepath.Join(dir, "second")}})
				}
				h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: s.server.URL, Destination: filepath.Join(dir, "later"), State: "paused"}})
				waitQueue(t, func() bool { return s.count() == 1 })
				// Initialize membership before collecting the two baseline windows.
				h.ticks <- time.Unix(0, h.at.Load())
				for i := 0; i < 20; i++ {
					h.pulse(t, s, true)
				}
				waitQueue(t, func() bool { return s.count() == 2 })
				first := h.request(t, desktopRequest{Operation: "snapshot"})
				if files {
					if first.Management.FileTarget != 2 {
						t.Fatalf("missing file admission: %+v", first)
					}
				} else {
					waitQueue(t, func() bool { r := h.request(t, desktopRequest{Operation: "snapshot"}); return r.Jobs[0].Target == 2 })
				}
				// Segment publication and five seconds of stable membership precede trials.
				time.Sleep(550 * time.Millisecond)
				for i := 0; i < 26; i++ {
					h.pulse(t, s, gain)
				}
				result := h.request(t, desktopRequest{Operation: "snapshot"})
				if files {
					if gain && result.Management.FileTarget != 2 {
						t.Fatalf("successful admission discarded: %+v", result.Management)
					}
					if !gain && (result.Management.FileTarget != 1 || !result.Management.Draining || s.count() != 2) {
						t.Fatalf("failed admission did not drain: %+v active=%d", result.Management, s.count())
					}
				} else {
					want := 1
					if gain {
						want = 2
					}
					waitQueue(t, func() bool {
						r := h.request(t, desktopRequest{Operation: "snapshot"})
						return r.Jobs[0].Target == want && s.count() == want
					})
					// Manual restores the configured ceiling; switching back returns to one.
					manual := requestPolicy{Mode: "manual", Total: 8, PerOrigin: 4}
					h.request(t, desktopRequest{Operation: "configure", MaxActive: 3, Connections: 4, Management: &manual})
					waitQueue(t, func() bool { return s.count() == 4 })
					auto := defaultRequestPolicy()
					h.request(t, desktopRequest{Operation: "configure", MaxActive: 3, Connections: 4, Management: &auto})
					waitQueue(t, func() bool { return s.count() == 1 })
				}
				if result.Jobs[len(result.Jobs)-1].State != "paused" {
					t.Fatal("adaptation started paused job")
				}
				close(s.finish)
				waitQueue(t, func() bool {
					r := h.request(t, desktopRequest{Operation: "snapshot"})
					for _, j := range r.Jobs[:len(r.Jobs)-1] {
						if j.State != "completed" {
							return false
						}
					}
					return r.Management.Active == 0 && r.Management.Waiting == 0
				})
				requireContents(t, filepath.Join(dir, "first"), s.payload)
				if files {
					requireContents(t, filepath.Join(dir, "second"), s.payload)
				}
				path, _ := queuePath()
				metadata, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"file_target", "automatic_reason", "measurements", "worker_target", "cooldown"} {
					if bytes.Contains(metadata, []byte(key)) {
						t.Fatal("runtime adaptation persisted", key)
					}
				}
			})
		}
	}
}

func TestAutomaticQueueCooldownDoesNotBlockHealthyOrigin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	throttled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		http.Error(w, "busy", 503)
	}))
	defer throttled.Close()
	healthy := newAutomaticHTTPFixture(t)
	h := startAutomaticQueueHarness(t)
	dir := t.TempDir()
	h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: throttled.URL, Destination: filepath.Join(dir, "throttled")}})
	waitQueue(t, func() bool {
		r := h.request(t, desktopRequest{Operation: "snapshot"})
		return len(r.Jobs) > 0 && r.Jobs[0].Measurements != nil && r.Jobs[0].Measurements.Retrying > 0
	})
	h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: throttled.URL, Destination: filepath.Join(dir, "same-origin")}})
	h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: healthy.server.URL, Destination: filepath.Join(dir, "healthy")}})
	waitQueue(t, func() bool { return healthy.count() == 1 })
	r := h.request(t, desktopRequest{Operation: "snapshot"})
	if r.Jobs[1].State != "queued" || r.Jobs[2].State != "running" {
		t.Fatalf("cooldown did not isolate origins: %+v", r.Jobs)
	}
	if r.Jobs[0].AutomaticReason == "" || r.Jobs[0].Activity != "Waiting for retry" {
		t.Fatal("cooldown/retry reason missing", r.Jobs[0])
	}
	close(healthy.finish)
	waitQueue(t, func() bool {
		r := h.request(t, desktopRequest{Operation: "snapshot"})
		return r.Jobs[2].State == "completed"
	})
	requireContents(t, filepath.Join(dir, "healthy"), healthy.payload)
}
