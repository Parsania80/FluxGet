//go:build linux

package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestRealParallelPartsAndResumeWithFewerConnections(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	payload := bytes.Repeat([]byte("part"), 524291)
	chunkSize := int64(len(payload)) / 4
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"parts-v1"`)
		var start, end int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err == nil && end > 0 && start < chunkSize*2 {
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
			w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
			w.WriteHeader(http.StatusPartialContent)
			w.(http.Flusher).Flush()
			select {
			case <-release:
				_, _ = w.Write(payload[start : end+1])
			case <-r.Context().Done():
			}
			return
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	settings, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Overrides["queue:1"] = 256
	if err := settings.save(); err != nil {
		t.Fatal(err)
	}
	start := func(connections int) *queueClient {
		q, err := startConfiguredQueue(binary, 1, connections, 0, settings.path, func(string) {}, func(error) {})
		if err != nil {
			t.Fatal(err)
		}
		return q
	}
	q := start(4)
	defer func() { _ = q.send("quit"); <-q.done }()
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	dest := filepath.Join(t.TempDir(), "file.bin")
	unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: dest}})
	entries := unifiedWait(t, q, func(j []queueEntry) bool {
		if len(j) != 1 || len(j[0].Segments) != 4 {
			return false
		}
		p := j[0].Segments
		return p[0].Downloaded == 0 && p[1].Downloaded == 0 && p[2].State == "completed" && p[3].State == "completed"
	})
	j := entries[0]
	if j.TransferMode != "parallel" || j.ConnectionLimit != 4 {
		t.Fatalf("method/limit: %+v", j)
	}
	var next int64
	for i, p := range j.Segments {
		wantEnd := int64(i+1) * chunkSize
		if i == 3 {
			wantEnd = int64(len(payload))
		}
		if p.Start != next || p.End != wantEnd {
			t.Fatalf("part %d has invented boundaries: %+v", i, p)
		}
		next = p.End
	}
	if err := q.send("pause 1"); err != nil {
		t.Fatal(err)
	}
	unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "paused" && j[0].Segments[0].State == "paused" })
	if err := q.send("quit"); err != nil {
		t.Fatal(err)
	}
	<-q.done
	q2 := start(1)
	defer func() { _ = q2.send("quit"); <-q2.done }()
	restored := unifiedRequest(t, q2, desktopRequest{Operation: "initialize"})
	if len(restored.Jobs[0].Segments) != 4 || restored.Jobs[0].Segments[2].State != "saved" {
		t.Fatal("checkpoint parts were not restored")
	}
	close(release)
	if err := q2.send("resume 1"); err != nil {
		t.Fatal(err)
	}
	unifiedWait(t, q2, func(j []queueEntry) bool {
		if len(j[0].Segments) != 4 || j[0].ConnectionLimit != 1 {
			return false
		}
		active, waiting, done := 0, 0, 0
		for _, p := range j[0].Segments {
			switch p.State {
			case "connecting", "downloading":
				active++
			case "waiting":
				waiting++
			case "completed":
				done++
			}
		}
		return active == 1 && waiting == 1 && done == 2
	})
	entries = unifiedWait(t, q2, func(j []queueEntry) bool { return j[0].State == "completed" })
	for _, p := range entries[0].Segments {
		if p.State != "completed" || p.Downloaded != p.End-p.Start {
			t.Fatalf("completed part wrong: %+v", p)
		}
	}
	data, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("resumed output differs: %v", err)
	}
}

func TestSequentialFallbackShowsOneRealPart(t *testing.T) {
	binary := unifiedCLI(t)
	for _, known := range []bool{true, false} {
		t.Run(fmt.Sprint("known-size-", known), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			gate := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Range") != "" {
					w.WriteHeader(http.StatusOK)
					return
				}
				if known {
					w.Header().Set("Content-Length", "10000")
				}
				_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
				w.(http.Flusher).Flush()
				select {
				case <-gate:
					_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
				case <-r.Context().Done():
				}
			}))
			defer server.Close()
			q, err := startConfiguredQueue(binary, 1, 4, 0, "", func(string) {}, func(error) {})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = q.send("quit"); <-q.done }()
			unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
			unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: filepath.Join(t.TempDir(), "single")}})
			entries := unifiedWait(t, q, func(j []queueEntry) bool {
				return len(j) == 1 && len(j[0].Segments) == 1 && j[0].Segments[0].Downloaded == 5000
			})
			j := entries[0]
			if j.TransferMode != "sequential" || j.ConnectionLimit != 1 {
				t.Fatal("fallback fabricated four connections")
			}
			if !known {
				if _, valid := segmentValue(j.Segments[0]); valid {
					t.Fatal("unknown stream has invented percentage")
				}
			}
			close(gate)
			unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "completed" })
		})
	}
}

func TestSegmentRowsBindToTheirOwnDownload(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("parts")
	defer w.Close()
	panel := newSegmentPanel()
	w.SetContent(panel)
	selected := ""
	updateSegmentPanel(panel, queueEntry{State: "running", TransferMode: "parallel", ConnectionLimit: 4, Segments: []downloadSegment{{Start: 0, End: 100, Downloaded: 25, State: "downloading"}, {Start: 100, End: 200, Downloaded: 100, State: "completed"}}}, func() { selected = "first" })
	header := panel.Objects[0].(*fyne.Container)
	button := header.Objects[1].(*widget.Button)
	test.Tap(button)
	if selected != "first" {
		t.Fatal("wrong first row")
	}
	updateSegmentPanel(panel, queueEntry{State: "running", TransferMode: "sequential", ConnectionLimit: 1, Segments: []downloadSegment{{Start: 0, End: 100, Downloaded: 60, State: "downloading"}}}, func() { selected = "second" })
	test.Tap(button)
	if selected != "second" || len(panel.Objects[1].(*fyne.Container).Objects) != 1 {
		t.Fatal("recycled row retained another download’s parts")
	}
}

func TestPartDetailsRefreshWhileDownloadRuns(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("parts")
	defer w.Close()
	settings := &speedSettings{MaxActive: 5, Connections: 4, DisplayUnit: "MB", Overrides: map[string]int64{}}
	v := newDownloadsView(a, w, settings, nil, "")
	w.SetContent(v.content)
	w.Resize(fyne.NewSize(1100, 720))
	v.entries = []queueEntry{{ID: 7, State: "running", Destination: "/tmp/example", TransferMode: "parallel", ConnectionLimit: 2, Segments: []downloadSegment{{Start: 0, End: 1000000, Downloaded: 250000, State: "downloading"}, {Start: 1000000, End: 2000000, Downloaded: 500000, State: "downloading"}}}}
	v.refresh()
	v.showParts("queue:7")
	card := v.partDetails.Objects[1].(*fyne.Container)
	if value := card.Objects[2].(*widget.ProgressBar).Value; value != 0.25 {
		t.Fatalf("initial part value %v", value)
	}
	v.entries[0].Segments[0].Downloaded = 750000
	v.refresh()
	if value := card.Objects[2].(*widget.ProgressBar).Value; value != 0.75 {
		t.Fatalf("open details did not update: %v", value)
	}
	if got := card.Objects[1].(*widget.Label).Text; got != "Range 0.00–1.00 MB · received 0.75 / 1.00 MB" {
		t.Fatalf("range detail: %q", got)
	}
	v.partDialog.Hide()
	if v.partDetails != nil || v.partKey != "" {
		t.Fatal("closed dialog retained live callbacks")
	}
}

func TestUnfinishedPartsSurviveDesktopRestart(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	payload := bytes.Repeat([]byte("desktop-prefix"), 120000)
	var finish atomic.Bool
	var mu sync.Mutex
	var resumed []int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		var start, end int64
		_, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end)
		if err != nil || end == 0 {
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		if finish.Load() {
			mu.Lock()
			resumed = append(resumed, start)
			mu.Unlock()
			http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			return
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(payload)))
		w.Header().Set("Content-Length", fmt.Sprint(end-start+1))
		w.WriteHeader(206)
		_, _ = w.Write(payload[start : start+32768])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	startQueue := func(connections int) *queueClient {
		q, err := startConfiguredQueue(binary, 1, connections, 0, "", func(string) {}, func(error) {})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			select {
			case <-q.done:
				return
			default:
				_ = q.send("quit")
			}
			select {
			case <-q.done:
			case <-time.After(10 * time.Second):
				t.Error("scheduler did not stop")
			}
		})
		return q
	}
	q := startQueue(4)
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	dest := filepath.Join(t.TempDir(), "file")
	unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: dest}})
	unifiedWait(t, q, func(j []queueEntry) bool {
		if len(j) != 1 || len(j[0].Segments) != 4 {
			return false
		}
		for _, part := range j[0].Segments {
			if part.Downloaded != 32768 {
				return false
			}
		}
		return true
	})
	if err := q.send("pause 1"); err != nil {
		t.Fatal(err)
	}
	before := unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "paused" })[0]
	saved, known := progressValue(before.Progress)
	if !known || saved <= 0 {
		t.Fatal("overall progress lost the unfinished prefixes")
	}
	if err := q.send("quit"); err != nil {
		t.Fatal(err)
	}
	<-q.done
	q2 := startQueue(1)
	reopened := unifiedRequest(t, q2, desktopRequest{Operation: "initialize"}).Jobs[0]
	after, known := progressValue(reopened.Progress)
	if !known || after != saved || len(reopened.Segments) != 4 {
		t.Fatalf("overall progress reset after reopening: before=%s after=%s", before.Progress, reopened.Progress)
	}
	for _, part := range reopened.Segments {
		if part.Downloaded != 32768 || part.State != "paused" {
			t.Fatalf("part progress reset: %+v", part)
		}
	}
	a := test.NewApp()
	defer a.Quit()
	panel := newSegmentPanel()
	updateSegmentPanel(panel, reopened, func() {})
	first := panel.Objects[1].(*fyne.Container).Objects[0].(*fyne.Container).Objects[1].(*widget.ProgressBar)
	if first.Value <= 0 {
		t.Fatal("saved part renders an empty bar")
	}
	finish.Store(true)
	if err := q2.send("resume 1"); err != nil {
		t.Fatal(err)
	}
	unifiedWait(t, q2, func(j []queueEntry) bool { return j[0].State == "completed" })
	data, err := os.ReadFile(dest)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("wrong completed file: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(resumed) != 4 {
		t.Fatalf("resumed requests: %v", resumed)
	}
	step := int64(len(payload)) / 4
	for _, offset := range resumed {
		if offset%step != 32768 {
			t.Fatalf("saved prefixes downloaded again: %v", resumed)
		}
	}
}
