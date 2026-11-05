//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestListJobsWithoutConfigDirectory(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-list"}, &stdout, &stderr); code != 0 || stdout.String() != "No saved jobs.\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
	if _, err := os.Stat(filepath.Join(config, "fluxget")); !os.IsNotExist(err) {
		t.Fatalf("listing created a configuration directory: %v", err)
	}
}

func TestListJobsReportsActiveInterruptedAndMalformed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	destination := filepath.Join(t.TempDir(), "file")
	partial, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	if _, err := partial.WriteString("saved"); err != nil {
		t.Fatal(err)
	}
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	total := int64(10)
	sum := sha256.Sum256([]byte("saved"))
	c := checkpoint{Version: 2, URL: "https://example.com/file?private=value", ResolvedURL: "https://example.com/file", Destination: destination, Partial: partial.Name(), ETag: `"v1"`, Total: &total, Completed: 5, PrefixSHA256: fmt.Sprintf("%x", sum), State: "downloading"}
	lock, err := lockJob(path+".lock", partial)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveCheckpoint(path, partial, c); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	bad := filepath.Join(filepath.Dir(path), "broken.json")
	if err := os.WriteFile(bad, []byte("{"), 0o600); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err := os.Symlink(path, filepath.Join(filepath.Dir(path), "link.json")); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "wrong-name.json"), before, 0o600); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := listJobs(&output); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "active\t5/10 bytes") || !strings.Contains(output.String(), "needs-attention\t?\t\"broken.json\"") || !strings.Contains(output.String(), "needs-attention\t?\t\"link.json\"") || !strings.Contains(output.String(), "needs-attention\t?\t\"wrong-name.json\"") || strings.Contains(output.String(), "private=value") {
		lock.Close()
		t.Fatalf("unexpected active listing: %q", output.String())
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := listJobs(&output); err != nil || !strings.Contains(output.String(), "interrupted\t5/10 bytes") {
		t.Fatalf("unexpected interrupted listing: %q, %v", output.String(), err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("listing changed persisted state: %v", err)
	}
}

func TestInterruptedJobStateIsPersistedAndListed(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.Write([]byte("partial"))
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "file")
	if _, err := download(context.Background(), server.Client(), server.URL, destination, "", nil); err == nil {
		t.Fatal("expected interrupted transfer")
	}
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadCheckpoint(path)
	if err != nil || c.Version != 2 || c.State != "interrupted" || c.Completed != 7 {
		t.Fatalf("checkpoint: %+v, %v", c, err)
	}
	var output bytes.Buffer
	if err := listJobs(&output); err != nil || !strings.Contains(output.String(), "interrupted\t7/100 bytes") {
		t.Fatalf("listing: %q, %v", output.String(), err)
	}
	requireContents(t, destination+".part", []byte("partial"))
}

func TestListReadyJobDoesNotReconcile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	destination := filepath.Join(t.TempDir(), "file")
	partial, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockJob(path+".lock", partial)
	if err != nil {
		t.Fatal(err)
	}
	total := int64(0)
	sum := sha256.Sum256(nil)
	c := checkpoint{Version: 1, URL: "https://example.com/file", Destination: destination, Partial: partial.Name(), Total: &total, PrefixSHA256: fmt.Sprintf("%x", sum), State: "ready"}
	if err := saveCheckpoint(path, partial, c); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	lock.Close()
	var output bytes.Buffer
	if err := listJobs(&output); err != nil || !strings.Contains(output.String(), "ready-to-reconcile\t0/0 bytes") {
		t.Fatalf("listing: %q, %v", output.String(), err)
	}
	requireMissing(t, destination)
	requireContents(t, destination+".part", nil)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("listing removed checkpoint: %v", err)
	}
}

