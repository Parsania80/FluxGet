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
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelDownloadAndBoundedWorkers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("abcdefghijk"), 200000)
	var active, maxActive, ranges atomic.Int32
	ready := make(chan struct{}, 4)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") != "bytes=0-0" {
			n := active.Add(1)
			for {
				old := maxActive.Load()
				if n <= old || maxActive.CompareAndSwap(old, n) {
					break
				}
			}
			ranges.Add(1)
			ready <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
			}
			defer active.Add(-1)
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "file")
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	done := make(chan error, 1)
	expected := fmt.Sprintf("%x", sha256.Sum256(payload))
	go func() {
		_, err := downloadFresh(context.Background(), client, server.URL, dest, expected, io.Discard, 4)
		done <- err
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-ready:
		case <-time.After(5 * time.Second):
			t.Fatal("workers did not run concurrently")
		}
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if maxActive.Load() != 4 || ranges.Load() != 4 {
		t.Fatalf("active %d, ranges %d", maxActive.Load(), ranges.Load())
	}
	requireContents(t, dest, payload)
	requireMissing(t, dest+".part")
}

func TestParallelServerFilename(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("a"), minParallelSize+1)
	var ranges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			ranges.Add(1)
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Disposition", `attachment; filename="parallel.bin"`)
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	dir := t.TempDir()
	var out, stderr bytes.Buffer
	if code := run([]string{"-url", server.URL + "/endpoint", "-dir", dir, "-connections", "4"}, &out, &stderr); code != 0 {
		t.Fatalf("download failed: %s", stderr.String())
	}
	requireContents(t, filepath.Join(dir, "parallel.bin"), payload)
	if ranges.Load() != 5 {
		t.Fatalf("got %d range requests, want probe plus four workers", ranges.Load())
	}
}

func TestParallelFallback(t *testing.T) {
	for _, mode := range []string{"ignored", "weak", "small"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			payload := bytes.Repeat([]byte("z"), minParallelSize+3)
			if mode == "small" {
				payload = []byte("small")
			}
			var requests, rangeRequests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Header.Get("Range") != "" {
					rangeRequests.Add(1)
				}
				if mode == "ignored" {
					w.Write(payload)
					return
				}
				if mode == "weak" {
					w.Header().Set("ETag", `W/"v1"`)
				} else {
					w.Header().Set("ETag", `"v1"`)
				}
				http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			}))
			defer server.Close()
			client := defaultDownloadClient()
			defer client.CloseIdleConnections()
			dest := filepath.Join(t.TempDir(), "file")
			if _, err := downloadFresh(context.Background(), client, server.URL, dest, "", io.Discard, 4); err != nil {
				t.Fatal(err)
			}
			requireContents(t, dest, payload)
			if requests.Load() != 2 || rangeRequests.Load() != 1 {
				t.Fatalf("requests %d range %d", requests.Load(), rangeRequests.Load())
			}
		})
	}
}

