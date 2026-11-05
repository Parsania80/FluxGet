package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
	got, err := defaultDestination("https://example.com/file?token=ignored", dir, "chosen.txt")
	if err != nil || got != filepath.Join(dir, "chosen.txt") {
		t.Fatalf("destination = %q, %v", got, err)
	}
	got, err = defaultDestination("https://example.com/", dir, "")
	if err != nil || got != filepath.Join(dir, "download") {
		t.Fatalf("fallback = %q, %v", got, err)
	}
	for _, name := range []string{"../file", ".", "..", "a\\b", "bad\nname"} {
		if _, err := defaultDestination("https://example.com/file", dir, name); err == nil {
			t.Errorf("accepted unsafe name %q", name)
		}
	}
	var out, stderr bytes.Buffer
	if code := run([]string{"-url", "https://example.com/file", "-out", "file", "-dir", dir}, &out, &stderr); code != 2 || !strings.Contains(stderr.String(), "cannot be combined") {
		t.Fatalf("conflicting flags: %d, %s", code, stderr.String())
	}
}
