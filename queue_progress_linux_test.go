//go:build linux

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestQueueProgressDoesNotBlockWhenDisplayIsBusy(t *testing.T) {
	updates := make(chan queueProgress)
	writer := &queueProgressWriter{id: 1, updates: updates}
	done := make(chan error, 1)
	go func() {
		for i := 0; i < 100; i++ {
			if _, err := writer.Write([]byte("Downloaded 1/10 bytes\n")); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("progress blocked transfer")
	}
}

func TestQueueProgressAndInspect(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ready := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "10")
		w.Header().Set("ETag", `"v1"`)
		io.WriteString(w, "12345")
		w.(http.Flusher).Flush()
		ready <- struct{}{}
		select {
		case <-time.After(1100 * time.Millisecond):
			io.WriteString(w, "67")
			w.(http.Flusher).Flush()
		case <-r.Context().Done():
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()
	input, commands := io.Pipe()
	defer commands.Close()
	out := &queueOutput{}
	done := make(chan error, 1)
	go func() { done <- runQueue(context.Background(), input, out, 1, 1) }()
	dest := filepath.Join(t.TempDir(), "file")
	fmt.Fprintf(commands, "add %s %s\nstart\n", server.URL, dest)
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	waitQueue(t, func() bool { return out.contains("Job 1: Downloaded 7/10 bytes") })
	fmt.Fprintln(commands, "inspect 1")
	waitQueue(t, func() bool { return out.contains("Transfer is active; saved bytes may lag live progress.") })
	fmt.Fprintln(commands, "pause 1")
	waitQueue(t, func() bool { return out.contains("Job 1: paused") })
	fmt.Fprintln(commands, "inspect 1")
	waitQueue(t, func() bool { return out.contains("Saved: 7/10 bytes") && out.contains("Saved data may be resumed") })
	fmt.Fprintln(commands, "quit")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestInspectExplainsRecoveryProblems(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dest := filepath.Join(t.TempDir(), "file")
	j := queueJob{ID: 1, URL: "https://example.com/file", Destination: dest, State: "paused"}
	var out bytes.Buffer
	if err := os.WriteFile(dest+".part", []byte("data"), 0600); err != nil {
		t.Fatal(err)
	}
	inspectQueueJob(&out, j)
	if !strings.Contains(out.String(), "no checkpoint; manual inspection") {
		t.Fatal(out.String())
	}
	out.Reset()
	j.State = "resetting"
	j.Cleanup = &cleanupIntent{Action: "restart"}
	inspectQueueJob(&out, j)
	if !strings.Contains(out.String(), "repeat restart 1") {
		t.Fatal(out.String())
	}
	out.Reset()
	j.State = "paused"
	j.Cleanup = nil
	path, err := checkpointLocation(dest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("bad"), 0600); err != nil {
		t.Fatal(err)
	}
	inspectQueueJob(&out, j)
	if !strings.Contains(out.String(), "invalid or does not match") {
		t.Fatal(out.String())
	}
}

func TestQueueParallelReportsLiveProgress(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("x"), minParallelSize)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		rr := r.Header.Get("Range")
		if rr == "bytes=0-0" {
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		var start, end int
		if _, err := fmt.Sscanf(rr, "bytes=%d-%d", &start, &end); err != nil {
			t.Errorf("invalid range %q", rr)
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		w.Write(payload[start : start+(end-start+1)/2])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	input, commands := io.Pipe()
	defer commands.Close()
	out := &queueOutput{}
	done := make(chan error, 1)
	go func() { done <- runQueue(context.Background(), input, out, 1, 4) }()
	dest := filepath.Join(t.TempDir(), "file")
	fmt.Fprintf(commands, "add %s %s\nstart\n", server.URL, dest)
	waitQueue(t, func() bool {
		out.mu.Lock()
		defer out.mu.Unlock()
		for _, line := range strings.Split(out.text, "\n") {
			var n int64
			if _, err := fmt.Sscanf(line, "Job 1: Downloaded %d/", &n); err == nil && n > 0 {
				return true
			}
		}
		return false
	})
	fmt.Fprintln(commands, "quit")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
