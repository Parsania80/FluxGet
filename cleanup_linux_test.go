//go:build linux

package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func ownedCleanupFixture(t *testing.T) (queueData, string, string, string, []byte) {
	t.Helper()
	payload := bytes.Repeat([]byte("owned"), 200)
	dest, cp, c := chunkFixture(t, "http://example.com/file", payload, false)
	f, err := os.OpenFile(dest+".part", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := identifyPartial(f)
	if err != nil {
		t.Fatal(err)
	}
	c.PartialIdentity = id
	c.PartialToken = createPartialToken(f)
	if c.PartialToken == "" {
		t.Skip("partial ownership markers are unavailable")
	}
	if err := saveCheckpoint(cp, f, c); err != nil {
		t.Fatal(err)
	}
	f.Close()
	qp, err := queuePath()
	if err != nil {
		t.Fatal(err)
	}
	q := queueData{Version: 1, NextID: 2, Jobs: []queueJob{{ID: 1, URL: c.URL, Destination: dest, State: "paused"}}}
	if err := saveQueue(qp, q); err != nil {
		t.Fatal(err)
	}
	return q, qp, cp, dest, payload
}

func TestCleanupRefusesUnownedOrConflictingFiles(t *testing.T) {
	cases := []string{"legacy", "marker", "ready", "replaced", "symlink", "hardlink", "modified", "final", "locked", "checkpoint symlink", "no checkpoint"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			q, qp, cp, dest, _ := ownedCleanupFixture(t)
			var lock *jobLock
			var f *os.File
			switch name {
			case "legacy":
				c, _ := loadCheckpoint(cp)
				c.PartialIdentity = nil
				c.PartialToken = ""
				f, _ = os.OpenFile(dest+".part", os.O_RDWR, 0)
				if err := saveCheckpoint(cp, f, c); err != nil {
					t.Fatal(err)
				}
				f.Close()
			case "ready":
				c, _ := loadCheckpoint(cp)
				c.Version = 2
				c.Chunks = nil
				c.Completed = *c.Total
				c.State = "ready"
				sum := sha256.Sum256(bytes.Repeat([]byte("owned"), 200))
				c.PrefixSHA256 = fmt.Sprintf("%x", sum)
				f, _ = os.OpenFile(dest+".part", os.O_RDWR, 0)
				if err := saveCheckpoint(cp, f, c); err != nil {
					t.Fatal(err)
				}
				f.Close()
			case "marker":
				if err := syscall.Removexattr(dest+".part", partialMarkerName); err != nil {
					t.Fatal(err)
				}
			case "replaced":
				if err := os.Rename(dest+".part", dest+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dest+".part", []byte("other"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(dest+".part", dest+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(dest+".old", dest+".part"); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(dest+".part", dest+".other"); err != nil {
					t.Fatal(err)
				}
			case "modified":
				f, _ = os.OpenFile(dest+".part", os.O_WRONLY, 0)
				if _, err := f.WriteAt([]byte("!"), int64(len(bytes.Repeat([]byte("owned"), 200))/2)); err != nil {
					t.Fatal(err)
				}
				f.Close()
			case "final":
				if err := os.WriteFile(dest, []byte("final"), 0600); err != nil {
					t.Fatal(err)
				}
			case "locked":
				f, _ = os.OpenFile(dest+".part", os.O_RDWR, 0)
				var err error
				lock, err = lockJob(cp+".lock", f)
				if err != nil {
					t.Fatal(err)
				}
				defer lock.Close()
				defer f.Close()
			case "checkpoint symlink":
				if err := os.Rename(cp, cp+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(cp+".old", cp); err != nil {
					t.Fatal(err)
				}
			case "no checkpoint":
				if err := os.Remove(cp); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.Lstat(dest + ".part")
			if err != nil {
				t.Fatal(err)
			}
			fatal, err := cleanupQueueJob(&q, 0, qp, "restart")
			if err == nil || fatal {
				t.Fatalf("cleanup accepted %s: fatal %v err %v", name, fatal, err)
			}
			after, err := os.Lstat(dest + ".part")
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("partial changed: %v", err)
			}
			if name == "final" {
				requireContents(t, dest, []byte("final"))
			} else {
				requireMissing(t, dest)
			}
		})
	}
}

func TestCleanupExplicitActions(t *testing.T) {
	for _, action := range []string{"restart", "discard"} {
		t.Run(action, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			q, qp, cp, dest, _ := ownedCleanupFixture(t)
			fatal, err := cleanupQueueJob(&q, 0, qp, action)
			if err != nil || fatal {
				t.Fatalf("cleanup: %v", err)
			}
			want := "queued"
			if action == "discard" {
				want = "canceled"
			}
			saved, err := loadQueue(qp)
			if err != nil || saved.Jobs[0].State != want || saved.Jobs[0].Cleanup != nil {
				t.Fatalf("queue %+v %v", saved, err)
			}
			requireMissing(t, cp)
			requireMissing(t, dest+".part")
			requireMissing(t, dest)
		})
	}
}

func TestQueueFreshRestartRetainsChecksum(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := []byte("complete data from the server")
	sum := sha256.Sum256(payload)
	checksum := fmt.Sprintf("%x", sum)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		if requests.Add(1) == 1 {
			w.Write(payload[:6])
			return
		}
		w.Write(payload)
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "file")
	if _, err := download(context.Background(), server.Client(), server.URL, dest, checksum, io.Discard); err == nil {
		t.Fatal("expected interrupted download")
	}
	cp, _ := checkpointLocation(dest)
	saved, _ := loadCheckpoint(cp)
	if saved.PartialToken == "" {
		t.Skip("partial ownership markers are unavailable")
	}
	input, commands := io.Pipe()
	defer commands.Close()
	out := &queueOutput{}
	done := make(chan error, 1)
	go func() { done <- runQueue(context.Background(), input, out, 1, 1) }()
	fmt.Fprintf(commands, "import %s\nrestart 1\nstart\n", dest)
	waitQueue(t, func() bool { return out.contains("Job 1: completed") })
	fmt.Fprintln(commands, "quit")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
	if _, err := syscall.Getxattr(dest, partialMarkerName, make([]byte, 32)); !errors.Is(err, syscall.ENODATA) {
		t.Fatalf("ownership marker remained: %v", err)
	}
	qp, _ := queuePath()
	q, err := loadQueue(qp)
	if err != nil || !strings.EqualFold(q.Jobs[0].ExpectedSHA256, checksum) {
		t.Fatalf("checksum lost: %+v %v", q, err)
	}
}

func TestCleanupCrashRecovery(t *testing.T) {
	if stage := os.Getenv("FLUXGET_CLEANUP_CRASH"); stage != "" {
		qp, err := queuePath()
		if err != nil {
			t.Fatal(err)
		}
		q, err := loadQueue(qp)
		if err != nil {
			t.Fatal(err)
		}
		hook := func(s string) {
			if s == stage {
				fmt.Fprintln(os.Stdout, "ready")
				syscall.Kill(os.Getpid(), syscall.SIGSTOP)
			}
		}
		_, err = cleanupQueueJobWithHook(&q, 0, qp, "restart", hook)
		t.Fatalf("crash stage not reached: %v", err)
	}
	for _, stage := range []string{"intent", "partial", "checkpoint"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			_, qp, cp, dest, _ := ownedCleanupFixture(t)
			killCleanupAtStage(t, stage)
			q, err := loadQueue(qp)
			if err != nil || q.Jobs[0].State != "resetting" {
				t.Fatalf("queue %+v %v", q, err)
			}
			if _, err := os.Lstat(dest); !os.IsNotExist(err) {
				t.Fatalf("destination exists: %v", err)
			}
			if stage == "checkpoint" {
				requireMissing(t, cp)
				requireMissing(t, dest+".part")
			}
			fatal, err := cleanupQueueJob(&q, 0, qp, "restart")
			if fatal || err != nil {
				t.Fatalf("finish interrupted cleanup: %v", err)
			}
			if q.Jobs[0].State != "queued" {
				t.Fatalf("state %s", q.Jobs[0].State)
			}
			requireMissing(t, cp)
			requireMissing(t, dest+".part")
		})
	}
}

