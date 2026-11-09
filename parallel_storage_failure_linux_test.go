//go:build linux

package main

import (
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
	"syscall"
	"testing"
	"time"
)

func rangeFileServer(payload []byte) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
}

func fillFilesystem(t *testing.T, dir string) func() {
	t.Helper()
	f, err := os.CreateTemp(dir, "filler-*")
	if err != nil {
		t.Fatal(err)
	}
	for {
		_, err = f.Write(make([]byte, 32<<10))
		if err != nil {
			if !errors.Is(err, syscall.ENOSPC) {
				t.Fatal(err)
			}
			break
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Remove(f.Name()); err != nil {
			t.Fatal(err)
		}
	}
}

func assertSavedChunks(t *testing.T, path, dest string, payload []byte) checkpoint {
	t.Helper()
	c, err := loadCheckpoint(path)
	if err != nil {
		t.Fatalf("load last checkpoint: %v", err)
	}
	if !c.parallel() {
		t.Fatalf("checkpoint version: %d", c.Version)
	}
	partial, err := os.ReadFile(dest + ".part")
	if err != nil {
		t.Fatal(err)
	}
	for _, chunk := range c.Chunks {
		if chunk.savedLength() == 0 {
			continue
		}
		end := chunk.Start + chunk.savedLength()
		if end > int64(len(partial)) || !bytes.Equal(partial[chunk.Start:end], payload[chunk.Start:end]) {
			t.Fatal("checkpoint claims invalid bytes")
		}
		sum := sha256.Sum256(partial[chunk.Start : chunk.Start+chunk.savedLength()])
		if fmt.Sprintf("%x", sum) != chunk.savedDigest() {
			t.Fatal("saved chunk hash differs")
		}
	}
	requireMissing(t, dest)
	return c
}

func TestParallelStorageENOSPC(t *testing.T) {
	if os.Getenv("FLUXGET_PARALLEL_DISK_CHILD") == "1" {
		testParallelDiskFull(t, os.Getenv("FLUXGET_PARALLEL_DISK_DIR"))
		return
	}
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skip("unshare is unavailable")
	}
	if err := exec.Command("unshare", "--user", "--map-root-user", "--mount", "true").Run(); err != nil {
		t.Skipf("private namespaces unavailable: %v", err)
	}
	dir := t.TempDir()
	script := `mount --make-rprivate / && mount -t tmpfs -o size=1m tmpfs "$1" && exec "$2" -test.run=^TestParallelStorageENOSPC$ -test.v`
	cmd := exec.Command("unshare", "--user", "--map-root-user", "--mount", "sh", "-c", script, "fluxget-test", dir, os.Args[0])
	cmd.Env = append(os.Environ(), "FLUXGET_PARALLEL_DISK_CHILD=1", "FLUXGET_PARALLEL_DISK_DIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("isolated parallel ENOSPC test failed: %v\n%s", err, output)
	} else {
		t.Logf("isolated filesystem results:\n%s", output)
	}
}

