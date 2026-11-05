package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSpeedLimitSharedAcrossConnections(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, 8192)) }))
	defer server.Close()
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	limitClient(client, &speedLimiter{gate: make(chan struct{}, 1), bytesPerSecond: 32768})
	start := time.Now()
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, e := client.Get(server.URL)
			if e == nil {
				_, e = io.Copy(io.Discard, r.Body)
				r.Body.Close()
			}
			errors <- e
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 450*time.Millisecond {
		t.Fatalf("combined limit was exceeded: %v", elapsed)
	}
}

func TestSpeedLimitWaitCanBeCanceled(t *testing.T) {
	limit := &speedLimiter{gate: make(chan struct{}, 1), bytesPerSecond: 1024}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := limit.wait(ctx, 1024); err == nil || time.Since(start) > time.Second {
		t.Fatalf("cancel did not stop limiter: %v", err)
	}
}

func TestSpeedLimitCreditsTimeSpentWaitingForNetwork(t *testing.T) {
	limit := &speedLimiter{gate: make(chan struct{}, 1), bytesPerSecond: 32768, last: time.Now().Add(-time.Second)}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if err := limit.wait(ctx, 3000); err != nil {
		t.Fatalf("available credit should avoid an extra wait: %v", err)
	}
}

func TestQueueJobsShareSpeedLimit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, 8192)) }))
	defer server.Close()
	limit := &speedLimiter{gate: make(chan struct{}, 1), bytesPerSecond: 32768}
	ctx := context.WithValue(context.Background(), limiterContextKey{}, limit)
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	dir := t.TempDir()
	start := time.Now()
	for _, name := range []string{"first", "second"} {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			errors <- runQueueDownloadWithProgress(ctx, queueJob{URL: server.URL, Destination: filepath.Join(dir, name)}, 1, io.Discard)
		}(name)
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if elapsed := time.Since(start); elapsed < 450*time.Millisecond {
		t.Fatalf("queue-wide limit was exceeded: %v", elapsed)
	}
}

func TestCLISpeedLimit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, 16384)) }))
	defer server.Close()
	var out, stderr bytes.Buffer
	start := time.Now()
	code := run([]string{"-url", server.URL, "-out", filepath.Join(t.TempDir(), "file"), "-connections", "1", "-speed-limit", "32"}, &out, &stderr)
	if code != 0 || time.Since(start) < 450*time.Millisecond {
		t.Fatalf("speed-limited download: code %d, elapsed %v, error %s", code, time.Since(start), stderr.String())
	}
	if code := run([]string{"-speed-limit", "-1"}, &out, &stderr); code != 2 {
		t.Fatalf("negative limit returned %d", code)
	}
}
