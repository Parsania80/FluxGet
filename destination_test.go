package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

func TestAutomaticDestination(t *testing.T) {
	home := t.TempDir()
	config := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)
	if err := os.WriteFile(filepath.Join(config, "user-dirs.dirs"), []byte("XDG_DOWNLOAD_DIR=\"$HOME/My Downloads\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("test file"))
	}))
	defer server.Close()
	var out, stderr bytes.Buffer
	if code := run([]string{"-url", server.URL + "/example%20file.txt", "-connections", "1"}, &out, &stderr); code != 0 {
		t.Fatalf("download failed: %s", stderr.String())
	}
	file := filepath.Join(home, "My Downloads", "example file.txt")
	if data, err := os.ReadFile(file); err != nil || string(data) != "test file" {
		t.Fatalf("file = %q, %v", data, err)
	}
	if code := run([]string{"-url", server.URL + "/example%20file.txt", "-connections", "1"}, &out, &stderr); code == 0 {
		t.Fatal("existing file was overwritten")
	}
	customDir := t.TempDir()
	if code := run([]string{"-url", server.URL + "/other", "-dir", customDir, "-name", "chosen.txt", "-connections", "1"}, &out, &stderr); code != 0 {
		t.Fatalf("custom download failed: %s", stderr.String())
	}
	if data, err := os.ReadFile(filepath.Join(customDir, "chosen.txt")); err != nil || string(data) != "test file" {
		t.Fatalf("custom file = %q, %v", data, err)
	}
}

func TestServerFilename(t *testing.T) {
	for _, tc := range []struct {
		name, header, explicit, want string
	}{
		{"standard", `attachment; filename="report.pdf"`, "", "report.pdf"},
		{"unicode", `attachment; filename="old.txt"; filename*=UTF-8''caf%C3%A9.txt`, "", "café.txt"},
		{"unsafe", `attachment; filename="../escape.txt"`, "", "endpoint"},
		{"backslash", `attachment; filename="..\\escape.txt"`, "", "endpoint"},
		{"malformed", `attachment; filename="broken`, "", "endpoint"},
		{"override", `attachment; filename="server.txt"`, "chosen.txt", "chosen.txt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			dir := t.TempDir()
			var requests atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path == "/endpoint" {
					http.Redirect(w, r, "/final", http.StatusFound)
					return
				}
				w.Header().Set("Content-Disposition", tc.header)
				_, _ = w.Write([]byte("contents"))
			}))
			defer server.Close()
			args := []string{"-url", server.URL + "/endpoint", "-dir", dir, "-connections", "1"}
			if tc.explicit != "" {
				args = append(args, "-name", tc.explicit)
			}
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 0 {
				t.Fatalf("download failed: %s", stderr.String())
			}
			if data, err := os.ReadFile(filepath.Join(dir, tc.want)); err != nil || string(data) != "contents" {
				t.Fatalf("file %q = %q, %v", tc.want, data, err)
			}
			if requests.Load() != 2 {
				t.Fatalf("got %d requests, want redirect and download only", requests.Load())
			}
		})
	}
}