func testParallelDiskFull(t *testing.T, dir string) {
	payload := bytes.Repeat([]byte("disk-full"), minParallelSize/9+1)[:minParallelSize]
	server := rangeFileServer(payload)
	defer server.Close()
	t.Run("fresh data", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		dest := filepath.Join(dir, "fresh")
		filler, err := os.Create(filepath.Join(dir, "data-filler"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := filler.Write(make([]byte, 512<<10)); err != nil {
			t.Fatal(err)
		}
		filler.Close()
		_, err = downloadFresh(context.Background(), server.Client(), server.URL, dest, "", io.Discard, 4)
		if !errors.Is(err, syscall.ENOSPC) {
			t.Fatalf("expected ENOSPC: %v", err)
		}
		cp, err := checkpointLocation(dest)
		if err != nil {
			t.Fatal(err)
		}
		assertSavedChunks(t, cp, dest, payload)
		if err := os.Remove(filler.Name()); err != nil {
			t.Fatal(err)
		}
		if _, err := resumeDownload(context.Background(), server.Client(), dest, "", io.Discard); err != nil {
			t.Fatalf("resume: %v", err)
		}
		requireContents(t, dest, payload)
		if err := os.Remove(dest); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("recovery data", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", t.TempDir())
		dest := filepath.Join(dir, "recovery")
		cp, err := checkpointLocation(dest)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(dest+".part", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		half := int64(len(payload) / 2)
		if _, err := f.WriteAt(payload[half:], half); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(payload[half:])
		total := int64(len(payload))
		c := checkpoint{Version: 3, URL: server.URL, ResolvedURL: server.URL, Destination: dest, Partial: dest + ".part", ETag: `"v1"`, State: "interrupted", Total: &total, Completed: half, Chunks: []savedChunk{{Start: 0, End: half}, {Start: half, End: total, SHA256: fmt.Sprintf("%x", sum)}}}
		if err := saveCheckpoint(cp, f, c); err != nil {
			t.Fatal(err)
		}
		f.Close()
		filler, err := os.Create(filepath.Join(dir, "recovery-filler"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := filler.Write(make([]byte, 256<<10)); err != nil {
			t.Fatal(err)
		}
		filler.Close()
		_, err = resumeDownload(context.Background(), server.Client(), dest, "", io.Discard)
		if !errors.Is(err, syscall.ENOSPC) {
			t.Fatalf("expected ENOSPC: %v", err)
		}
		after := assertSavedChunks(t, cp, dest, payload)
		if after.Chunks[1].SHA256 == "" {
			t.Fatal("previous chunk was lost")
		}
		if err := os.Remove(filler.Name()); err != nil {
			t.Fatal(err)
		}
		if _, err := resumeDownload(context.Background(), server.Client(), dest, "", io.Discard); err != nil {
			t.Fatal(err)
		}
		requireContents(t, dest, payload)
		if err := os.Remove(dest); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("fresh metadata", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "fresh-config"))
		testParallelCheckpointFailure(t, false, func(jobDir string) func() { return fillFilesystem(t, jobDir) }, syscall.ENOSPC)
	})
	t.Run("recovery metadata", func(t *testing.T) {
		t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "recovery-config"))
		testParallelCheckpointFailure(t, true, func(jobDir string) func() { return fillFilesystem(t, jobDir) }, syscall.ENOSPC)
	})
}

// Block remaining HTTP ranges while the checkpoint filesystem is made unwritable.
func testParallelCheckpointFailure(t *testing.T, recovery bool, makeUnwritable func(string) func(), want error) {
	t.Helper()
	payload := bytes.Repeat([]byte("checkpoint"), minParallelSize/10+1)[:minParallelSize]
	step := len(payload) / 4
	release := make(chan struct{})
	blocked := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		rr := r.Header.Get("Range")
		if rr != "bytes=0-0" && (recovery || rr != fmt.Sprintf("bytes=0-%d", step-1)) {
			select {
			case blocked <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
		server.Close()
	}()
	var dest, path string
	if recovery {
		dest, path, _ = chunkFixture(t, server.URL, payload, false)
	} else {
		dest = filepath.Join(t.TempDir(), "file")
		var err error
		path, err = checkpointLocation(dest)
		if err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan error, 1)
	go func() {
		var err error
		if recovery {
			_, err = resumeDownload(context.Background(), server.Client(), dest, "", io.Discard)
		} else {
			_, err = downloadFresh(context.Background(), server.Client(), server.URL, dest, "", io.Discard, 4)
		}
		done <- err
	}()
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("range did not block")
	}
	waitQueue(t, func() bool {
		c, err := loadCheckpoint(path)
		if err != nil {
			return false
		}
		if recovery {
			return c.State == "downloading"
		}
		return len(c.Chunks) == 4 && c.Chunks[0].SHA256 != ""
	})
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	restore := makeUnwritable(filepath.Dir(path))
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("expected %v, got %v", want, err)
		}
		if want == syscall.ENOSPC && !strings.Contains(err.Error(), "write checkpoint") {
			t.Fatalf("expected checkpoint write failure, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("transfer did not stop on checkpoint failure")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("last checkpoint changed: %v", err)
	}
	assertSavedChunks(t, path, dest, payload)
	temps, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".fluxget-checkpoint-*"))
	if err != nil || len(temps) != 0 {
		t.Fatalf("temporary files remain: %v %v", temps, err)
	}
	restore()
	if _, err := resumeDownload(context.Background(), server.Client(), dest, "", io.Discard); err != nil {
		t.Fatalf("resume after storage restored: %v", err)
	}
	requireContents(t, dest, payload)
}

