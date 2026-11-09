//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestParallelChunkCrashRecovery(t *testing.T) {
	if os.Getenv("FLUXGET_CHUNK_CHILD") == "1" {
		client := defaultDownloadClient()
		defer client.CloseIdleConnections()
		_, err := downloadFresh(context.Background(), client, os.Getenv("FLUXGET_CHUNK_URL"), os.Getenv("FLUXGET_CHUNK_DEST"), "", io.Discard, 4)
		t.Fatalf("child exited before crash: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("chunked!"), 200000)
	step := int64(len(payload)) / 4
	var recovery atomic.Bool
	var mu sync.Mutex
	var ranges []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		rr := r.Header.Get("Range")
		if recovery.Load() {
			mu.Lock()
			ranges = append(ranges, rr)
			mu.Unlock()
		} else if rr != "bytes=0-0" && rr != fmt.Sprintf("bytes=%d-%d", step, 2*step-1) {
			<-r.Context().Done()
			return
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "file")
	path, err := checkpointLocation(dest)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestParallelChunkCrashRecovery$")
	cmd.Env = append(os.Environ(), "FLUXGET_CHUNK_CHILD=1", "FLUXGET_CHUNK_URL="+server.URL, "FLUXGET_CHUNK_DEST="+dest)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			cmd.Process.Kill()
			cmd.Wait()
		}
	}()
	waitQueue(t, func() bool {
		c, e := loadCheckpoint(path)
		return e == nil && len(c.Chunks) == 4 && c.Chunks[1].SHA256 != ""
	})
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("child was not killed")
	}
	saved, err := loadCheckpoint(path)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version != 4 || saved.Chunks[0].SHA256 != "" || saved.Chunks[1].SHA256 == "" {
		t.Fatalf("unexpected checkpoint %+v; child stderr %s", saved, stderr.String())
	}
	recovery.Store(true)
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	if _, err := resumeDownload(context.Background(), client, dest, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
	requireMissing(t, path)
	requireMissing(t, dest+".part")
	mu.Lock()
	defer mu.Unlock()
	savedRange := fmt.Sprintf("bytes=%d-%d", step, 2*step-1)
	for _, r := range ranges {
		if r == savedRange {
			t.Fatalf("already saved chunk requested again: %v", ranges)
		}
	}
}

func chunkFixture(t *testing.T, serverURL string, payload []byte, all bool) (string, string, checkpoint) {
	t.Helper()
	dest := filepath.Join(t.TempDir(), "file")
	path, err := checkpointLocation(dest)
	if err != nil {
		t.Fatal(err)
	}
	partial, err := os.OpenFile(dest+".part", os.O_CREATE|os.O_RDWR|os.O_EXCL, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer partial.Close()
	if _, err := partial.Write(payload); err != nil {
		t.Fatal(err)
	}
	total := int64(len(payload))
	split := total / 2
	c := checkpoint{Version: 3, URL: serverURL, ResolvedURL: serverURL, Destination: dest, Partial: dest + ".part", ETag: `"v1"`, Total: &total, State: "interrupted", Chunks: []savedChunk{{Start: 0, End: split}, {Start: split, End: total}}}
	for i := range c.Chunks {
		if i == 0 && !all {
			continue
		}
		sum := sha256.Sum256(payload[c.Chunks[i].Start:c.Chunks[i].End])
		c.Chunks[i].SHA256 = fmt.Sprintf("%x", sum)
		c.Completed += c.Chunks[i].End - c.Chunks[i].Start
	}
	if err := saveCheckpoint(path, partial, c); err != nil {
		t.Fatal(err)
	}
	return dest, path, c
}

func TestChunkRecoveryRejectsChangedDataBeforeNetwork(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("z"), minParallelSize)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1) }))
	defer server.Close()
	dest, path, c := chunkFixture(t, server.URL, payload, false)
	f, err := os.OpenFile(dest+".part", os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("!"), c.Chunks[1].Start); err != nil {
		t.Fatal(err)
	}
	f.Close()
	_, err = resumeDownload(context.Background(), server.Client(), dest, "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "saved chunk") {
		t.Fatalf("error %v", err)
	}
	if requests.Load() != 0 {
		t.Fatal("contacted server before checking local data")
	}
	requireMissing(t, dest)
	if _, err := loadCheckpoint(path); err != nil {
		t.Fatal(err)
	}
}

func TestChunkRecoveryRejectsChangedRemote(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("z"), minParallelSize)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v2"`)
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	dest, path, _ := chunkFixture(t, server.URL, payload, false)
	_, err := resumeDownload(context.Background(), server.Client(), dest, "", io.Discard)
	if err == nil || !strings.Contains(err.Error(), "remote file") {
		t.Fatalf("error %v", err)
	}
	requireMissing(t, dest)
	if _, err := loadCheckpoint(path); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteChunksRecoverWithoutNetwork(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("z"), 1024)
	dest, path, _ := chunkFixture(t, "http://127.0.0.1:1/file", payload, true)
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	if _, err := resumeDownload(context.Background(), client, dest, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
	requireMissing(t, path)
}

