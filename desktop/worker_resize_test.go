//go:build linux

package main

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestSchedulerResizesLiveWorkersAndRestoresPausedParts(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	payload := bytes.Repeat([]byte("resize!"), 1200000)
	release := make(chan struct{})
	var active atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"resize-v1"`)
		if r.Header.Get("Range") == "bytes=0-0" {
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil {
			http.Error(w, "invalid range", 400)
			return
		}
		active.Add(1)
		defer active.Add(-1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(http.StatusPartialContent)
		n := min(int64(64<<10), end-start+1)
		_, _ = w.Write(payload[start : start+n])
		w.(http.Flusher).Flush()
		select {
		case <-release:
			_, _ = w.Write(payload[start+n : end+1])
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	q, err := startConfiguredQueue(binary, 2, 4, 0, "", func(string) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.send("quit"); <-q.done }()
	policy := requestPolicy{Mode: "automatic", Total: 8, PerOrigin: 4}
	unifiedRequest(t, q, desktopRequest{Operation: "initialize", Management: &policy})
	dir := t.TempDir()
	dest := filepath.Join(dir, "live.bin")
	unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: dest}})
	unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: filepath.Join(dir, "later.bin"), State: "paused"}})
	waitWorkers := func(n int) []queueEntry {
		t.Helper()
		return unifiedWait(t, q, func(j []queueEntry) bool {
			if len(j) != 2 || j[0].State != "running" || len(j[0].Segments) != 4 || j[0].WorkerTarget != n || int(active.Load()) != n || j[1].State != "paused" {
				return false
			}
			if j[0].Requests == nil || j[0].Requests.Active != n {
				return false
			}
			count := 0
			for _, part := range j[0].Segments {
				if part.State == "retiring" {
					return false
				}
				if part.State == "downloading" && part.Downloaded >= 64<<10 {
					count++
				}
			}
			return count == n
		})
	}
	waitWorkers(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	_, err = q.request(ctx, desktopRequest{Operation: "workers", Job: queueEntry{ID: 1}, Connections: 5})
	cancel()
	if err == nil {
		t.Fatal("target exceeded user ceiling")
	}
	for _, target := range []int{2, 4, 1, 4, 1} {
		unifiedRequest(t, q, desktopRequest{Operation: "workers", Job: queueEntry{ID: 1}, Connections: target})
		waitWorkers(target)
	}
	policy.Mode = "manual"
	unifiedRequest(t, q, desktopRequest{Operation: "configure", MaxActive: 2, Connections: 2, Management: &policy})
	waitWorkers(2)
	unifiedRequest(t, q, desktopRequest{Operation: "configure", MaxActive: 2, Connections: 4, Management: &policy})
	waitWorkers(4)
	// Existing parts remain fixed even if the configured maximum grows.
	unifiedRequest(t, q, desktopRequest{Operation: "configure", MaxActive: 2, Connections: 8, Management: &policy})
	entries := waitWorkers(4)
	if entries[0].WorkerReason != "Limited by saved part layout" || entries[0].ConnectionLimit != 8 {
		t.Fatal("saved layout ceiling not explained", entries[0].WorkerReason)
	}
	policy.Mode = "automatic"
	unifiedRequest(t, q, desktopRequest{Operation: "configure", MaxActive: 2, Connections: 4, Management: &policy})
	waitWorkers(1)
	if err := q.send("pause 1"); err != nil {
		t.Fatal(err)
	}
	unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "paused" && active.Load() == 0 })
	if err := q.send("quit"); err != nil {
		t.Fatal(err)
	}
	<-q.done
	metadata, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fluxget", "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"worker_target", "worker_changes", "worker_reason"} {
		if bytes.Contains(metadata, []byte(name)) {
			t.Fatal("runtime resize state was persisted", name)
		}
	}
	q2, err := startConfiguredQueue(binary, 2, 4, 0, "", func(string) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q2.send("quit"); <-q2.done }()
	restored := unifiedRequest(t, q2, desktopRequest{Operation: "initialize", Management: &policy})
	if restored.Management.Active != 0 || restored.Jobs[0].State != "paused" || len(restored.Jobs[0].Segments) != 4 {
		t.Fatal("restart lost parts or resumed work")
	}
	close(release)
	unifiedRequest(t, q2, desktopRequest{Operation: "action", Action: "resume", Job: queueEntry{ID: 1}})
	unifiedWait(t, q2, func(j []queueEntry) bool { return j[0].State == "completed" })
	data, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("live resize/restart output differs", err)
	}
}

func TestLiveWorkerSettingsLeaveSequentialResponsesAlone(t *testing.T) {
	for _, unknown := range []bool{false, true} {
		t.Run(fmt.Sprint("unknown=", unknown), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			binary := unifiedCLI(t)
			payload := bytes.Repeat([]byte("sequential"), 10000)
			release := make(chan struct{})
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				if !unknown {
					w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
				}
				_, _ = w.Write(payload[:32000])
				w.(http.Flusher).Flush()
				select {
				case <-release:
					_, _ = w.Write(payload[32000:])
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			finish := sync.OnceFunc(func() { close(release) })
			defer finish()
			q := unifiedQueue(t, binary, 1)
			policy := requestPolicy{Mode: "automatic", Total: 4, PerOrigin: 4}
			unifiedRequest(t, q, desktopRequest{Operation: "initialize", Management: &policy})
			unifiedRequest(t, q, desktopRequest{Operation: "configure", MaxActive: 1, Connections: 4, Management: &policy})
			dest := filepath.Join(t.TempDir(), "sequential.bin")
			unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: dest}})
			unifiedWait(t, q, func(j []queueEntry) bool {
				return len(j) == 1 && j[0].TransferMode == "sequential" && len(j[0].Segments) == 1 && j[0].Segments[0].Downloaded >= 32000
			})
			before := calls.Load()
			unifiedRequest(t, q, desktopRequest{Operation: "workers", Job: queueEntry{ID: 1}, Connections: 4})
			policy.Mode = "manual"
			unifiedRequest(t, q, desktopRequest{Operation: "configure", MaxActive: 1, Connections: 1, Management: &policy})
			policy.Mode = "automatic"
			unifiedRequest(t, q, desktopRequest{Operation: "configure", MaxActive: 1, Connections: 4, Management: &policy})
			p := unifiedRequest(t, q, desktopRequest{Operation: "snapshot"}).Jobs[0]
			if calls.Load() != before || p.ConnectionLimit != 1 || p.WorkerTarget != 1 || p.Segments[0].State != "downloading" {
				t.Fatal("sequential transfer restarted for tuning", p)
			}
			finish()
			unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "completed" })
			data, err := os.ReadFile(dest)
			if err != nil || !bytes.Equal(data, payload) {
				t.Fatal("sequential output differs", err)
			}
		})
	}
}

func TestResizePartStatusExplainsRetirementAndLayout(t *testing.T) {
	j := queueEntry{ConnectionLimit: 8, WorkerTarget: 4, WorkerReason: "Limited by saved part layout", Segments: []downloadSegment{{State: "downloading"}, {State: "retiring"}, {State: "waiting"}, {State: "waiting"}}}
	text := segmentSummary(j)
	if !strings.Contains(text, "2 active") || !strings.Contains(text, "target 4") || !strings.Contains(text, j.WorkerReason) || segmentState("retiring") != "Saving before stopping" {
		t.Fatal("resize status is misleading", text)
	}
}
