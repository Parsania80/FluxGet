//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestAutomaticBudgetsAcrossDownloadsAndRestart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	payload := bytes.Repeat([]byte("budget-data"), 120000)
	gate := make(chan struct{})
	var activeA, peakA, activeB, peakB, activeAll, peakAll atomic.Int32
	record := func(active, peak *atomic.Int32) func() {
		n := active.Add(1)
		for old := peak.Load(); n > old; old = peak.Load() {
			if peak.CompareAndSwap(old, n) {
				break
			}
		}
		return func() { active.Add(-1) }
	}
	handler := func(active, peak *atomic.Int32) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			defer record(active, peak)()
			defer record(&activeAll, &peakAll)()
			w.Header().Set("ETag", `"budget-v1"`)
			if r.Header.Get("Range") != "bytes=0-0" {
				// Keep a real range request open without losing header validation.
				select {
				case <-gate:
				case <-r.Context().Done():
					return
				}
			}
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
		}
	}
	a := httptest.NewServer(handler(&activeA, &peakA))
	defer a.Close()
	b := httptest.NewServer(handler(&activeB, &peakB))
	defer b.Close()
	q, err := startConfiguredQueue(binary, 3, 4, 0, "", func(string) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.send("quit"); <-q.done }()
	policy := requestPolicy{Mode: "automatic", Total: 2, PerOrigin: 1}
	initial := unifiedRequest(t, q, desktopRequest{Operation: "initialize", Management: &policy})
	if initial.Management == nil || initial.Management.Policy != policy {
		t.Fatal("policy not acknowledged")
	}
	dir := t.TempDir()
	for i, url := range []string{a.URL, a.URL, b.URL} {
		unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: url, Destination: filepath.Join(dir, []string{"first", "second", "third"}[i])}})
	}
	unifiedWait(t, q, func(j []queueEntry) bool {
		return len(j) == 3 && j[2].Requests != nil && j[2].Requests.Active == 1 && j[0].Requests != nil && j[1].Requests != nil && j[0].Requests.Active+j[1].Requests.Active == 1 && j[0].Requests.Waiting+j[1].Requests.Waiting > 0
	})
	if peakAll.Load() > 2 || peakA.Load() > 1 || peakB.Load() > 1 {
		t.Fatalf("request limits exceeded: global=%d a=%d b=%d", peakAll.Load(), peakA.Load(), peakB.Load())
	}
	invalid := policy
	invalid.Total = 0
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	rejected, err := q.request(ctx, desktopRequest{Operation: "configure", MaxActive: 3, Connections: 4, Management: &invalid})
	cancel()
	if err == nil || rejected.Management == nil || rejected.Management.Policy != policy {
		t.Fatal("invalid policy was applied")
	}
	// A queued waiter is canceled even when every origin/global slot is held.
	if err := q.send("pause 2"); err != nil {
		t.Fatal(err)
	}
	unifiedWait(t, q, func(j []queueEntry) bool {
		return j[1].State == "paused" && j[1].Requests.Waiting == 0 && j[1].Requests.Active == 0
	})
	close(gate)
	entries := unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "completed" && j[2].State == "completed" })
	for _, i := range []int{0, 2} {
		data, err := os.ReadFile(entries[i].Destination)
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("file %d: %v", i, err)
		}
		if entries[i].Measurements == nil || entries[i].Measurements.Accepted != int64(len(payload)) || entries[i].Measurements.Requests != 5 {
			t.Fatalf("metrics for %d: %+v", i, entries[i].Measurements)
		}
	}
	snapshot := unifiedRequest(t, q, desktopRequest{Operation: "snapshot"})
	if snapshot.Management.Active != 0 || snapshot.Management.Waiting != 0 {
		t.Fatal("completed/paused queue leaked leases")
	}
	if err := q.send("quit"); err != nil {
		t.Fatal(err)
	}
	<-q.done
	metadata, err := os.ReadFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fluxget", "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]any
	if err := json.Unmarshal(metadata, &persisted); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metadata, []byte("measurements")) || bytes.Contains(metadata, []byte("budget_wait")) {
		t.Fatal("runtime observations became recovery metadata")
	}
	q2, err := startConfiguredQueue(binary, 3, 4, 0, "", func(string) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q2.send("quit"); <-q2.done }()
	reopened := unifiedRequest(t, q2, desktopRequest{Operation: "initialize", Management: &policy})
	if reopened.Jobs[1].State != "paused" || reopened.Management.Active != 0 || reopened.Management.Waiting != 0 {
		t.Fatal("restart resumed a paused file or restored stale leases")
	}
}
