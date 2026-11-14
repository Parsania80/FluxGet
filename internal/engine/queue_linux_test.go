//go:build linux

package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type queueOutput struct {
	mu   sync.Mutex
	text string
}

func (o *queueOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.text += string(p)
	return len(p), nil
}
func (o *queueOutput) contains(s string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return strings.Contains(o.text, s)
}
func waitQueue(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !f() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for queue")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestQueueConcurrencyAndPersistence(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	entered := make(chan struct{}, 3)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-release:
			io.WriteString(w, "done")
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	input, commands := io.Pipe()
	out := &queueOutput{}
	finished := make(chan error, 1)
	go func() { finished <- runQueue(context.Background(), input, out, 2, 1) }()
	dir := t.TempDir()
	for i := 1; i <= 3; i++ {
		fmt.Fprintf(commands, "add %s/file %s\n", server.URL, filepath.Join(dir, fmt.Sprint(i)))
	}
	fmt.Fprintln(commands, "start")
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("download did not start")
		}
	}
	select {
	case <-entered:
		t.Fatal("active limit exceeded")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	waitQueue(t, func() bool { return out.contains("Job 3: completed") })
	fmt.Fprintln(commands, "quit")
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	commands.Close()
	path, err := queuePath()
	if err != nil {
		t.Fatal(err)
	}
	q, err := loadQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range q.Jobs {
		if j.State != "completed" {
			t.Fatalf("job state %s", j.State)
		}
		b, err := os.ReadFile(j.Destination)
		if err != nil || string(b) != "done" {
			t.Fatalf("output %q: %v", b, err)
		}
	}
}

func TestQueueRestoresWithoutStartingAndPreservesFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := queuePath()
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "output")
	q := queueData{Version: 1, NextID: 2, Jobs: []queueJob{{ID: 1, URL: "http://127.0.0.1:1/file", Destination: dest, State: "running"}}}
	if err := saveQueue(path, q); err != nil {
		t.Fatal(err)
	}
	out := &queueOutput{}
	if err := runQueue(context.Background(), strings.NewReader("list\ncancel 1\nremove 1\nquit\n"), out, 5, 1); err != nil {
		t.Fatal(err)
	}
	if !out.contains("1\tpaused") || !out.contains("Job 1: canceled") {
		t.Fatalf("unexpected output: %s", out.text)
	}
	q, err = loadQueue(path)
	if err != nil || len(q.Jobs) != 0 {
		t.Fatalf("queue: %+v %v", q, err)
	}
}

func TestQueueRejectsForeignCheckpointAndCancellationErrors(t *testing.T) {
	if onlyCancellation(errors.Join(context.Canceled, errors.New("checkpoint failed"))) {
		t.Fatal("checkpoint failure treated as cancellation")
	}
	if !onlyCancellation(errors.Join(fmt.Errorf("stopped: %w", context.Canceled), nil)) {
		t.Fatal("cancellation not recognized")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dest := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(dest+".part", []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	err := runQueueDownload(context.Background(), queueJob{URL: "http://example.com/file", Destination: dest}, 1)
	if err == nil {
		t.Fatal("unowned partial accepted")
	}
	b, _ := os.ReadFile(dest + ".part")
	if string(b) != "keep" {
		t.Fatal("partial changed")
	}
}

func TestQueueExclusiveLock(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := queuePath()
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockQueue(path + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := runQueue(context.Background(), strings.NewReader("quit\n"), io.Discard, 5, 1); err == nil {
		t.Fatal("second queue acquired lock")
	}
}

func TestQueuePauseWaitsForCheckpoint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ready := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000000")
		w.Header().Set("ETag", `"stable"`)
		io.WriteString(w, "first bytes")
		w.(http.Flusher).Flush()
		ready <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	input, commands := io.Pipe()
	out := &queueOutput{}
	finished := make(chan error, 1)
	go func() { finished <- runQueue(context.Background(), input, out, 1, 1) }()
	dest := filepath.Join(t.TempDir(), "file")
	fmt.Fprintf(commands, "add %s/file %s\nstart\n", server.URL, dest)
	select {
	case <-ready:
	case <-time.After(5 * time.Second):
		t.Fatal("download did not start")
	}
	cp, err := checkpointLocation(dest)
	if err != nil {
		t.Fatal(err)
	}
	waitQueue(t, func() bool { _, err := os.Stat(cp); return err == nil })
	fmt.Fprintln(commands, "pause 1")
	waitQueue(t, func() bool { return out.contains("Job 1: paused") })
	c, err := loadCheckpoint(cp)
	if err != nil || c.State != "interrupted" {
		t.Fatalf("checkpoint: %+v %v", c, err)
	}
	fmt.Fprintln(commands, "quit")
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	commands.Close()
	qpath, _ := queuePath()
	q, err := loadQueue(qpath)
	if err != nil || q.Jobs[0].State != "paused" {
		t.Fatalf("queue: %+v %v", q, err)
	}
}

func TestQueueImportAndResumeSavedJob(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := []byte("resumed content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		switch r.Header.Get("Range") {
		case "":
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.Write(payload[:6])
		case "bytes=0-0":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-0/%d", len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[:1])
		case "bytes=6-":
			w.Header().Set("Content-Range", fmt.Sprintf("bytes 6-%d/%d", len(payload)-1, len(payload)))
			w.WriteHeader(http.StatusPartialContent)
			w.Write(payload[6:])
		default:
			t.Errorf("unexpected range %q", r.Header.Get("Range"))
			w.WriteHeader(400)
		}
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "file with spaces")
	if _, err := download(context.Background(), server.Client(), server.URL, dest, "", io.Discard); err == nil {
		t.Fatal("expected interrupted download")
	}
	input, commands := io.Pipe()
	out := &queueOutput{}
	finished := make(chan error, 1)
	go func() { finished <- runQueue(context.Background(), input, out, 1, 1) }()
	fmt.Fprintf(commands, "import %s\nresume 1\nstart\n", dest)
	waitQueue(t, func() bool { return out.contains("Job 1: completed") })
	fmt.Fprintln(commands, "quit")
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	commands.Close()
	requireContents(t, dest, payload)
}

func TestQueueRejectsInvalidMetadata(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := queuePath()
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{`{}`, `{"version":1,"next_id":2,"jobs":[{"id":1,"url":"http://example.com","destination":"relative","state":"queued"}]}`, `{"version":1,"next_id":1,"jobs":[]} trailing`} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadQueue(path); err == nil {
			t.Fatalf("accepted invalid metadata %s", data)
		}
	}
}
