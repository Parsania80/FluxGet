//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

const queueWorkloadJobs = 6
const queueWorkloadFileBytes = 4 << 20

type queueWorkloadServer struct {
	payload            []byte
	slow               bool
	gate               <-chan time.Time
	mu                 sync.Mutex
	active             map[string]int
	maxJobs, maxRanges int
}

func (s *queueWorkloadServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rr := r.Header.Get("Range")
	data := rr != "bytes=0-0"
	s.mu.Lock()
	if data {
		s.active[r.URL.Path]++
		if len(s.active) > s.maxJobs {
			s.maxJobs = len(s.active)
		}
		if s.active[r.URL.Path] > s.maxRanges {
			s.maxRanges = s.active[r.URL.Path]
		}
	}
	s.mu.Unlock()
	if data {
		defer func() {
			s.mu.Lock()
			s.active[r.URL.Path]--
			if s.active[r.URL.Path] == 0 {
				delete(s.active, r.URL.Path)
			}
			s.mu.Unlock()
		}()
	}
	w.Header().Set("ETag", `"queue-workload-v1"`)
	if !s.slow {
		http.ServeContent(w, r, "fixture", time.Time{}, bytes.NewReader(s.payload))
		return
	}
	timer := time.NewTimer(40 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-r.Context().Done():
		return
	}
	start, end := 0, len(s.payload)-1
	if rr != "" {
		if _, err := fmt.Sscanf(rr, "bytes=%d-%d", &start, &end); err != nil || start < 0 || end >= len(s.payload) || end < start {
			http.Error(w, "invalid range", 416)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(s.payload)))
	}
	w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
	if rr != "" {
		w.WriteHeader(http.StatusPartialContent)
	}
	if rr == "bytes=0-0" {
		_, _ = w.Write(s.payload[:1])
		return
	}
	for start <= end {
		select {
		case <-s.gate:
		case <-r.Context().Done():
			return
		}
		n := 64 << 10
		if n > end-start+1 {
			n = end - start + 1
		}
		written, err := w.Write(s.payload[start : start+n])
		start += written
		if err != nil || written != n {
			return
		}
	}
}

func queueWorkloadDirectory(b *testing.B, storage string) string {
	b.Helper()
	if storage == "tmpfs" {
		return b.TempDir()
	}
	parent := os.Getenv("FLUXGET_WORKLOAD_STORAGE_DIR")
	if parent == "" {
		var err error
		parent, err = os.Getwd()
		if err != nil {
			b.Fatal(err)
		}
	}
	dir, err := os.MkdirTemp(parent, ".fluxget-workload-*")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if err := os.RemoveAll(dir); err != nil {
			b.Errorf("remove workload directory: %v", err)
		}
	})
	return dir
}

