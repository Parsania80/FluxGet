//go:build linux

package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueRepeatedCrashAndTransientRecovery(t *testing.T) {
	if os.Getenv("FLUXGET_QUEUE_RECOVERY_CHILD") == "1" {
		connections := 4
		if os.Getenv("FLUXGET_QUEUE_RECOVERY_LIMIT") == "2" {
			connections = 2
		}
		if err := runQueue(context.Background(), os.Stdin, io.Discard, 1, connections); err != nil {
			t.Fatal(err)
		}
		return
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("recovery"), 200000)
	step := int64(len(payload)) / 4
	var phase, failProbe, failChunk, requests, active, maxActive atomic.Int32
	var mu sync.Mutex
	var finalRanges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		rr := r.Header.Get("Range")
		current := phase.Load()
		if current == 2 && r.URL.Path == "/first" {
			mu.Lock()
			finalRanges = append(finalRanges, rr)
			mu.Unlock()
			if rr == "bytes=0-0" && failProbe.CompareAndSwap(0, 1) {
				w.WriteHeader(503)
				return
			}
			if rr != "bytes=0-0" && failChunk.CompareAndSwap(0, 1) {
				w.WriteHeader(503)
				return
			}
		}
		if current == 2 {
			n := active.Add(1)
			for {
				old := maxActive.Load()
				if n <= old || maxActive.CompareAndSwap(old, n) {
					break
				}
			}
			defer active.Add(-1)
		}
		w.Header().Set("ETag", `"v1"`)
		if current < 2 && rr != "bytes=0-0" {
			allowed := fmt.Sprintf("bytes=%d-%d", int64(current+1)*step, int64(current+2)*step-1)
			if rr != allowed {
				<-r.Context().Done()
				return
			}
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	var children []*exec.Cmd
	defer func() {
		for _, child := range children {
			if child.ProcessState == nil {
				child.Process.Kill()
				child.Wait()
			}
		}
	}()
	dir := t.TempDir()
	first := filepath.Join(dir, "first")
	second := filepath.Join(dir, "second")
	cp, err := checkpointLocation(first)
	if err != nil {
		t.Fatal(err)
	}
	qp, err := queuePath()
	if err != nil {
		t.Fatal(err)
	}
	startChild := func(limit string) (*exec.Cmd, io.WriteCloser) {
		t.Helper()
		cmd := exec.Command(os.Args[0], "-test.run=^TestQueueRepeatedCrashAndTransientRecovery$")
		cmd.Env = append(os.Environ(), "FLUXGET_QUEUE_RECOVERY_CHILD=1", "FLUXGET_QUEUE_RECOVERY_LIMIT="+limit)
		cmd.Stderr = os.Stderr
		input, err := cmd.StdinPipe()
		if err != nil {
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		children = append(children, cmd)
		t.Cleanup(func() {
			input.Close()
			if cmd.ProcessState == nil {
				cmd.Process.Kill()
				cmd.Wait()
			}
		})
		return cmd, input
	}
	kill := func(cmd *exec.Cmd) {
		t.Helper()
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := cmd.Wait(); err == nil {
			t.Fatal("expected killed queue process")
		}
	}
	state := func(want string) bool {
		q, e := loadQueue(qp)
		return e == nil && len(q.Jobs) == 2 && q.Jobs[0].State == want && q.Jobs[1].State == "queued"
	}
	cmd, input := startChild("4")
	fmt.Fprintf(input, "add %s/first %s\nadd %s/second %s\nstart\n", server.URL, first, server.URL, second)
	waitQueue(t, func() bool {
		c, e := loadCheckpoint(cp)
		return state("running") && e == nil && len(c.Chunks) == 4 && c.Chunks[1].SHA256 != ""
	})
	kill(cmd)
	requireMissing(t, first)
	requireMissing(t, second)
	phase.Store(1)
	cmd, input = startChild("4")
	waitQueue(t, func() bool { return state("paused") })
	oldRequests := requests.Load()
	time.Sleep(120 * time.Millisecond)
	if requests.Load() != oldRequests {
		t.Fatal("queue restarted network activity automatically")
	}
	fmt.Fprintln(input, "resume 1\nstart")
	waitQueue(t, func() bool {
		c, e := loadCheckpoint(cp)
		return state("running") && e == nil && c.Chunks[1].SHA256 != "" && c.Chunks[2].SHA256 != ""
	})
	kill(cmd)
	requireMissing(t, first)
	requireMissing(t, second)
	phase.Store(2)
	cmd, input = startChild("2")
	waitQueue(t, func() bool { return state("paused") })
	oldRequests = requests.Load()
	time.Sleep(120 * time.Millisecond)
	if requests.Load() != oldRequests {
		t.Fatal("second queue restart started downloads")
	}
	fmt.Fprintln(input, "resume 1\nstart")
	deadline := time.Now().Add(15 * time.Second)
	for {
		q, e := loadQueue(qp)
		if e == nil {
			activeJobs := 0
			for _, job := range q.Jobs {
				if job.State == "running" {
					activeJobs++
				}
			}
			if activeJobs > 1 {
				t.Fatalf("active job limit exceeded: %+v", q.Jobs)
			}
		}
		if e == nil && len(q.Jobs) == 2 && q.Jobs[0].State == "completed" && q.Jobs[1].State == "completed" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("queue did not complete after retries: %+v %v", q, e)
		}
		time.Sleep(10 * time.Millisecond)
	}
	fmt.Fprintln(input, "quit")
	input.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	requireContents(t, first, payload)
	requireContents(t, second, payload)
	requireMissing(t, cp)
	if failProbe.Load() != 1 || failChunk.Load() != 1 {
		t.Fatalf("transient responses not exercised: probe=%d chunk=%d", failProbe.Load(), failChunk.Load())
	}
	if maxActive.Load() > 2 {
		t.Fatalf("connection limit exceeded: %d", maxActive.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	for _, rr := range finalRanges {
		for _, index := range []int64{1, 2} {
			if rr == fmt.Sprintf("bytes=%d-%d", index*step, (index+1)*step-1) {
				t.Fatalf("re-requested saved chunk %s; ranges: %v", rr, finalRanges)
			}
		}
	}
}

func TestQueuePauseCancelsRetryBackoff(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var requests atomic.Int32
	responded := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Retry-After", "2")
		w.WriteHeader(503)
		select {
		case responded <- struct{}{}:
		default:
		}
	}))
	defer server.Close()
	input, commands := io.Pipe()
	defer commands.Close()
	out := &queueOutput{}
	done := make(chan error, 1)
	go func() { done <- runQueue(context.Background(), input, out, 1, 4) }()
	dest := filepath.Join(t.TempDir(), "file")
	fmt.Fprintf(commands, "add %s %s\nstart\n", server.URL, dest)
	select {
	case <-responded:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach server")
	}
	fmt.Fprintln(commands, "pause 1")
	waitQueue(t, func() bool { return out.contains("Job 1: paused") })
	time.Sleep(2300 * time.Millisecond)
	if requests.Load() != 1 {
		t.Fatalf("retry occurred after pause: %d requests", requests.Load())
	}
	requireMissing(t, dest)
	requireMissing(t, dest+".part")
	fmt.Fprintln(commands, "quit")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
