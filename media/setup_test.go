package media

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMediaToolSetupVerifiesAndPublishesTogether(t *testing.T) {
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	archives := map[string][]byte{}
	for tool, names := range map[string][]string{"yt_dlp": {"yt-dlp"}, "deno": {"deno"}, "ffmpeg": {"ffmpeg", "ffprobe"}} {
		var buffer bytes.Buffer
		z := zip.NewWriter(&buffer)
		for _, name := range names {
			w, err := z.Create("nested/bin/" + name + suffix)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("fixture executable " + name)); err != nil {
				t.Fatal(err)
			}
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		archives["/"+tool] = buffer.Bytes()
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(archives[r.URL.Path]) }))
	defer server.Close()
	artifacts := map[string]toolArtifact{}
	for tool := range map[string]bool{"yt_dlp": true, "deno": true, "ffmpeg": true} {
		digest := sha256.Sum256(archives["/"+tool])
		artifacts[tool] = toolArtifact{server.URL + "/" + tool, hex.EncodeToString(digest[:]), tool + ".zip"}
	}
	destination := filepath.Join(t.TempDir(), "installed")
	bad := artifacts["deno"]
	valid := bad
	bad.SHA256 = strings.Repeat("0", 64)
	artifacts["deno"] = bad
	if _, err := installTools(t.Context(), destination, artifacts, nil, server.Client()); err == nil {
		t.Fatal("checksum mismatch accepted")
	}
	if _, err := os.Stat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed setup published a partial tool set", err)
	}
	entries, err := os.ReadDir(filepath.Dir(destination))
	if err != nil || len(entries) != 0 {
		t.Fatal("failed setup retained staging data", err)
	}
	artifacts["deno"] = valid
	manifest, err := installTools(t.Context(), destination, artifacts, map[string]string{"test": "1"}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	tools, err := LoadTools(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(tools); err != nil {
		t.Fatal(err)
	}
	for name, path := range map[string]string{"yt-dlp": tools.YTDLP, "deno": tools.Deno, "ffmpeg": tools.FFmpeg, "ffprobe": tools.FFprobe} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != "fixture executable "+name || filepath.Dir(path) != destination {
			t.Fatal("wrong published tool", name, err)
		}
	}
	before, _ := os.ReadFile(manifest)
	if repeated, err := installTools(t.Context(), destination, nil, nil, server.Client()); err != nil || repeated != manifest {
		t.Fatal("existing tools were not reused", err)
	}
	after, _ := os.ReadFile(manifest)
	if !bytes.Equal(before, after) {
		t.Fatal("setup overwrote existing manifest")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := installTools(ctx, filepath.Join(t.TempDir(), "canceled"), artifacts, nil, server.Client()); err == nil {
		t.Fatal("canceled setup succeeded")
	}
}

func TestMediaToolLockAndDefaultLocation(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LOCALAPPDATA", cache)
	manifest, err := DefaultToolsManifest()
	if err != nil || !filepath.IsAbs(manifest) || !strings.HasPrefix(manifest, cache+string(filepath.Separator)) {
		t.Fatal("default tool cache is not isolated", err)
	}
	if _, err := os.Stat(filepath.Dir(manifest)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("default lookup created files", err)
	}
	var lock toolLock
	if err := json.Unmarshal(toolsLock, &lock); err != nil {
		t.Fatal(err)
	}
	for _, platform := range []string{"linux-amd64", "windows-amd64"} {
		if len(lock.Platforms[platform]) != 3 {
			t.Fatal("incomplete platform lock", platform)
		}
		for _, entry := range lock.Platforms[platform] {
			digest, err := hex.DecodeString(entry.SHA256)
			if err != nil || len(digest) != 32 || !strings.HasPrefix(entry.URL, "https://github.com/") {
				t.Fatal("unlocked upstream artifact", entry.Name)
			}
		}
	}
}

func TestMediaToolSetupCancellationCleansPartialDownload(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("partial download"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	root := t.TempDir()
	destination := filepath.Join(root, "installed")
	artifacts := map[string]toolArtifact{"yt_dlp": {server.URL, strings.Repeat("0", 64), "yt-dlp"}}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := installTools(ctx, destination, artifacts, nil, server.Client()); done <- err }()
	<-started
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled download succeeded")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatal("canceled download left staging data", err)
	}
}

func TestMediaToolArchiveRejectsDuplicatesAndMissingExecutables(t *testing.T) {
	for _, names := range [][]string{{"other"}, {"a/deno", "b/deno"}} {
		var buffer bytes.Buffer
		z := zip.NewWriter(&buffer)
		for _, name := range names {
			w, err := z.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = w.Write([]byte("fixture"))
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		root := t.TempDir()
		archive := filepath.Join(root, "bundle.zip")
		if err := os.WriteFile(archive, buffer.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if err := unpackTool(t.Context(), archive, "bundle.zip", "deno", filepath.Join(root, "deno")); err == nil {
			t.Fatal("ambiguous/missing archive member accepted")
		}
	}
}
