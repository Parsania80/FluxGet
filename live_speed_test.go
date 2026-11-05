package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLiveSpeedChangesWhileWaiting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"default_kib":1,"overrides":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	limit := &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: "download:1"}}
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- limit.wait(context.Background(), 1024) }()
	time.Sleep(120 * time.Millisecond)
	if err := os.WriteFile(path, []byte(`{"enabled":false,"default_kib":1,"overrides":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		if time.Since(start) > 800*time.Millisecond {
			t.Fatal("updated limit took too long")
		}
	case <-time.After(time.Second):
		t.Fatal("disabling limit did not unblock transfer")
	}
}

func TestLiveSpeedDefaultAndIndividualCeilings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"default_kib":100,"overrides":{"queue:1":25,"queue:2":200}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		key  string
		want int64
	}{{"queue:1", 25 * 1024}, {"queue:2", 100 * 1024}, {"queue:3", 100 * 1024}} {
		l := &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: path, key: tc.key}}
		if got := l.liveRate(); got != tc.want {
			t.Errorf("%s rate %d want %d", tc.key, got, tc.want)
		}
	}
}

func TestCLIAppliesLiveSpeedChange(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"enabled":true,"default_kib":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, 32*1024)) }))
	defer server.Close()
	done := make(chan int, 1)
	var out, stderr bytes.Buffer
	go func() {
		done <- run([]string{"-url", server.URL, "-out", filepath.Join(t.TempDir(), "file"), "-connections", "1", "-live-speed-file", path, "-live-speed-key", "download:0"}, &out, &stderr)
	}()
	select {
	case <-done:
		t.Fatal("download finished despite enabled low limit")
	case <-time.After(400 * time.Millisecond):
	}
	if err := os.WriteFile(path, []byte(`{"enabled":false,"default_kib":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		if code != 0 {
			t.Fatalf("download failed: %s", stderr.String())
		}
	case <-time.After(3 * time.Second):
		t.Fatal("download did not speed up after disabling limit")
	}
}

func TestQueueDownloadAppliesLiveSpeedChange(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "speed.json")
	if err := os.WriteFile(path, []byte(`{"enabled":false,"default_kib":0,"overrides":{"queue:7":2}}`), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(make([]byte, 32*1024)) }))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "file")
	ctx := context.WithValue(context.Background(), liveSpeedKey{}, path)
	done := make(chan error, 1)
	go func() {
		done <- runQueueDownloadWithProgress(ctx, queueJob{ID: 7, URL: server.URL, Destination: destination}, 1, io.Discard)
	}()
	select {
	case err := <-done:
		t.Fatalf("download finished despite low limit: %v", err)
	case <-time.After(400 * time.Millisecond):
	}
	if err := os.WriteFile(path, []byte(`{"enabled":false,"default_kib":0,"overrides":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queue download did not speed up")
	}
}