func TestParallelCheckpointPermissions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may bypass filesystem permissions")
	}
	for _, recovery := range []bool{false, true} {
		name := "fresh"
		if recovery {
			name = "recovery"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			testParallelCheckpointFailure(t, recovery, func(dir string) func() {
				if err := os.Chmod(dir, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.Chmod(dir, 0700) })
				return func() {
					if err := os.Chmod(dir, 0700); err != nil {
						t.Fatal(err)
					}
				}
			}, os.ErrPermission)
		})
	}
}

func TestParallelDestinationAndRecoveryPermissions(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may bypass filesystem permissions")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("p"), minParallelSize)
	server := rangeFileServer(payload)
	defer server.Close()
	t.Run("new output", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.Chmod(dir, 0500); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(dir, 0700)
		dest := filepath.Join(dir, "file")
		_, err := downloadFresh(context.Background(), server.Client(), server.URL, dest, "", io.Discard, 4)
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("expected permission error: %v", err)
		}
		requireMissing(t, dest)
		requireMissing(t, dest+".part")
	})
	t.Run("saved partial", func(t *testing.T) {
		dest, path, _ := chunkFixture(t, server.URL, payload, false)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dest+".part", 0400); err != nil {
			t.Fatal(err)
		}
		_, err = resumeDownload(context.Background(), server.Client(), dest, "", io.Discard)
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("expected permission error: %v", err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("checkpoint changed: %v", err)
		}
		requireMissing(t, dest)
		if err := os.Chmod(dest+".part", 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := resumeDownload(context.Background(), server.Client(), dest, "", io.Discard); err != nil {
			t.Fatal(err)
		}
		requireContents(t, dest, payload)
	})
}

func TestQueueParallelCheckpointFailureCanRetry(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may bypass filesystem permissions")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("q"), minParallelSize)
	step := len(payload) / 4
	release := make(chan struct{})
	blocked := make(chan struct{}, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		rr := r.Header.Get("Range")
		if rr != "bytes=0-0" && rr != fmt.Sprintf("bytes=0-%d", step-1) {
			select {
			case blocked <- struct{}{}:
			default:
			}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	input, commands := io.Pipe()
	defer commands.Close()
	out := &queueOutput{}
	done := make(chan error, 1)
	go func() { done <- runQueue(context.Background(), input, out, 1, 4) }()
	dest := filepath.Join(t.TempDir(), "file")
	path, err := checkpointLocation(dest)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Fprintf(commands, "add %s %s\nstart\n", server.URL, dest)
	select {
	case <-blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("range did not block")
	}
	waitQueue(t, func() bool {
		c, e := loadCheckpoint(path)
		return e == nil && len(c.Chunks) == 4 && c.Chunks[0].SHA256 != ""
	})
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0700) })
	close(release)
	waitQueue(t, func() bool { return out.contains("Job 1 failed:") })
	assertSavedChunks(t, path, dest, payload)
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	fmt.Fprintln(commands, "retry 1")
	waitQueue(t, func() bool { return out.contains("Job 1: completed") })
	fmt.Fprintln(commands, "quit")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
}
