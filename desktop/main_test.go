package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestDownloadUsesExistingCLI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "fluxget")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="example.txt"`)
		_, _ = w.Write([]byte("hello"))
	}))
	defer server.Close()
	dir := t.TempDir()
	var last string
	err := runDownload(context.Background(), binary, downloadArgs(server.URL+"/file", dir, ""), func(line string) { last = line })
	if err != nil {
		t.Fatalf("download: %v, last output: %s", err, last)
	}
	data, err := os.ReadFile(filepath.Join(dir, "example.txt"))
	if err != nil || string(data) != "hello" || !strings.Contains(last, "Downloaded 5 bytes") {
		t.Fatalf("downloaded data %q, output %q, error %v", data, last, err)
	}
}

func TestExistingServerFilenameIsAnnouncedWithoutOverwrite(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "fluxget")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	dir := t.TempDir()
	dest := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(dest, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="existing.txt"`)
		_, _ = w.Write([]byte("replacement"))
	}))
	defer server.Close()
	var announced string
	err := runDownload(context.Background(), binary, []string{"-url", server.URL, "-dir", dir, "-connections", "1"}, func(line string) {
		if strings.HasPrefix(line, "Saving to ") {
			announced = line
		}
	})
	data, readErr := os.ReadFile(dest)
	if err == nil || readErr != nil || string(data) != "original" || announced != "Saving to "+strconv.Quote(dest) {
		t.Fatalf("error=%v content=%q announced=%q read=%v", err, data, announced, readErr)
	}
}

func TestPausedDownloadCanResume(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "fluxget")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	payload := bytes.Repeat([]byte("hello world"), 20000)
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Disposition", `attachment; filename="file"`)
		if r.Header.Get("Range") == "" {
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
	dest := filepath.Join(t.TempDir(), "file")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	var announced string
	go func() {
		done <- runDownload(ctx, binary, []string{"-url", server.URL, "-dir", filepath.Dir(dest), "-connections", "1"}, func(line string) {
			if strings.HasPrefix(line, "Saving to ") {
				announced = line
			}
		})
	}()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("download did not start")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if info, err := os.Stat(dest + ".part"); err == nil && info.Size() >= 50000 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial data was not written")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("pause returned success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("pause did not finish")
	}
	if announced != "Saving to "+strconv.Quote(dest) {
		t.Fatalf("announced destination: %q", announced)
	}
	if err := runDownload(context.Background(), binary, []string{"-resume", "-out", dest, "-connections", "1"}, func(string) {}); err != nil {
		t.Fatalf("resume: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("resumed file differs: %v", err)
	}
}

func TestStopInterruptsCLI(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := filepath.Join(t.TempDir(), "fluxget")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write([]byte("started"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	args := append(downloadArgs(server.URL+"/file", t.TempDir(), ""), "-connections", "1")
	go func() { done <- runDownload(ctx, binary, args, func(string) {}) }()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("download did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("stopped download returned success")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("download did not stop")
	}
}