func killCleanupAtStage(t *testing.T, stage string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestCleanupCrashRecovery$")
	cmd.Env = append(os.Environ(), "FLUXGET_CLEANUP_CRASH="+stage)
	cmd.Stderr = os.Stderr
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	ready := make(chan string, 1)
	go func() { s, _ := bufio.NewReader(output).ReadString('\n'); ready <- s }()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("stage %s: %q", stage, line)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cleanup stage not reached")
	}
	// The child prints readiness before SIGSTOP takes effect. Wait for the
	// stopped state so it cannot finish the cleanup before the parent kills it.
	var status syscall.WaitStatus
	if _, err := syscall.Wait4(cmd.Process.Pid, &status, syscall.WUNTRACED, nil); err != nil || !status.Stopped() {
		t.Fatalf("wait for cleanup crash stop: %v status=%v", err, status)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected killed process")
	}
}

func TestInterruptedCleanupRefusesReplacement(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, qp, cp, dest, _ := ownedCleanupFixture(t)
	killCleanupAtStage(t, "partial")
	if err := os.WriteFile(dest+".part", []byte("new unrelated file"), 0600); err != nil {
		t.Fatal(err)
	}
	q, err := loadQueue(qp)
	if err != nil {
		t.Fatal(err)
	}
	fatal, err := cleanupQueueJob(&q, 0, qp, "restart")
	if err == nil || fatal {
		t.Fatalf("replacement accepted: %v", err)
	}
	requireContents(t, dest+".part", []byte("new unrelated file"))
	if _, err := os.Stat(cp); err != nil {
		t.Fatal(err)
	}
}

