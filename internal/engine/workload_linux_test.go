//go:build linux

package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const workloadBytes = 128 << 20

type workloadSamples struct {
	rss, heap  uint64
	goroutines int
}

func sampleWorkload() workloadSamples {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	s := workloadSamples{heap: m.HeapAlloc, goroutines: runtime.NumGoroutine()}
	data, err := os.ReadFile("/proc/self/status")
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "VmRSS:") {
				fields := strings.Fields(line)
				if len(fields) > 1 {
					value, _ := strconv.ParseUint(fields[1], 10, 64)
					s.rss = value * 1024
				}
				break
			}
		}
	}
	return s
}
func monitorWorkload(stop <-chan struct{}, done chan<- workloadSamples) {
	peak := sampleWorkload()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s := sampleWorkload()
			if s.heap > peak.heap {
				peak.heap = s.heap
			}
			if s.rss > peak.rss {
				peak.rss = s.rss
			}
			if s.goroutines > peak.goroutines {
				peak.goroutines = s.goroutines
			}
		case <-stop:
			done <- peak
			return
		}
	}
}

func BenchmarkRecordedWorkload(b *testing.B) {
	b.Setenv("XDG_CONFIG_HOME", b.TempDir())
	dir := b.TempDir()
	fixture := filepath.Join(dir, "fixture")
	f, err := os.Create(fixture)
	if err != nil {
		b.Fatal(err)
	}
	block := bytes.Repeat([]byte("fluxget-workload"), 2048)
	digest := sha256.New()
	for written := 0; written < workloadBytes; {
		n := len(block)
		if n > workloadBytes-written {
			n = workloadBytes - written
		}
		if _, err := f.Write(block[:n]); err != nil {
			b.Fatal(err)
		}
		digest.Write(block[:n])
		written += n
	}
	if err := f.Close(); err != nil {
		b.Fatal(err)
	}
	expected := digest.Sum(nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, err := os.Open(fixture)
		if err != nil {
			http.Error(w, "fixture unavailable", 500)
			return
		}
		defer file.Close()
		w.Header().Set("ETag", `"workload-v1"`)
		http.ServeContent(w, r, "fixture", time.Time{}, file)
	}))
	defer server.Close()
	for _, connections := range []int{1, 4, 8} {
		b.Run(fmt.Sprintf("connections_%d", connections), func(b *testing.B) {
			client := defaultDownloadClient()
			defer client.CloseIdleConnections()
			var elapsed time.Duration
			var max workloadSamples
			var base workloadSamples
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dest := filepath.Join(dir, fmt.Sprintf("download-%d-%d", connections, i))
				runtime.GC()
				base = sampleWorkload()
				stop := make(chan struct{})
				samples := make(chan workloadSamples, 1)
				go monitorWorkload(stop, samples)
				start := time.Now()
				n, err := downloadFresh(context.Background(), client, server.URL, dest, "", io.Discard, connections)
				elapsed += time.Since(start)
				close(stop)
				peak := <-samples
				if base.rss > peak.rss {
					peak.rss = base.rss
				}
				if base.heap > peak.heap {
					peak.heap = base.heap
				}
				if base.goroutines > peak.goroutines {
					peak.goroutines = base.goroutines
				}
				if err != nil || n != workloadBytes {
					b.Fatalf("download %d: %v", n, err)
				}
				if peak.rss > max.rss {
					max.rss = peak.rss
				}
				if peak.heap > max.heap {
					max.heap = peak.heap
				}
				if peak.goroutines > max.goroutines {
					max.goroutines = peak.goroutines
				}
				file, err := os.Open(dest)
				if err != nil {
					b.Fatal(err)
				}
				hash := sha256.New()
				_, err = io.Copy(hash, file)
				file.Close()
				if err != nil || !bytes.Equal(hash.Sum(nil), expected) {
					b.Fatal("output differs from fixture")
				}
				if err := os.Remove(dest); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(workloadBytes*b.N)/(1<<20)/elapsed.Seconds(), "MiB/s")
			b.ReportMetric(float64(elapsed.Microseconds())/float64(b.N)/1000, "transfer-ms")
			b.ReportMetric(float64(base.rss)/(1<<20), "base-RSS-MiB")
			b.ReportMetric(float64(base.heap)/(1<<20), "base-heap-MiB")
			b.ReportMetric(float64(base.goroutines), "base-goroutines")
			b.ReportMetric(float64(max.rss)/(1<<20), "peak-RSS-MiB")
			b.ReportMetric(float64(max.heap)/(1<<20), "peak-heap-MiB")
			b.ReportMetric(float64(max.goroutines), "peak-goroutines")
		})
	}
}