func TestListLegacyCheckpointAndMissingPartial(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	destination := filepath.Join(t.TempDir(), "file")
	partial, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	if _, err := partial.WriteString("saved"); err != nil {
		t.Fatal(err)
	}
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockJob(path+".lock", partial)
	if err != nil {
		t.Fatal(err)
	}
	total := int64(10)
	sum := sha256.Sum256([]byte("saved"))
	c := checkpoint{Version: 1, URL: "https://example.com/file", ResolvedURL: "https://example.com/file", Destination: destination, Partial: partial.Name(), ETag: `"v1"`, Total: &total, Completed: 5, PrefixSHA256: fmt.Sprintf("%x", sum)}
	if err := saveCheckpoint(path, partial, c); err != nil {
		lock.Close()
		t.Fatal(err)
	}
	lock.Close()
	var output bytes.Buffer
	if err := listJobs(&output); err != nil || !strings.Contains(output.String(), "interrupted\t5/10 bytes") {
		t.Fatalf("legacy listing: %q, %v", output.String(), err)
	}
	if err := os.Remove(partial.Name()); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := listJobs(&output); err != nil || !strings.Contains(output.String(), "needs-attention\t5/10 bytes") {
		t.Fatalf("missing partial listing: %q, %v", output.String(), err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("listing removed the legacy checkpoint: %v", err)
	}
}

func TestResumePersistsLifecycleAndUpgradesLegacyCheckpoint(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") == "bytes=0-0" {
			w.Header().Set("Content-Range", "bytes 0-0/10")
			w.WriteHeader(http.StatusPartialContent)
			w.Write([]byte("h"))
			return
		}
		w.Header().Set("Content-Range", "bytes 5-9/10")
		w.Header().Set("Content-Length", "5")
		w.WriteHeader(http.StatusPartialContent)
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "file")
	partial, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := partial.WriteString("hello"); err != nil {
		t.Fatal(err)
	}
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	lock, err := lockJob(path+".lock", partial)
	if err != nil {
		t.Fatal(err)
	}
	total := int64(10)
	sum := sha256.Sum256([]byte("hello"))
	c := checkpoint{Version: 1, URL: server.URL, ResolvedURL: server.URL, Destination: destination, Partial: partial.Name(), ETag: `"v1"`, Total: &total, Completed: 5, PrefixSHA256: fmt.Sprintf("%x", sum)}
	if err := saveCheckpoint(path, partial, c); err != nil {
		t.Fatal(err)
	}
	lock.Close()
	partial.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := resumeDownload(ctx, server.Client(), destination, "", nil)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("resumed request did not start")
	}
	deadline := time.After(5 * time.Second)
	for {
		c, err := loadCheckpoint(path)
		if err == nil && c.Version == 2 && c.State == "downloading" {
			break
		}
		select {
		case <-deadline:
			t.Fatal("resumed job did not persist downloading state")
		case err := <-done:
			t.Fatalf("resumed transfer stopped early: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("expected cancellation")
	}
	if c, err := loadCheckpoint(path); err != nil || c.Version != 2 || c.State != "interrupted" || c.Completed != 5 {
		t.Fatalf("expected interrupted version-two checkpoint, got %+v, %v", c, err)
	}
	requireContents(t, destination+".part", []byte("hello"))
}

func TestListRejectsCombinedArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-list", "-resume", "-out", "file"}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout.String(), stderr.String())
	}
}

func TestDiscoveryAfterEarlyCrash(t *testing.T) {
	if os.Getenv("FLUXGET_DISCOVERY_CHILD") == "1" {
		_, err := download(context.Background(), &http.Client{}, os.Getenv("FLUXGET_DISCOVERY_URL"), os.Getenv("FLUXGET_DISCOVERY_DEST"), "", nil)
		t.Fatalf("transfer ended before crash: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"early"`)
		w.Header().Set("Content-Length", fmt.Sprint(2<<20))
		w.Write(bytes.Repeat([]byte("x"), 4096))
		w.(http.Flusher).Flush()
		started <- struct{}{}
		<-r.Context().Done()
	}))
	defer server.Close()
	destination := filepath.Join(t.TempDir(), "file")
	child := exec.Command(os.Args[0], "-test.run=^TestDiscoveryAfterEarlyCrash$")
	child.Env = append(os.Environ(), "FLUXGET_DISCOVERY_CHILD=1", "FLUXGET_DISCOVERY_URL="+server.URL, "FLUXGET_DISCOVERY_DEST="+destination)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if child.ProcessState == nil {
			child.Process.Kill()
			child.Wait()
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("transfer did not start")
	}
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		_, checkpointErr := loadCheckpoint(path)
		info, fileErr := os.Stat(destination + ".part")
		if checkpointErr == nil && fileErr == nil && info.Size() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("early job record or partial data was not saved")
		case <-time.After(10 * time.Millisecond):
		}
	}
	var active bytes.Buffer
	if err := listJobs(&active); err != nil || !strings.Contains(active.String(), "active\t0/2097152 bytes") {
		t.Fatalf("listing while running: %q, %v", active.String(), err)
	}
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	child.Wait()
	var output bytes.Buffer
	if err := listJobs(&output); err != nil || !strings.Contains(output.String(), "interrupted (restart required)") || !strings.Contains(output.String(), "0/2097152 bytes") {
		t.Fatalf("listing after crash: %q, %v", output.String(), err)
	}
	if info, err := os.Stat(destination + ".part"); err != nil || info.Size() == 0 {
		t.Fatalf("early partial was not preserved: %v, %v", info, err)
	}
}