func TestQueueCleanupRejectsActiveJobAndThenDiscards(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ready := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Header().Set("ETag", `"v1"`)
		io.WriteString(w, "partial")
		w.(http.Flusher).Flush()
		ready <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	input, commands := io.Pipe()
	defer commands.Close()
	out := &queueOutput{}
	done := make(chan error, 1)
	go func() { done <- runQueue(context.Background(), input, out, 1, 1) }()
	dest := filepath.Join(t.TempDir(), "file")
	cp, _ := checkpointLocation(dest)
	fmt.Fprintf(commands, "add %s %s\nstart\n", server.URL, dest)
	<-ready
	waitQueue(t, func() bool { _, e := os.Stat(cp); return e == nil })
	c, err := loadCheckpoint(cp)
	if err != nil {
		t.Fatal(err)
	}
	if c.PartialToken == "" {
		t.Skip("partial ownership markers are unavailable")
	}
	fmt.Fprintln(commands, "discard 1")
	waitQueue(t, func() bool { return out.contains("Pause or cancel the job before cleanup.") })
	if _, err := os.Stat(dest + ".part"); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(commands, "pause 1")
	waitQueue(t, func() bool { return out.contains("Job 1: paused") })
	fmt.Fprintln(commands, "discard 1")
	waitQueue(t, func() bool { return out.contains("Job 1: canceled") })
	requireMissing(t, dest+".part")
	requireMissing(t, cp)
	fmt.Fprintln(commands, "quit")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestCleanupPermissionFailuresPreserveRecovery(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may bypass filesystem permissions")
	}
	for _, where := range []string{"queue", "partial", "checkpoint"} {
		t.Run(where, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			q, qp, cp, dest, payload := ownedCleanupFixture(t)
			if where == "checkpoint" {
				lock, err := lockJobRecord(cp + ".lock")
				if err != nil {
					t.Fatal(err)
				}
				lock.Close()
			}
			dir := filepath.Dir(qp)
			if where == "partial" {
				dir = filepath.Dir(dest)
			} else if where == "checkpoint" {
				dir = filepath.Dir(cp)
			}
			if err := os.Chmod(dir, 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Chmod(dir, 0700) })
			fatal, err := cleanupQueueJob(&q, 0, qp, "restart")
			if !errors.Is(err, os.ErrPermission) || fatal != (where == "queue") {
				t.Fatalf("fatal=%v error=%v", fatal, err)
			}
			onDisk, err := loadQueue(qp)
			if err != nil {
				t.Fatal(err)
			}
			if where == "queue" {
				if onDisk.Jobs[0].State != "paused" {
					t.Fatal("unsaved cleanup intent")
				}
				requireContents(t, dest+".part", payload)
			} else {
				if onDisk.Jobs[0].State != "resetting" {
					t.Fatalf("state %s", onDisk.Jobs[0].State)
				}
				if where == "partial" {
					requireContents(t, dest+".part", payload)
				} else {
					requireMissing(t, dest+".part")
				}
			}
			if err := os.Chmod(dir, 0700); err != nil {
				t.Fatal(err)
			}
			fatal, err = cleanupQueueJob(&onDisk, 0, qp, "restart")
			if fatal || err != nil {
				t.Fatalf("recovery: %v", err)
			}
			if onDisk.Jobs[0].State != "queued" {
				t.Fatal("job not queued")
			}
			requireMissing(t, cp)
			requireMissing(t, dest+".part")
		})
	}
}