func TestSortedDownloadUsesFinalFilenameAndPreservesConflicts(t *testing.T) {
	setConfigForProxyTest(t)
	dir := t.TempDir()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="report.PDF"`)
		_, _ = w.Write([]byte("verified document"))
	}))
	defer server.Close()
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"server", nil, filepath.Join("Documents", "report.PDF")},
		{"explicit", []string{"-name", "chosen.mp3"}, filepath.Join("Music", "chosen.mp3")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"-url", server.URL + "/fallback.zip", "-dir", dir, "-sort-files", "-connections", "1"}, tc.args...)
			var out, stderr bytes.Buffer
			if code := run(args, &out, &stderr); code != 0 {
				t.Fatalf("sorted download failed: %s", stderr.String())
			}
			path := filepath.Join(dir, tc.want)
			if data, err := os.ReadFile(path); err != nil || string(data) != "verified document" {
				t.Fatalf("wrong output: %q, %v", data, err)
			}
			if code := run(args, &out, &stderr); code == 0 {
				t.Fatal("existing categorized file overwritten")
			}
			if data, err := os.ReadFile(path); err != nil || string(data) != "verified document" {
				t.Fatal("existing output changed", err)
			}
		})
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"-url", server.URL, "-out", filepath.Join(dir, "exact.pdf"), "-sort-files"}, &out, &stderr); code != 2 {
		t.Fatal("sorting accepted an exact output path")
	}
}

func TestSortedQueueRequiresPendingAutomaticCategory(t *testing.T) {
	root := t.TempDir()
	for _, job := range []queueJob{
		{Destination: filepath.Join(root, "clip.mp3"), AutomaticName: true},
		{Destination: filepath.Join(root, "Music", "clip.pdf"), AutomaticName: true},
		{Destination: filepath.Join(root, "Music", "clip.mp3"), AutomaticName: false},
	} {
		job.SortFiles, job.ID, job.URL, job.State = true, 1, "https://example.com/file", "paused"
		q := queueData{Version: 2, NextID: 2}
		if _, err := desktopAdd(&q, job, false); err == nil || !strings.Contains(err.Error(), "category") {
			t.Fatalf("invalid sorted intake accepted: %+v, %v", job, err)
		}
		q.Jobs = []queueJob{job}
		data, err := json.Marshal(q)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(root, "queue.json")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadQueue(path); err == nil || !strings.Contains(err.Error(), "category") {
			t.Fatalf("invalid persisted category accepted: %+v, %v", job, err)
		}
	}
}

func TestServerFilenameRetryAndExistingFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := t.TempDir()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Disposition", `attachment; filename="server.txt"`)
		_, _ = w.Write([]byte("contents"))
	}))
	defer server.Close()
	args := []string{"-url", server.URL + "/endpoint", "-dir", dir, "-connections", "1"}
	var out, stderr bytes.Buffer
	if code := run(args, &out, &stderr); code != 0 {
		t.Fatalf("download after retry failed: %s", stderr.String())
	}
	if code := run(args, &out, &stderr); code == 0 {
		t.Fatal("existing server-named file was overwritten")
	}
	if data, err := os.ReadFile(filepath.Join(dir, "server.txt")); err != nil || string(data) != "contents" {
		t.Fatalf("existing file = %q, %v", data, err)
	}
}

func TestDestinationOverridesAndUnsafeName(t *testing.T) {
	dir := t.TempDir()
	got, err := defaultDestination("https://example.com/file?token=ignored", dir, "chosen.txt", false)
	if err != nil || got != filepath.Join(dir, "chosen.txt") {
		t.Fatalf("destination = %q, %v", got, err)
	}
	got, err = defaultDestination("https://example.com/", dir, "", false)
	if err != nil || got != filepath.Join(dir, "download") {
		t.Fatalf("fallback = %q, %v", got, err)
	}
	for _, name := range []string{"../file", ".", "..", "a\\b", "bad\nname"} {
		if _, err := defaultDestination("https://example.com/file", dir, name, false); err == nil {
			t.Errorf("accepted unsafe name %q", name)
		}
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"-url", "https://example.com/file", "-out", "file", "-dir", dir}, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("conflicting flags: %d, %s", code, stderr.String())
	}
}

func TestSuggestOutputUsesURLWithoutDownloading(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out, stderr bytes.Buffer
	if code := run([]string{"-suggest-output", "-url", "https://example.com/a%20file.zip?secret=ignored"}, &out, &stderr); code != 0 {
		t.Fatalf("suggest output: %s", stderr.String())
	}
	want := strconv.Quote(filepath.Join(home, "Downloads", "a file.zip")) + "\n"
	if out.String() != want {
		t.Fatalf("got %q, want %q", out.String(), want)
	}
	if code := run([]string{"-suggest-output", "-queue"}, &out, &stderr); code != 2 {
		t.Fatalf("invalid combination returned %d", code)
	}
}