func BenchmarkRecordedQueueWorkload(b *testing.B) {
	cases := []struct {
		name, storage string
		slow          bool
		active        int
		pause         bool
	}{
		{"tmpfs_fast_active3", "tmpfs", false, 3, false},
		{"disk_fast_active3", "disk", false, 3, false},
		{"disk_slow_active1", "disk", true, 1, false},
		{"disk_slow_active3", "disk", true, 3, false},
		{"disk_slow_pause_active3", "disk", true, 3, true},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			root := queueWorkloadDirectory(b, tc.storage)
			b.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
			payload := bytes.Repeat([]byte("queue-fixture"), queueWorkloadFileBytes/13+1)[:queueWorkloadFileBytes]
			sum := sha256.Sum256(payload)
			ticker := time.NewTicker(time.Second / 64)
			defer ticker.Stop()
			handler := &queueWorkloadServer{payload: payload, slow: tc.slow, gate: ticker.C, active: make(map[string]int)}
			server := httptest.NewServer(handler)
			defer server.Close()
			var total time.Duration
			var peak, base workloadSamples
			var maxJobs, maxRanges, maxQueueRunning int
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				runDir, err := os.MkdirTemp(root, "run-*")
				if err != nil {
					b.Fatal(err)
				}
				// Each iteration gets a fresh queue and checkpoint directory.
				if err := os.Setenv("XDG_CONFIG_HOME", filepath.Join(runDir, "config")); err != nil {
					b.Fatal(err)
				}
				qp, err := queuePath()
				if err != nil {
					b.Fatal(err)
				}
				runtime.GC()
				base = sampleWorkload()
				stop := make(chan struct{})
				samples := make(chan workloadSamples, 1)
				go monitorWorkload(stop, samples)
				input, commands := io.Pipe()
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() { done <- runQueue(ctx, input, io.Discard, tc.active, 4) }()
				start := time.Now()
				for n := 1; n <= queueWorkloadJobs; n++ {
					dest := filepath.Join(runDir, fmt.Sprintf("file-%d", n))
					if _, err := fmt.Fprintf(commands, "add %s/%d %s\n", server.URL, n, dest); err != nil {
						cancel()
						b.Fatal(err)
					}
				}
				if _, err := fmt.Fprintln(commands, "start"); err != nil {
					cancel()
					b.Fatal(err)
				}
				deadline := time.Now().Add(30 * time.Second)
				if tc.pause {
					dest := filepath.Join(runDir, "file-1")
					for {
						info, err := os.Stat(dest + ".part")
						if err == nil && info.Size() > 0 {
							break
						}
						if time.Now().After(deadline) {
							cancel()
							b.Fatal("first queued job did not start")
						}
						time.Sleep(5 * time.Millisecond)
					}
					start = time.Now()
					if _, err := fmt.Fprintln(commands, "pause 1"); err != nil {
						cancel()
						b.Fatal(err)
					}
				}
				for {
					q, err := loadQueue(qp)
					if err == nil && len(q.Jobs) == queueWorkloadJobs {
						complete := 0
						running := 0
						paused := false
						for _, j := range q.Jobs {
							if j.State == "failed" {
								cancel()
								b.Fatalf("job %d failed", j.ID)
							}
							if j.State == "completed" {
								complete++
							}
							if j.State == "running" {
								running++
							}
							if j.ID == 1 && j.State == "paused" {
								paused = true
							}
						}
						if running > maxQueueRunning {
							maxQueueRunning = running
						}
						if running > tc.active {
							cancel()
							b.Fatal("queue active limit exceeded")
						}
						if (!tc.pause && complete == queueWorkloadJobs) || (tc.pause && paused) {
							break
						}
					}
					if time.Now().After(deadline) {
						cancel()
						b.Fatal("queue workload timed out")
					}
					time.Sleep(5 * time.Millisecond)
				}
				elapsed := time.Since(start)
				total += elapsed
				if _, err := fmt.Fprintln(commands, "quit"); err != nil {
					cancel()
					b.Fatal(err)
				}
				commands.Close()
				if err := <-done; err != nil {
					cancel()
					b.Fatal(err)
				}
				cancel()
				close(stop)
				p := <-samples
				if base.rss > p.rss {
					p.rss = base.rss
				}
				if base.heap > p.heap {
					p.heap = base.heap
				}
				if base.goroutines > p.goroutines {
					p.goroutines = base.goroutines
				}
				if p.rss > peak.rss {
					peak.rss = p.rss
				}
				if p.heap > peak.heap {
					peak.heap = p.heap
				}
				if p.goroutines > peak.goroutines {
					peak.goroutines = p.goroutines
				}
				handler.mu.Lock()
				if handler.maxJobs > maxJobs {
					maxJobs = handler.maxJobs
				}
				if handler.maxRanges > maxRanges {
					maxRanges = handler.maxRanges
				}
				handler.mu.Unlock()
				if maxJobs > tc.active || maxRanges > 4 {
					b.Fatalf("observed server concurrency exceeds limits: jobs=%d ranges=%d", maxJobs, maxRanges)
				}
				if !tc.pause {
					for n := 1; n <= queueWorkloadJobs; n++ {
						f, err := os.Open(filepath.Join(runDir, fmt.Sprintf("file-%d", n)))
						if err != nil {
							b.Fatal(err)
						}
						hash := sha256.New()
						_, err = io.Copy(hash, f)
						f.Close()
						if err != nil || !bytes.Equal(hash.Sum(nil), sum[:]) {
							b.Fatalf("file %d differs from fixture", n)
						}
					}
				} else {
					p, err := checkpointLocation(filepath.Join(runDir, "file-1"))
					if err != nil {
						b.Fatal(err)
					}
					c, err := loadCheckpoint(p)
					if err != nil || c.State != "interrupted" {
						b.Fatalf("paused checkpoint: %+v %v", c, err)
					}
				}
			}
			b.ReportMetric(float64(total.Microseconds())/float64(b.N)/1000, "queue-ms")
			if !tc.pause {
				b.ReportMetric(float64(queueWorkloadJobs*queueWorkloadFileBytes*b.N)/(1<<20)/total.Seconds(), "aggregate-MiB/s")
			}
			b.ReportMetric(float64(base.rss)/(1<<20), "base-RSS-MiB")
			b.ReportMetric(float64(peak.rss)/(1<<20), "peak-RSS-MiB")
			b.ReportMetric(float64(peak.heap)/(1<<20), "peak-heap-MiB")
			b.ReportMetric(float64(peak.goroutines), "peak-goroutines")
			b.ReportMetric(float64(maxQueueRunning), "sampled-running-jobs")
			b.ReportMetric(float64(maxJobs), "max-server-jobs")
			b.ReportMetric(float64(maxRanges), "max-ranges-per-job")
		})
	}
}
