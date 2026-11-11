//go:build windows

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/Parsania80/FluxGet/internal/platformfile"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestWindowsOwnershipAndPublication(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file.part")
	f, err := openPartial(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	token := createPartialToken(f)
	if len(token) != 32 || !checkPartialToken(f, token) {
		t.Fatal("NTFS ownership marker unavailable")
	}
	lockPath := filepath.Join(t.TempDir(), "job.lock")
	lock, err := lockJob(lockPath, f)
	if err != nil {
		t.Fatal(err)
	}
	if active, err := jobActive(lockPath); err != nil || !active {
		t.Fatalf("active=%v error=%v", active, err)
	}
	if other, err := lockJob(lockPath, f); err == nil {
		other.Close()
		t.Fatal("second owner accepted")
	}
	// Mandatory Windows locks must leave real file data writable.
	if _, err := f.Write([]byte("content")); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(filepath.Dir(path), "published")
	if err := os.Link(path, dest); err != nil {
		t.Fatal(err)
	}
	clearPartialToken(f, token)
	if checkPartialToken(f, token) {
		t.Fatal("marker not removed")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, []byte("content"))
	if active, err := jobActive(lockPath); err != nil || active {
		t.Fatalf("released active=%v error=%v", active, err)
	}
}

func TestWindowsParallelResumeAndDelete(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	payload := bytes.Repeat([]byte("FluxGet ranges\n"), 200000)
	var ranges atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"windows-v1"`)
		if r.Header.Get("Range") != "" {
			ranges.Add(1)
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	dest := filepath.Join(t.TempDir(), "parallel.bin")
	client := defaultDownloadClient()
	defer client.CloseIdleConnections()
	if _, err := downloadFresh(context.Background(), client, server.URL, dest, fmt.Sprintf("%x", sha256.Sum256(payload)), io.Discard, 4); err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
	if ranges.Load() < 5 {
		t.Fatalf("parallel ranges missing: %d", ranges.Load())
	}
	f, err := openPartial(dest, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	identity, _, err := identifyPartial(f)
	if err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat()
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	q := queueData{Version: 2, NextID: 2, Jobs: []queueJob{{ID: 1, URL: server.URL, Destination: dest, State: "completed"}}}
	_, err = deleteQueueFiles(&q, 0, filepath.Join(t.TempDir(), "queue.json"), &outputRemoval{Path: dest, Device: identity.Device, Inode: identity.Inode, Size: info.Size(), Modified: info.ModTime().UnixNano()})
	if err != nil {
		t.Fatal(err)
	}
	requireMissing(t, dest)
	// A truncated response must save a checkpoint and resume its exact prefix.
	interrupted := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"windows-v1"`)
		if r.Header.Get("Range") == "" {
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.Write(payload[:50000])
			return
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer interrupted.Close()
	dest = filepath.Join(t.TempDir(), "resume.bin")
	if _, err := download(context.Background(), client, interrupted.URL, dest, "", nil); err == nil {
		t.Fatal("truncation accepted")
	}
	cp, err := checkpointLocation(dest)
	if err != nil {
		t.Fatal(err)
	}
	c, err := loadCheckpoint(cp)
	if err != nil || c.Completed != 50000 {
		t.Fatalf("checkpoint=%+v error=%v", c, err)
	}
	if _, err := resumeDownloadWithLimit(context.Background(), client, dest, "", nil, 1); err != nil {
		t.Fatal(err)
	}
	requireContents(t, dest, payload)
	requireMissing(t, dest+".part")
	requireMissing(t, cp)
}

func TestWindowsRejectsSpecialFilePaths(t *testing.T) {
	for _, name := range []string{"CON", "NUL.zip", "COM1.bin", "file:stream", "trailing.", "trailing "} {
		if safeFilename(name) {
			t.Fatalf("unsafe filename accepted: %q", name)
		}
		if f, err := openPartial(filepath.Join(t.TempDir(), name), os.O_CREATE|os.O_RDWR, 0600); err == nil {
			f.Close()
			t.Fatalf("unsafe path opened: %q", name)
		}
	}
}

func TestWindowsMetadataReplacementWithOpenReader(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := platformfile.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := os.WriteFile(path+".new", []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatalf("metadata reader blocked atomic replacement: %v", err)
	}
	old, err := io.ReadAll(reader)
	if err != nil || string(old) != "old" {
		t.Fatalf("old snapshot=%q error=%v", old, err)
	}
	fresh, err := platformfile.ReadFile(path)
	if err != nil || string(fresh) != "new" {
		t.Fatalf("new snapshot=%q error=%v", fresh, err)
	}
}