func TestChunkCheckpointRejectsInvalidMetadata(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _, base := chunkFixture(t, "http://example.com/file", bytes.Repeat([]byte("x"), 1024), false)
	cases := map[string]func(*checkpoint){
		"gap":               func(c *checkpoint) { c.Chunks[1].Start++ },
		"overlap":           func(c *checkpoint) { c.Chunks[1].Start-- },
		"outside file":      func(c *checkpoint) { c.Chunks[1].End++ },
		"bad hash":          func(c *checkpoint) { c.Chunks[1].SHA256 = "not a digest" },
		"incorrect count":   func(c *checkpoint) { c.Completed++ },
		"missing validator": func(c *checkpoint) { c.ETag = `W/"weak"` },
		"legacy chunks":     func(c *checkpoint) { c.Version = 2 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			c := base
			c.Chunks = append([]savedChunk(nil), base.Chunks...)
			mutate(&c)
			if err := c.validate(); err == nil {
				t.Fatal("invalid checkpoint accepted")
			}
		})
	}
}

func TestChunkCheckpointCrashAtomic(t *testing.T) {
	if stage := os.Getenv("FLUXGET_CRASH_STAGE"); stage != "" {
		dest := filepath.Join(os.Getenv("FLUXGET_CRASH_DIR"), "file")
		path, err := checkpointLocation(dest)
		if err != nil {
			t.Fatal(err)
		}
		c, err := loadCheckpoint(path)
		if err != nil {
			t.Fatal(err)
		}
		partial, err := os.OpenFile(dest+".part", os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer partial.Close()
		lock, err := lockJob(path+".lock", partial)
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		digest, err := chunkDigest(partial, c.Chunks[0])
		if err != nil {
			t.Fatal(err)
		}
		c.Chunks[0].SHA256 = digest
		c.Completed += c.Chunks[0].End
		hook := func(current string) {
			if current == stage {
				fmt.Fprintln(os.Stdout, "ready")
				syscall.Kill(os.Getpid(), syscall.SIGSTOP)
			}
		}
		if err := saveCheckpointWithHook(path, partial, c, hook); err != nil {
			t.Fatal(err)
		}
		t.Fatal("crash stage not reached")
	}
	for _, stage := range []string{"checkpoint-written", "checkpoint-synced", "checkpoint-published"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			payload := bytes.Repeat([]byte("a"), minParallelSize)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"v1"`)
				http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			}))
			defer server.Close()
			dest, path, _ := chunkFixture(t, server.URL, payload, false)
			killAtStage(t, "TestChunkCheckpointCrashAtomic", stage, filepath.Dir(dest))
			c, err := loadCheckpoint(path)
			if err != nil {
				t.Fatal(err)
			}
			if (c.Chunks[0].SHA256 != "") != (stage == "checkpoint-published") {
				t.Fatalf("wrong checkpoint after %s: %+v", stage, c)
			}
			client := defaultDownloadClient()
			defer client.CloseIdleConnections()
			if _, err := resumeDownload(context.Background(), client, dest, "", io.Discard); err != nil {
				t.Fatal(err)
			}
			requireContents(t, dest, payload)
		})
	}
}

func TestChunkResumeHonorsConnectionLimit(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("x"), minParallelSize)
	var active, maxActive atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if r.Header.Get("Range") != "bytes=0-0" {
			n := active.Add(1)
			for {
				old := maxActive.Load()
				if n <= old || maxActive.CompareAndSwap(old, n) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			defer active.Add(-1)
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	dest, path, c := chunkFixture(t, server.URL, payload, false)
	size := int64(len(payload)) / 4
	c.Chunks = nil
	c.Completed = 0
	for i := int64(0); i < 4; i++ {
		chunk := savedChunk{Start: i * size, End: (i + 1) * size}
		if i == 0 {
			sum := sha256.Sum256(payload[:size])
			chunk.SHA256 = fmt.Sprintf("%x", sum)
			c.Completed = size
		}
		c.Chunks = append(c.Chunks, chunk)
	}
	partial, err := os.OpenFile(dest+".part", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := saveCheckpoint(path, partial, c); err != nil {
		t.Fatal(err)
	}
	partial.Close()
	if _, err := resumeDownloadWithLimit(context.Background(), server.Client(), dest, "", io.Discard, 1); err != nil {
		t.Fatal(err)
	}
	if maxActive.Load() != 1 {
		t.Fatalf("maximum active ranges: %d", maxActive.Load())
	}
	requireContents(t, dest, payload)
}
