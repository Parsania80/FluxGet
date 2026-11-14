//go:build linux

package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestJobLockExclusiveAcrossProcesses(t *testing.T) {
	dir := t.TempDir()
	partialPath := filepath.Join(dir, "file.part")
	if os.Getenv("FLUXGET_LOCK_TEST_CHILD") == "1" {
		partial, err := os.OpenFile(os.Getenv("FLUXGET_LOCK_TEST_PARTIAL"), os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer partial.Close()
		if lock, err := lockJob(os.Getenv("FLUXGET_LOCK_TEST_JOB"), partial); err == nil {
			lock.Close()
			t.Fatal("another process acquired an owned job")
		}
		return
	}
	partial, err := os.Create(partialPath)
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	jobPath := filepath.Join(dir, "job.lock")
	lock, err := lockJob(jobPath, partial)
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestJobLockExclusiveAcrossProcesses$")
	child.Env = append(os.Environ(), "FLUXGET_LOCK_TEST_CHILD=1", "FLUXGET_LOCK_TEST_PARTIAL="+partialPath, "FLUXGET_LOCK_TEST_JOB="+jobPath)
	if output, err := child.CombinedOutput(); err != nil {
		lock.Close()
		t.Fatalf("child process: %v: %s", err, output)
	}
	if other, err := lockJob(filepath.Join(dir, "other.lock"), partial); err == nil {
		other.Close()
		lock.Close()
		t.Fatal("same partial file was locked as another job")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := lockJob(jobPath, partial)
	if err != nil {
		t.Fatalf("could not reacquire released lock: %v", err)
	}
	if err := again.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestJobLockRejectsSymlinksAndPartialAsLock(t *testing.T) {
	dir := t.TempDir()
	partial, err := os.Create(filepath.Join(dir, "file.part"))
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(partial.Name(), link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{link, partial.Name()} {
		if lock, err := lockJob(path, partial); err == nil {
			lock.Close()
			t.Fatalf("accepted lock path %s", path)
		}
	}
}
