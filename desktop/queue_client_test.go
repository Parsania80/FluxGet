package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestDesktopQueueUsesPersistentCLIScheduler(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "fluxget")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	payload := bytes.Repeat([]byte("abcdefghij"), 22000)
	var first atomic.Bool
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") == "" && first.CompareAndSwap(false, true) {
			w.Header().Set("Content-Length", "220000")
			_, _ = w.Write(payload[:50000])
			w.(http.Flusher).Flush()
			close(started)
			<-r.Context().Done()
			return
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	events := make(chan string, 1024)
	stopped := make(chan error, 1)
	q, err := startQueue(binary, 1, 0, func(line string) { events <- line }, func(err error) { stopped <- err })
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		select {
		case <-q.done:
		default:
			_ = q.send("quit")
			<-q.done
		}
	}()
	send := func(s string) {
		t.Helper()
		if err := q.send(s); err != nil {
			t.Fatal(err)
		}
	}
	waitSnapshot := func(match func([]queueEntry) bool) []queueEntry {
		t.Helper()
		deadline := time.After(10 * time.Second)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case line := <-events:
				if strings.HasPrefix(line, "QUEUE_JSON ") {
					var snapshot []queueEntry
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "QUEUE_JSON ")), &snapshot); err != nil {
						t.Fatal(err)
					}
					if match(snapshot) {
						return snapshot
					}
				}
			case <-ticker.C:
				send("snapshot")
			case err := <-stopped:
				t.Fatalf("queue exited: %v", err)
			case <-deadline:
				t.Fatal("timed out waiting for queue state")
			}
		}
	}
	dir := t.TempDir()
	firstPath := filepath.Join(dir, "first file.bin")
	secondPath := filepath.Join(dir, "second file.bin")
	for _, dest := range []string{firstPath, secondPath} {
		data, err := json.Marshal(struct {
			URL         string `json:"url"`
			Destination string `json:"destination"`
		}{server.URL, dest})
		if err != nil {
			t.Fatal(err)
		}
		send("add-json " + string(data))
	}
	waitSnapshot(func(j []queueEntry) bool { return len(j) == 2 && j[0].State == "queued" && j[1].State == "queued" })
	lockedEvents := make(chan string, 8)
	lockedDone := make(chan error, 1)
	other, err := startQueue(binary, 1, 0, func(line string) { lockedEvents <- line }, func(err error) { lockedDone <- err })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-lockedDone:
		if err == nil {
			t.Fatal("second queue unexpectedly acquired ownership")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("second queue did not report lock conflict")
	}
	<-other.done
	select {
	case line := <-lockedEvents:
		if !strings.Contains(line, "Queue failed:") {
			t.Fatalf("missing lock explanation: %s", line)
		}
	default:
		t.Fatal("missing lock explanation")
	}
	send("start")
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("first download did not start")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if info, err := os.Stat(firstPath + ".part"); err == nil && info.Size() >= 50000 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial was not written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	send("pause 1")
	waitSnapshot(func(j []queueEntry) bool { return len(j) == 2 && j[0].State == "paused" })
	send("resume 1")
	send("start")
	waitSnapshot(func(j []queueEntry) bool {
		return len(j) == 2 && j[0].State == "completed" && j[1].State == "completed"
	})
	for _, path := range []string{firstPath, secondPath} {
		data, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("bad file %s: %v", path, err)
		}
	}
	send("quit")
	select {
	case <-q.done:
	case <-time.After(10 * time.Second):
		t.Fatal("queue did not stop")
	}
	events2 := make(chan string, 32)
	q2, err := startQueue(binary, 1, 0, func(line string) { events2 <- line }, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q2.send("quit"); <-q2.done }()
	if err := q2.send("snapshot"); err != nil {
		t.Fatal(err)
	}
	deadline2 := time.After(10 * time.Second)
	for {
		select {
		case line := <-events2:
			if strings.HasPrefix(line, "QUEUE_JSON ") {
				var got []queueEntry
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "QUEUE_JSON ")), &got); err != nil || len(got) != 2 || got[0].State != "completed" || got[1].State != "completed" {
					t.Fatalf("persisted queue: %+v, %v", got, err)
				}
				return
			}
		case <-deadline2:
			t.Fatal("queue did not reopen")
		}
	}
}

func TestDesktopQueueRunsJobsConcurrently(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "fluxget")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	started := make(chan struct{}, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusOK)
			return
		}
		_, _ = w.Write([]byte("started"))
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	q, err := startQueue(binary, 2, 0, func(string) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.send("quit"); <-q.done }()
	for _, name := range []string{"one", "two"} {
		data, _ := json.Marshal(struct {
			URL         string `json:"url"`
			Destination string `json:"destination"`
		}{server.URL, filepath.Join(t.TempDir(), name)})
		if err := q.send("add-json " + string(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.send("start"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("both downloads did not start concurrently")
		}
	}
}
