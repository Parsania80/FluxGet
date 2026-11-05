//go:build linux

package main

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAcceptanceBuiltCLIDownload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := []byte("FluxGet built CLI acceptance fixture\x00\xff")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(payload) }))
	defer server.Close()
	dir := t.TempDir()
	binary := filepath.Join(dir, "fluxget")
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	dest := filepath.Join(dir, "file")
	sum := sha256.Sum256(payload)
	cmd := exec.Command(binary, "-url", server.URL, "-out", dest, "-connections", "1", "-sha256", fmt.Sprintf("%x", sum))
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("download: %v\n%s", err, output)
	}
	got, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("downloaded data %q: %v", got, err)
	}
	requireMissing(t, dest+".part")
}
