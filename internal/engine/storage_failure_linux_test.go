//go:build linux

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestDiskFullInPrivateFilesystem(t *testing.T) {
	if os.Getenv("FLUXGET_FULL_DISK_CHILD") == "1" {
		testDiskFull(t, os.Getenv("FLUXGET_FULL_DISK_DIR"))
		return
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("unshare is unavailable")
	}
	if err := exec.Command("unshare", "--user", "--map-root-user", "--mount", "true").Run(); err != nil {
		t.Skipf("private user and mount namespaces are unavailable: %v", err)
	}
	dir := t.TempDir()
	script := `mount --make-rprivate / && mount -t tmpfs -o size=1m tmpfs "$1" && exec "$2" -test.run=^TestDiskFullInPrivateFilesystem$ -test.v`
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--mount", "sh", "-c", script, "fluxget-test", dir, os.Args[0])
	cmd.Env = append(os.Environ(), "FLUXGET_FULL_DISK_CHILD=1", "FLUXGET_FULL_DISK_DIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated disk-full test failed: %v\n%s", err, output)
	}
}

func testDiskFull(t *testing.T, dir string) {
	payload := bytes.Repeat([]byte("a"), 2<<20)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload)
	}))
	defer server.Close()
	destination := filepath.Join(dir, "download")
	written, err := download(context.Background(), server.Client(), server.URL, destination, "", nil)
	if !errors.Is(err, syscall.ENOSPC) || written <= 0 || written >= int64(len(payload)) {
		t.Fatalf("download = %d, %v; want partial transfer and ENOSPC", written, err)
	}
	requireMissing(t, destination)
	partial, err := os.ReadFile(destination + ".part")
	if err != nil || int64(len(partial)) != written || !bytes.Equal(partial, payload[:written]) {
		t.Fatalf("incorrect retained partial: %d bytes, %v", len(partial), err)
	}
	if err := os.Remove(destination + ".part"); err != nil {
		t.Fatal(err)
	}

	partialFile, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	defer partialFile.Close()
	if _, err := partialFile.WriteString("saved data"); err != nil {
		t.Fatal(err)
	}
	c := checkpoint{Version: 1, URL: server.URL, Destination: destination, Partial: partialFile.Name(), Completed: 4}
	path := filepath.Join(dir, "job.json")
	if err := saveCheckpoint(path, partialFile, c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	filler, err := os.Create(filepath.Join(dir, "filler"))
	if err != nil {
		t.Fatal(err)
	}
	block := make([]byte, 32<<10)
	for {
		if _, err := filler.Write(block); err != nil {
			if !errors.Is(err, syscall.ENOSPC) {
				t.Fatal(err)
			}
			break
		}
	}
	if err := filler.Close(); err != nil {
		t.Fatal(err)
	}
	c.Completed = 9
	if err := saveCheckpoint(path, partialFile, c); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("checkpoint update = %v, want ENOSPC", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("previous checkpoint changed: %v", err)
	}
	requireContents(t, partialFile.Name(), []byte("saved data"))
	temps, err := filepath.Glob(filepath.Join(dir, ".fluxget-checkpoint-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("temporary checkpoint files remain: %v, %v", temps, err)
	}
}

func TestDownloadDestinationPermissionFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may bypass filesystem permissions")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("data"))
	}))
	defer server.Close()
	destination := filepath.Join(dir, "file")
	written, err := download(context.Background(), server.Client(), server.URL, destination, "", nil)
	if written != 0 || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("download = %d, %v; want permission error", written, err)
	}
	requireMissing(t, destination)
	requireMissing(t, destination+".part")
}

func TestCheckpointPermissionFailurePreservesPrevious(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may bypass filesystem permissions")
	}
	dir := t.TempDir()
	destination := filepath.Join(dir, "file")
	partial, err := os.Create(destination + ".part")
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	if _, err := partial.WriteString("saved data"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "job.json")
	c := checkpoint{Version: 1, URL: "https://example.com/file", Destination: destination, Partial: partial.Name(), Completed: 4}
	if err := saveCheckpoint(path, partial, c); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	c.Completed = 9
	if err := saveCheckpoint(path, partial, c); !errors.Is(err, os.ErrPermission) {
		t.Fatalf("checkpoint update = %v; want permission error", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("previous checkpoint changed: %v", err)
	}
	requireContents(t, partial.Name(), []byte("saved data"))
}

func TestTransferStopsWhenCheckpointUpdateFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may bypass filesystem permissions")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("x"), (2<<20)+131072)
	continueTransfer := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"checkpoint-permission"`)
		if r.Header.Get("Range") != "" {
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		w.Write(payload[:(1<<20)+65536])
		w.(http.Flusher).Flush()
		select {
		case <-continueTransfer:
			w.Write(payload[(1<<20)+65536:])
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	destination := filepath.Join(t.TempDir(), "file")
	done := make(chan error, 1)
	go func() {
		_, err := download(ctx, server.Client(), server.URL, destination, "", nil)
		done <- err
	}()
	path, err := checkpointLocation(destination)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	var before []byte
	for {
		before, err = os.ReadFile(path)
		if err == nil {
			var saved checkpoint
			if err := json.Unmarshal(before, &saved); err != nil {
				t.Fatal(err)
			}
			// The initial checkpoint has no reusable bytes. Inject the failure
			// only after a nonempty prefix has actually been saved.
			if saved.Completed >= 1<<20 {
				break
			}
		}
		select {
		case <-deadline:
			t.Fatal("first checkpoint was not saved")
		case err := <-done:
			t.Fatalf("transfer stopped too early: %v", err)
		case <-time.After(10 * time.Millisecond):
		}
	}
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	close(continueTransfer)
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("transfer error = %v; want checkpoint permission failure", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("transfer did not stop after checkpoint update failed")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("last valid checkpoint changed: %v", err)
	}
	requireMissing(t, destination)
	if info, err := os.Stat(destination + ".part"); err != nil || info.Size() < 1<<20 {
		t.Fatalf("partial file was not preserved: %v, %v", info, err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := resumeDownload(context.Background(), server.Client(), destination, "", nil); err != nil {
		t.Fatalf("resume after restoring permissions: %v", err)
	}
	requireContents(t, destination, payload)
}