func BenchmarkRecordedCancellation(b *testing.B) {
	b.Setenv("XDG_CONFIG_HOME", b.TempDir())
	for _, connections := range []int{1, 4} {
		b.Run(fmt.Sprintf("connections_%d", connections), func(b *testing.B) {
			dir := b.TempDir()
			entered := make(chan struct{}, 16)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"workload-v1"`)
				rr := r.Header.Get("Range")
				if rr == "bytes=0-0" {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", workloadBytes))
					w.WriteHeader(206)
					w.Write([]byte("f"))
					return
				}
				if rr != "" {
					var start, end int
					if _, err := fmt.Sscanf(rr, "bytes=%d-%d", &start, &end); err != nil {
						b.Errorf("bad range %q", rr)
						return
					}
					w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, workloadBytes))
					w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
					w.WriteHeader(206)
				} else {
					w.Header().Set("Content-Length", fmt.Sprint(workloadBytes))
				}
				w.Write(make([]byte, 64<<10))
				w.(http.Flusher).Flush()
				entered <- struct{}{}
				<-r.Context().Done()
			}))
			defer server.Close()
			client := defaultDownloadClient()
			defer client.CloseIdleConnections()
			var total, max time.Duration
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				dest := filepath.Join(dir, fmt.Sprintf("cancel-%d-%d", connections, i))
				ctx, cancel := context.WithCancel(context.Background())
				done := make(chan error, 1)
				go func() {
					_, err := downloadFresh(ctx, client, server.URL, dest, "", io.Discard, connections)
					done <- err
				}()
				for j := 0; j < connections; j++ {
					select {
					case <-entered:
					case <-time.After(10 * time.Second):
						cancel()
						b.Fatal("workers did not start")
					}
				}
				deadline := time.Now().Add(10 * time.Second)
				for {
					info, err := os.Stat(dest + ".part")
					if err == nil && info.Size() > 0 {
						break
					}
					select {
					case err := <-done:
						cancel()
						b.Fatalf("transfer stopped before cancellation: %v", err)
					default:
					}
					if time.Now().After(deadline) {
						cancel()
						b.Fatal("partial data did not arrive")
					}
					time.Sleep(time.Millisecond)
				}
				start := time.Now()
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						b.Fatalf("cancellation: %v", err)
					}
				case <-time.After(10 * time.Second):
					b.Fatal("cancellation timed out")
				}
				duration := time.Since(start)
				total += duration
				if duration > max {
					max = duration
				}
				if _, err := os.Lstat(dest); !errors.Is(err, os.ErrNotExist) {
					b.Fatalf("destination published: %v", err)
				}
				if _, err := os.Stat(dest + ".part"); err != nil {
					b.Fatal(err)
				}
				checkpointPath, err := checkpointLocation(dest)
				if err != nil {
					b.Fatal(err)
				}
				checkpoint, err := loadCheckpoint(checkpointPath)
				if err != nil || checkpoint.State != "interrupted" {
					b.Fatalf("interrupted checkpoint: %+v %v", checkpoint, err)
				}
			}
			b.ReportMetric(float64(total.Microseconds())/float64(b.N)/1000, "cancel-ms")
			b.ReportMetric(float64(max.Microseconds())/1000, "max-cancel-ms")
		})
	}
}
