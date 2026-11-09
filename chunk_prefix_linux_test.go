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
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelPrefixPauseResumeTwice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("prefix-data!"), 200000)
	step := int64(len(payload)) / 4
	const portion = int64(65536)
	var phase atomic.Int32
	var mu sync.Mutex
	requests := make(map[int][]int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || end == 0 {
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		round := int(phase.Load())
		mu.Lock()
		requests[round] = append(requests[round], start)
		mu.Unlock()
		if round >= 2 {
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[start : start+portion])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "file")
	cp, err := checkpointLocation(dest)
	if err != nil {
		t.Fatal(err)
	}
	client := server.Client()
	for round := 0; round < 2; round++ {
		phase.Store(int32(round))
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		writer := &queueProgressWriter{updates: make(chan queueProgress, 1)}
		done := make(chan error, 1)
		go func() {
			var err error
			if round == 0 {
				_, err = downloadFresh(ctx, client, server.URL, dest, "", writer, 4)
			} else {
				_, err = resumeDownloadWithLimit(ctx, client, dest, "", writer, 4)
			}
			done <- err
		}()
		// The small prefixes do not reach the periodic 1 MiB threshold. Pause must
		// flush each worker's actual written prefix, including a stalled stream.
		waitQueue(t, func() bool {
			p := writer.segments.Load()
			if p == nil || len(p.Parts) != 4 {
				return false
			}
			for _, part := range p.Parts {
				if part.Downloaded != int64(round+1)*portion {
					return false
				}
			}
			return true
		})
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("pause: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("pause did not stop")
		}
		saved, err := loadCheckpoint(cp)
		if err != nil {
			t.Fatal(err)
		}
		if saved.Version != 4 || saved.Completed != 4*int64(round+1)*portion {
			t.Fatalf("saved progress: %+v", saved)
		}
		f, err := os.Open(saved.Partial)
		if err != nil {
			t.Fatal(err)
		}
		err = verifyOwnedPartial(f, saved)
		f.Close()
		if err != nil {
			t.Fatalf("saved prefixes failed ownership verification: %v", err)
		}
		for i, chunk := range saved.Chunks {
			if chunk.SHA256 != "" || chunk.Downloaded != int64(round+1)*portion {
				t.Fatalf("part %d was not saved: %+v", i, chunk)
			}
			sum := sha256.Sum256(payload[chunk.Start : chunk.Start+chunk.Downloaded])
			if chunk.PrefixSHA256 != fmt.Sprintf("%x", sum) {
				t.Fatal("wrong saved prefix hash")
			}
		}
	}
	phase.Store(2)
	if _, err := resumeDownloadWithLimit(context.Background(), client, dest, "", io.Discard, 2); err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
	mu.Lock()
	defer mu.Unlock()
	for round := 0; round < 3; round++ {
		starts := requests[round]
		if len(starts) != 4 {
			t.Fatalf("round %d requests: %v", round, starts)
		}
		for _, start := range starts {
			if start%step != int64(round)*portion {
				t.Fatalf("round %d redownloaded a prefix: %v", round, starts)
			}
		}
	}
}

func TestParallelPrefixCheckpointPermissionFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may bypass filesystem permissions")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("checkpoint-prefix"), 300000)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var recovery atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		if !recovery.Load() && r.Header.Get("Range") != "bytes=0-0" {
			started <- struct{}{}
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	dest, path, _ := prefixFixture(t, server.URL, payload)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := resumeDownload(ctx, server.Client(), dest, "", io.Discard)
		done <- err
	}()
	for range 2 {
		select {
		case <-started:
		case err := <-done:
			t.Fatalf("resume stopped early: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("ranges did not start")
		}
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Dir(path)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o700)
	close(release)
	select {
	case err := <-done:
		if !errors.Is(err, os.ErrPermission) {
			t.Fatalf("expected checkpoint permission error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("checkpoint failure did not stop transfer")
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("last valid prefixes changed: %v", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	recovery.Store(true)
	if _, err := resumeDownload(context.Background(), server.Client(), dest, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
}

func prefixFixture(t *testing.T, url string, payload []byte) (string, string, checkpoint) {
	t.Helper()
	dest, path, c := chunkFixture(t, url, payload, false)
	c.Version = 4
	c.Completed = 0
	for i := range c.Chunks {
		chunk := &c.Chunks[i]
		chunk.SHA256 = ""
		chunk.Downloaded = 16384 + int64(i)
		sum := sha256.Sum256(payload[chunk.Start : chunk.Start+chunk.Downloaded])
		chunk.PrefixSHA256 = fmt.Sprintf("%x", sum)
		c.Completed += chunk.Downloaded
	}
	f, err := os.OpenFile(c.Partial, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := saveCheckpoint(path, f, c); err != nil {
		t.Fatal(err)
	}
	return dest, path, c
}

func TestParallelPrefixRejectsCorruptionAndRemoteChanges(t *testing.T) {
	for _, mode := range []string{"local prefix", "truncated prefix", "remote etag", "range etag", "range offset", "range ignored"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			payload := bytes.Repeat([]byte("data"), minParallelSize/4)
			var hits atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				w.Header().Set("ETag", `"v1"`)
				probe := r.Header.Get("Range") == "bytes=0-0"
				if mode == "remote etag" || (mode == "range etag" && !probe) {
					w.Header().Set("ETag", `"changed"`)
				}
				if !probe && mode == "range offset" {
					w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-1/%d", len(payload)))
					w.WriteHeader(206)
					_, _ = w.Write(payload[:2])
					return
				}
				if !probe && mode == "range ignored" {
					_, _ = w.Write(payload)
					return
				}
				http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			}))
			defer server.Close()
			dest, path, c := prefixFixture(t, server.URL, payload)
			if mode == "local prefix" {
				f, err := os.OpenFile(c.Partial, os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.WriteAt([]byte("!"), c.Chunks[1].Start)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			if mode == "truncated prefix" {
				if err := os.Truncate(c.Partial, c.Chunks[1].Start+1); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			_, err = resumeDownload(context.Background(), server.Client(), dest, "", io.Discard)
			if err == nil {
				t.Fatal("unsafe resume accepted")
			}
			if strings.Contains(mode, "prefix") && hits.Load() != 0 {
				t.Fatal("contacted server before checking saved bytes")
			}
			after, err := loadCheckpoint(path)
			if err != nil {
				t.Fatal(err)
			}
			if after.Completed != c.Completed {
				t.Fatal("invalid response changed reusable progress")
			}
			if mode == "local prefix" || mode == "truncated prefix" || mode == "remote etag" {
				data, _ := os.ReadFile(path)
				if !bytes.Equal(data, before) {
					t.Fatal("failed validation changed checkpoint")
				}
			}
			requireMissing(t, dest)
		})
	}
}

func TestParallelPrefixMetadataValidation(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, _, base := prefixFixture(t, "http://example.com/file", bytes.Repeat([]byte("z"), 1<<20))
	tests := map[string]func(*checkpoint){
		"negative length":          func(c *checkpoint) { c.Chunks[0].Downloaded = -1 },
		"past range":               func(c *checkpoint) { c.Chunks[0].Downloaded = c.Chunks[0].End - c.Chunks[0].Start + 1 },
		"unvalidated full range":   func(c *checkpoint) { c.Chunks[0].Downloaded = c.Chunks[0].End - c.Chunks[0].Start },
		"missing digest":           func(c *checkpoint) { c.Chunks[0].PrefixSHA256 = "" },
		"malformed digest":         func(c *checkpoint) { c.Chunks[0].PrefixSHA256 = "bad" },
		"zero with digest":         func(c *checkpoint) { c.Chunks[0].Downloaded = 0 },
		"wrong total":              func(c *checkpoint) { c.Completed++ },
		"old version":              func(c *checkpoint) { c.Version = 3 },
		"completed prefix overlap": func(c *checkpoint) { c.Chunks[0].SHA256 = c.Chunks[0].PrefixSHA256 },
	}
	for name, mutate := range tests {
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

func TestParallelPrefixProcessKill(t *testing.T) {
	if os.Getenv("FLUXGET_PREFIX_CHILD") == "1" {
		_, err := downloadFresh(context.Background(), defaultDownloadClient(), os.Getenv("FLUXGET_PREFIX_URL"), os.Getenv("FLUXGET_PREFIX_DEST"), "", io.Discard, 4)
		t.Fatalf("child stopped before kill: %v", err)
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("crash-prefix"), 600000)
	var recover atomic.Bool
	var mu sync.Mutex
	var starts []int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		var start, end int64
		_, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		if err != nil || end == 0 {
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		if recover.Load() {
			mu.Lock()
			starts = append(starts, start)
			mu.Unlock()
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(206)
		_, _ = w.Write(payload[start : start+(1<<20)+32768])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "file")
	path, err := checkpointLocation(dest)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestParallelPrefixProcessKill$")
	cmd.Env = append(os.Environ(), "FLUXGET_PREFIX_CHILD=1", "FLUXGET_PREFIX_URL="+server.URL, "FLUXGET_PREFIX_DEST="+dest)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
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
		if e != nil || len(c.Chunks) != 4 {
			return false
		}
		for _, p := range c.Chunks {
			if p.Downloaded < 1<<20 || p.SHA256 != "" {
				return false
			}
		}
		return true
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
	recover.Store(true)
	if _, err := resumeDownload(context.Background(), server.Client(), dest, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
	mu.Lock()
	defer mu.Unlock()
	expected := make(map[int64]bool)
	for _, p := range saved.Chunks {
		expected[p.Start+p.Downloaded] = true
	}
	if len(starts) != 4 {
		t.Fatalf("range requests: %v", starts)
	}
	for _, start := range starts {
		if !expected[start] {
			t.Fatalf("resume ignored saved offset: %v", starts)
		}
	}
}

func TestParallelPrefixCheckpointPublicationAtomic(t *testing.T) {
	if stage := os.Getenv("FLUXGET_PREFIX_SAVE_STAGE"); stage != "" {
		dest := filepath.Join(os.Getenv("FLUXGET_PREFIX_SAVE_DIR"), "file")
		path, err := checkpointLocation(dest)
		if err != nil {
			t.Fatal(err)
		}
		c, err := loadCheckpoint(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.OpenFile(c.Partial, os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		p := &c.Chunks[0]
		p.Downloaded += 4096
		sum := sha256.New()
		_, err = io.CopyN(sum, io.NewSectionReader(f, p.Start, p.Downloaded), p.Downloaded)
		if err != nil {
			t.Fatal(err)
		}
		p.PrefixSHA256 = fmt.Sprintf("%x", sum.Sum(nil))
		c.Completed += 4096
		if err := saveCheckpointWithHook(path, f, c, func(at string) {
			if at == stage {
				os.Exit(47)
			}
		}); err != nil {
			t.Fatal(err)
		}
		t.Fatal("hook was not reached")
	}
	for _, stage := range []string{"checkpoint-written", "checkpoint-synced", "checkpoint-published"} {
		t.Run(stage, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			payload := bytes.Repeat([]byte("s"), 1<<20)
			dest, path, before := prefixFixture(t, "http://example.com/file", payload)
			cmd := exec.Command(os.Args[0], "-test.run=^TestParallelPrefixCheckpointPublicationAtomic$")
			cmd.Env = append(os.Environ(), "FLUXGET_PREFIX_SAVE_STAGE="+stage, "FLUXGET_PREFIX_SAVE_DIR="+filepath.Dir(dest))
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatal("child did not exit")
			} else if e, ok := err.(*exec.ExitError); !ok || e.ExitCode() != 47 {
				t.Fatalf("child: %v %s", err, output)
			}
			after, err := loadCheckpoint(path)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "checkpoint-published" {
				if after.Completed != before.Completed+4096 || after.Chunks[0].Downloaded != before.Chunks[0].Downloaded+4096 {
					t.Fatal("published prefix missing")
				}
			} else if !reflect.DeepEqual(after, before) {
				t.Fatal("old checkpoint changed before publication")
			}
		})
	}
}
