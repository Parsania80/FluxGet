//go:build linux

package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestCrashDuringCheckpointPublication(t *testing.T) {
	if os.Getenv("FLUXGET_CRASH_STAGE") != "" {
		runCrashChild(t, "checkpoint")
		return
	}
	for _, stage := range []string{"checkpoint-written", "checkpoint-synced", "checkpoint-published"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			destination := filepath.Join(dir, "file")
			partial, err := os.Create(destination + ".part")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := partial.WriteString("abcdefgh"); err != nil {
				t.Fatal(err)
			}
			c := checkpoint{Version: 1, URL: "https://example.com/file", Destination: destination, Partial: partial.Name(), Completed: 3}
			path := filepath.Join(dir, "job.json")
			if err := saveCheckpoint(path, partial, c); err != nil {
				t.Fatal(err)
			}
			partial.Close()
			killAtStage(t, "TestCrashDuringCheckpointPublication", stage, dir)
			got, err := loadCheckpoint(path)
			if err != nil {
				t.Fatalf("checkpoint is unreadable after crash: %v", err)
			}
			want := int64(3)
			if stage == "checkpoint-published" {
				want = 6
			}
			if got.Completed != want {
				t.Fatalf("checkpoint completed = %d, want %d", got.Completed, want)
			}
			requireContents(t, destination+".part", []byte("abcdefgh"))
			requireMissing(t, destination)
			partial, err = os.OpenFile(destination+".part", os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer partial.Close()
			lock, err := lockJob(path+".lock", partial)
			if err != nil {
				t.Fatalf("crashed process retained lock: %v", err)
			}
			lock.Close()
		})
	}
}

func TestCrashDuringFinalPublication(t *testing.T) {
	if os.Getenv("FLUXGET_CRASH_STAGE") != "" {
		runCrashChild(t, "publication")
		return
	}
	for _, stage := range []string{"before-link", "after-link", "after-partial-removal"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir := t.TempDir()
			destination := filepath.Join(dir, "file")
			payload := []byte("complete verified download")
			if err := os.WriteFile(destination+".part", payload, 0o600); err != nil {
				t.Fatal(err)
			}
			path, err := checkpointLocation(destination)
			if err != nil {
				t.Fatal(err)
			}
			partial, err := os.OpenFile(destination+".part", os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			total := int64(len(payload))
			prefix := sha256.Sum256(payload)
			c := checkpoint{Version: 1, URL: "https://example.com/file", ResolvedURL: "https://example.com/file", Destination: destination, Partial: partial.Name(), ETag: `"v1"`, Total: &total, Completed: total, PrefixSHA256: fmt.Sprintf("%x", prefix), State: "ready"}
			if err := saveCheckpoint(path, partial, c); err != nil {
				t.Fatal(err)
			}
			partial.Close()
			killAtStage(t, "TestCrashDuringFinalPublication", stage, dir)
			if saved, err := loadCheckpoint(path); err != nil || saved.Completed != total || saved.State != "ready" {
				t.Fatalf("checkpoint was not preserved: %+v, %v", saved, err)
			}
			if stage == "before-link" {
				requireMissing(t, destination)
			} else {
				requireContents(t, destination, payload)
			}
			if stage == "after-partial-removal" {
				requireMissing(t, destination+".part")
			} else {
				requireContents(t, destination+".part", payload)
			}
			var stdout, stderr bytes.Buffer
			if code := run([]string{"-resume", "-out", destination}, &stdout, &stderr); code != 0 {
				t.Fatalf("reconcile exit %d: %s", code, stderr.String())
			}
			requireContents(t, destination, payload)
			requireMissing(t, destination+".part")
			requireMissing(t, path)
		})
	}
}

func runCrashChild(t *testing.T, operation string) {
	dir := os.Getenv("FLUXGET_CRASH_DIR")
	stage := os.Getenv("FLUXGET_CRASH_STAGE")
	destination := filepath.Join(dir, "file")
	hook := func(current string) {
		if current == stage {
			fmt.Fprintln(os.Stdout, "ready")
			syscall.Kill(os.Getpid(), syscall.SIGSTOP)
		}
	}
	if operation == "publication" {
		if err := publishFile(destination+".part", destination, hook); err != nil {
			t.Fatal(err)
		}
	} else {
		partial, err := os.OpenFile(destination+".part", os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer partial.Close()
		path := filepath.Join(dir, "job.json")
		lock, err := lockJob(path+".lock", partial)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		c := checkpoint{Version: 1, URL: "https://example.com/file", Destination: destination, Partial: partial.Name(), Completed: 6}
		if err := saveCheckpointWithHook(path, partial, c, hook); err != nil {
			t.Fatal(err)
		}
	}
	t.Fatal("crash stage was not reached")
}

func killAtStage(t *testing.T, testName, stage, dir string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^"+testName+"$")
	cmd.Env = append(os.Environ(), "FLUXGET_CRASH_STAGE="+stage, "FLUXGET_CRASH_DIR="+dir)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
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
	go func() {
		line, _ := bufio.NewReader(output).ReadString('\n')
		ready <- line
	}()
	select {
	case line := <-ready:
		if line != "ready\n" {
			t.Fatalf("child did not reach %s: %q", stage, line)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("child did not reach %s", stage)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("expected the child process to be killed")
	}
}