func TestParallelRejectsInvalidWorkerResponses(t *testing.T) {
	for _, mode := range []string{"etag", "range", "ignored", "truncated"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			payload := bytes.Repeat([]byte("abcdefgh"), 150000)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"v1"`)
				rr := r.Header.Get("Range")
				if rr == "bytes=0-0" {
					http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
					return
				}
				if strings.HasPrefix(rr, "bytes=0-") {
					switch mode {
					case "etag":
						w.Header().Set("ETag", `"changed"`)
					case "range":
						w.Header().Set("Content-Range", fmt.Sprintf("bytes 1-2/%d", len(payload)))
						w.WriteHeader(206)
						io.WriteString(w, "xx")
						return
					case "ignored":
						w.Write(payload)
						return
					case "truncated":
						var start, end int
						fmt.Sscanf(rr, "bytes=%d-%d", &start, &end)
						w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
						w.Header().Set("Content-Length", strconv.Itoa(end-start+1))
						w.WriteHeader(206)
						w.Write(payload[:12])
						return
					}
				}
				http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			}))
			defer server.Close()
			client := defaultDownloadClient()
			defer client.CloseIdleConnections()
			dest := filepath.Join(t.TempDir(), "file")
			_, err := downloadFresh(context.Background(), client, server.URL, dest, "", io.Discard, 4)
			if err == nil {
				t.Fatal("accepted invalid worker response")
			}
			requireMissing(t, dest)
			cp, e := checkpointLocation(dest)
			if e != nil {
				t.Fatal(e)
			}
			c, e := loadCheckpoint(cp)
			if e != nil {
				t.Fatal(e)
			}
			if c.State != "interrupted" {
				t.Fatalf("state %s", c.State)
			}
			partial, e := os.ReadFile(dest + ".part")
			if e != nil {
				t.Fatal(e)
			}
			for _, chunk := range c.Chunks {
				if chunk.SHA256 == "" {
					continue
				}
				if chunk.End > int64(len(partial)) || !bytes.Equal(partial[chunk.Start:chunk.End], payload[chunk.Start:chunk.End]) {
					t.Fatal("saved chunk includes wrong bytes")
				}
				sum := sha256.Sum256(partial[chunk.Start:chunk.End])
				if fmt.Sprintf("%x", sum) != chunk.SHA256 {
					t.Fatal("chunk hash differs")
				}
			}
		})
	}
}

func TestParallelChecksumMismatchDoesNotPublish(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("x"), minParallelSize)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	dest := filepath.Join(t.TempDir(), "file")
	if _, err := downloadFresh(context.Background(), client, server.URL, dest, strings.Repeat("0", 64), io.Discard, 4); err == nil {
		t.Fatal("wrong checksum accepted")
	}
	requireMissing(t, dest)
}

func TestParallelCancellationResumesContiguousPrefix(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("0123456789"), 150000)
	firstDone := make(chan struct{}, 1)
	hold := make(chan struct{})
	var recovery atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		rr := r.Header.Get("Range")
		if !recovery.Load() && rr != "bytes=0-0" {
			if strings.HasPrefix(rr, "bytes=0-") {
				http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
				firstDone <- struct{}{}
				return
			}
			select {
			case <-hold:
			case <-r.Context().Done():
			}
			return
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	defer close(hold)
	ctx, cancel := context.WithCancel(context.Background())
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	dest := filepath.Join(t.TempDir(), "file")
	done := make(chan error, 1)
	go func() { _, err := downloadFresh(ctx, client, server.URL, dest, "", io.Discard, 4); done <- err }()
	select {
	case <-firstDone:
	case <-time.After(5 * time.Second):
		t.Fatal("first range did not complete")
	}
	// Allow the client to finish consuming the first response before cancellation.
	time.Sleep(30 * time.Millisecond)
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected cancellation")
	}
	requireMissing(t, dest)
	cp, err := checkpointLocation(dest)
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadCheckpoint(cp)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != "interrupted" || c.Completed <= 0 || c.Completed >= int64(len(payload)) {
		t.Fatalf("invalid checkpoint %+v", c)
	}
	recovery.Store(true)
	if _, err := resumeDownload(context.Background(), client, dest, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
}

func TestQueueUsesParallelConnections(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("x"), minParallelSize+12)
	var ranges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") != "bytes=0-0" && r.Header.Get("Range") != "" {
			ranges.Add(1)
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	input, commands := io.Pipe()
	out := &queueOutput{}
	done := make(chan error, 1)
	go func() { done <- runQueue(context.Background(), input, out, 1, 4) }()
	dest := filepath.Join(t.TempDir(), "file")
	fmt.Fprintf(commands, "add %s %s\nstart\n", server.URL, dest)
	waitQueue(t, func() bool { return out.contains("Job 1: completed") })
	fmt.Fprintln(commands, "quit")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	commands.Close()
	if ranges.Load() != 4 {
		t.Fatalf("ranges %d", ranges.Load())
	}
	requireContents(t, dest, payload)
}

func TestParallelRangeWorkerCountsAndRemainders(t *testing.T) {
	for _, tc := range []struct{ workers, size int }{{2, minParallelSize}, {3, minParallelSize + 1}, {4, minParallelSize + 17}, {16, minParallelSize + 31}} {
		t.Run(fmt.Sprintf("workers_%d", tc.workers), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			payload := bytes.Repeat([]byte("fluxget"), tc.size/7+1)[:tc.size]
			var ranges atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"v1"`)
				if r.Header.Get("Range") != "bytes=0-0" {
					ranges.Add(1)
				}
				http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			}))
			defer server.Close()
			client := defaultDownloadClient()
			defer client.CloseIdleConnections()
			dest := filepath.Join(t.TempDir(), "file")
			if _, err := downloadFresh(context.Background(), client, server.URL, dest, "", io.Discard, tc.workers); err != nil {
				t.Fatal(err)
			}
			if ranges.Load() != int32(tc.workers) {
				t.Fatalf("got %d ranges, want %d", ranges.Load(), tc.workers)
			}
			requireContents(t, dest, payload)
		})
	}
}
