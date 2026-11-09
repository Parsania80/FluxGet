//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
)

func unifiedCLI(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "fluxget")
	cmd := exec.Command("go", "build", "-o", binary, ".")
	cmd.Dir = ".."
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	return binary
}
func unifiedQueue(t *testing.T, binary string, active int) *queueClient {
	t.Helper()
	q, err := startConfiguredQueue(binary, active, 1, 0, "", func(string) {}, func(error) {})
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
func unifiedRequest(t *testing.T, q *queueClient, request desktopRequest) desktopResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	response, err := q.request(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
func unifiedWait(t *testing.T, q *queueClient, match func([]queueEntry) bool) []queueEntry {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		response := unifiedRequest(t, q, desktopRequest{Operation: "snapshot"})
		if match(response.Jobs) {
			return response.Jobs
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatal("download state did not converge")
	return nil
}

func TestUnifiedAutomaticSchedulingAndDownloadLater(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	payload := bytes.Repeat([]byte("test transfer"), 6000)
	gates := map[string]chan struct{}{"/one": make(chan struct{}), "/two": make(chan struct{})}
	started := make(chan string, 8)
	var active, peak, later atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := active.Add(1)
		defer active.Add(-1)
		for old := peak.Load(); current > old; old = peak.Load() {
			if peak.CompareAndSwap(old, current) {
				break
			}
		}
		if r.URL.Path == "/later" {
			later.Add(1)
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Content-Disposition", `attachment; filename="`+r.URL.Path[1:]+`.bin"`)
		w.Header().Set("Content-Length", "78000")
		_, _ = w.Write(payload[:32000])
		w.(http.Flusher).Flush()
		started <- r.URL.Path
		if gate := gates[r.URL.Path]; gate != nil {
			select {
			case <-gate:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = w.Write(payload[32000:])
	}))
	defer server.Close()
	q := unifiedQueue(t, binary, 1)
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	dir := t.TempDir()
	for _, name := range []string{"one", "two"} {
		unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL + "/" + name, Destination: filepath.Join(dir, name), AutomaticName: true}})
	}
	unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL + "/later", Destination: filepath.Join(dir, "later.bin"), State: "paused"}})
	select {
	case got := <-started:
		if got != "/one" {
			t.Fatalf("started %s", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("download did not start automatically")
	}
	unifiedWait(t, q, func(j []queueEntry) bool {
		return len(j) == 3 && j[0].State == "running" && j[1].State == "queued" && j[2].State == "paused"
	})
	close(gates["/one"])
	select {
	case got := <-started:
		if got != "/two" {
			t.Fatalf("started %s", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("waiting download did not start")
	}
	close(gates["/two"])
	unifiedWait(t, q, func(j []queueEntry) bool {
		return len(j) == 3 && j[0].State == "completed" && j[1].State == "completed" && j[2].State == "paused"
	})
	if later.Load() != 0 || peak.Load() != 1 {
		t.Fatalf("later=%d peak=%d", later.Load(), peak.Load())
	}
	for _, name := range []string{"one.bin", "two.bin"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("%s differs: %v", name, err)
		}
	}
	if err := q.send("resume 3"); err != nil {
		t.Fatal(err)
	}
	unifiedWait(t, q, func(j []queueEntry) bool { return j[2].State == "completed" })
	if later.Load() != 1 {
		t.Fatalf("later requested %d times", later.Load())
	}
}

func TestUnifiedMigrationIsIdempotentAndKeepsLimits(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); _, _ = w.Write([]byte("unexpected")) }))
	defer server.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, "paused.bin")
	completed := filepath.Join(dir, "completed.bin")
	if err := os.WriteFile(completed, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	progress := 0.42
	history := []job{{URL: server.URL, Path: path, State: "Interrupted", Progress: &progress}, {URL: server.URL + "/complete", Path: completed, State: "Completed"}}
	if err := saveHistory(history); err != nil {
		t.Fatal(err)
	}
	settings, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Overrides["download:0"] = 128
	if err := settings.save(); err != nil {
		t.Fatal(err)
	}
	historyFile, _ := historyPath()
	before, _ := os.ReadFile(historyFile)
	q := unifiedQueue(t, binary, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	migrated, entries, err := migrateDownloads(ctx, q, history, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].State != "paused" || entries[1].State != "completed" || entries[0].SavedProgress == nil || *entries[0].SavedProgress != progress {
		t.Fatalf("migration: %+v", entries)
	}
	if migrated[0].QueueID == 0 || settings.Overrides["queue:1"] != 128 {
		t.Fatalf("limits or identity not migrated: %+v", settings)
	}
	again, entries, err := migrateDownloads(ctx, q, migrated, settings)
	if err != nil || len(entries) != 2 || again[0].QueueID != migrated[0].QueueID {
		t.Fatalf("repeated migration: %+v %v", entries, err)
	}
	backup, err := os.ReadFile(historyFile + ".before-unified")
	if err != nil || !bytes.Equal(backup, before) {
		t.Fatal("original history backup differs", err)
	}
	if requests.Load() != 0 {
		t.Fatal("migration started a network request")
	}
	data, _ := os.ReadFile(completed)
	if string(data) != "keep" {
		t.Fatal("completed file changed")
	}
}

func TestUnifiedServerNameConflictRetargetAndRestart(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	binary := unifiedCLI(t)
	dir := t.TempDir()
	original := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(original, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="existing.txt"`)
		_, _ = w.Write([]byte("new"))
	}))
	defer server.Close()
	q := unifiedQueue(t, binary, 1)
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: filepath.Join(dir, "fallback"), AutomaticName: true}})
	entries := unifiedWait(t, q, func(j []queueEntry) bool { return len(j) == 1 && j[0].State == "failed" })
	if entries[0].Conflict != original || entries[0].Message == "" {
		t.Fatalf("conflict not surfaced: %+v", entries[0])
	}
	copy := filepath.Join(dir, "copy.txt")
	unifiedRequest(t, q, desktopRequest{Operation: "retarget", Job: queueEntry{ID: 1, Destination: copy}})
	if err := q.send("resume 1"); err != nil {
		t.Fatal(err)
	}
	unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "completed" })
	data, _ := os.ReadFile(original)
	if string(data) != "keep" {
		t.Fatal("existing file overwritten")
	}
	data, _ = os.ReadFile(copy)
	if string(data) != "new" {
		t.Fatal("copy did not complete")
	}
	unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: filepath.Join(dir, "later"), State: "paused"}})
	if err := q.send("quit"); err != nil {
		t.Fatal(err)
	}
	<-q.done
	q2 := unifiedQueue(t, binary, 1)
	response := unifiedRequest(t, q2, desktopRequest{Operation: "initialize"})
	if len(response.Jobs) != 2 || response.Jobs[0].State != "completed" || response.Jobs[1].State != "paused" {
		t.Fatalf("restored: %+v", response.Jobs)
	}
	raw, err := os.ReadFile(filepath.Join(config, "fluxget", "queue.json"))
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Version int `json:"version"`
	}
	if json.Unmarshal(raw, &metadata) != nil || metadata.Version != 2 {
		t.Fatal("missing versioned migration")
	}
}

func TestUnifiedViewFiltersActionsAndLayout(t *testing.T) {
	a := test.NewApp()
	a.Settings().SetTheme(fluxTheme{theme.DefaultTheme()})
	defer a.Quit()
	w := a.NewWindow("FluxGet")
	settings := &speedSettings{MaxActive: 2, Connections: 4, Overrides: map[string]int64{"queue:1": 1024}}
	v := newDownloadsView(a, w, settings, nil, "")
	w.SetContent(v.content)
	w.Resize(fyne.NewSize(1100, 720))
	w.Show()
	defer w.Close()
	v.ready = true
	sampleSpeed := float64(2.5 * 1024 * 1024)
	v.entries = []queueEntry{{ID: 1, State: "running", Destination: "/tmp/ubuntu.iso", Speed: &sampleSpeed, Progress: "Downloaded 400000000/1000000000 bytes (40.0%)", TransferMode: "parallel", ConnectionLimit: 4, WorkerTarget: 2, WorkerReason: "Reducing active connections…", Segments: []downloadSegment{{Start: 0, End: 250000000, Downloaded: 250000000, State: "completed"}, {Start: 250000000, End: 500000000, Downloaded: 100000000, State: "retiring"}, {Start: 500000000, End: 750000000, Downloaded: 50000000, State: "downloading"}, {Start: 750000000, End: 1000000000, State: "connecting"}}}, {ID: 2, State: "queued", Destination: "/tmp/manual.pdf"}, {ID: 3, State: "paused", Destination: "/tmp/archive.zip"}, {ID: 4, State: "completed", Destination: "/tmp/photo.jpg"}}
	v.refresh()
	v.list.Select(1)
	if v.pause.Disabled() || !v.resume.Disabled() {
		t.Fatal("waiting actions incorrect")
	}
	v.category = "Paused"
	v.refresh()
	if len(v.rows()) != 1 || v.rows()[0].Entry.ID != 3 {
		t.Fatal("paused filter incorrect")
	}
	v.list.Select(0)
	if v.resume.Disabled() || !v.pause.Disabled() {
		t.Fatal("paused actions incorrect")
	}
	v.category = "Completed"
	v.refresh()
	v.list.Select(0)
	if v.open.Disabled() || !v.retry.Disabled() {
		t.Fatal("completed actions incorrect")
	}
	if screenshot := os.Getenv("FLUXGET_UI_SCREENSHOT"); screenshot != "" {
		v.category = "All downloads"
		v.selected = ""
		v.refresh()
		v.list.ScrollToTop()
		f, err := os.Create(screenshot)
		if err != nil {
			t.Fatal(err)
		}
		if err = png.Encode(f, w.Canvas().Capture()); err != nil {
			t.Fatal(err)
		}
		if err = f.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestUnifiedShutdownRestoresActiveAndWaitingPaused(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	payload := bytes.Repeat([]byte("resume safely"), 20000)
	var first atomic.Bool
	var waiting atomic.Int32
	started := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/waiting" {
			waiting.Add(1)
		}
		w.Header().Set("ETag", `"v1"`)
		if r.URL.Path == "/active" && r.Header.Get("Range") == "" && first.CompareAndSwap(false, true) {
			w.Header().Set("Content-Length", "260000")
			_, _ = w.Write(payload[:50000])
			w.(http.Flusher).Flush()
			started <- struct{}{}
			<-r.Context().Done()
			return
		}
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	q := unifiedQueue(t, binary, 1)
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	dir := t.TempDir()
	for _, name := range []string{"active", "waiting"} {
		unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL + "/" + name, Destination: filepath.Join(dir, name)}})
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("active job did not start")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		info, err := os.Stat(filepath.Join(dir, "active.part"))
		if err == nil && info.Size() >= 50000 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("partial not saved")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := q.send("quit"); err != nil {
		t.Fatal(err)
	}
	<-q.done
	q2 := unifiedQueue(t, binary, 1)
	response := unifiedRequest(t, q2, desktopRequest{Operation: "initialize"})
	if len(response.Jobs) != 2 || response.Jobs[0].State != "paused" || response.Jobs[1].State != "paused" {
		t.Fatalf("restored jobs: %+v", response.Jobs)
	}
	if waiting.Load() != 0 {
		t.Fatal("waiting job started during shutdown or restore")
	}
	if err := q2.send("resume 1"); err != nil {
		t.Fatal(err)
	}
	unifiedWait(t, q2, func(j []queueEntry) bool { return j[0].State == "completed" && j[1].State == "paused" })
	data, err := os.ReadFile(filepath.Join(dir, "active"))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("resumed bytes differ: %v", err)
	}
}

func TestUnifiedParallelNamesReserveOneDestination(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	payload := bytes.Repeat([]byte("parallel"), 300000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"parallel"`)
		w.Header().Set("Content-Disposition", `attachment; filename="same.bin"`)
		http.ServeContent(w, r, "same.bin", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	q, err := startConfiguredQueue(binary, 2, 4, 0, "", func(string) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.send("quit"); <-q.done }()
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	dir := t.TempDir()
	for _, name := range []string{"first", "second"} {
		unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: filepath.Join(dir, name), AutomaticName: true}})
	}
	entries := unifiedWait(t, q, func(j []queueEntry) bool {
		return len(j) == 2 && ((j[0].State == "completed" && j[1].State == "failed") || (j[1].State == "completed" && j[0].State == "failed"))
	})
	if entries[0].Destination == entries[1].Destination {
		t.Fatal("two jobs reserved the same destination")
	}
	data, err := os.ReadFile(filepath.Join(dir, "same.bin"))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("parallel output differs: %v", err)
	}
}

func TestUnifiedImportsVersionOneQueueWithoutStartingIt(t *testing.T) {
	config := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", config)
	binary := unifiedCLI(t)
	dir := filepath.Join(config, "fluxget")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"version":1,"next_id":2,"jobs":[{"id":1,"url":"http://127.0.0.1:1/file","destination":"` + filepath.Join(t.TempDir(), "file") + `","state":"queued"}]}`)
	path := filepath.Join(dir, "queue.json")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	q := unifiedQueue(t, binary, 1)
	response := unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	if len(response.Jobs) != 1 || response.Jobs[0].State != "paused" {
		t.Fatal("old waiting job started")
	}
	backup, err := os.ReadFile(path + ".before-unified")
	if err != nil || !bytes.Equal(backup, original) {
		t.Fatal("version-one backup missing or changed", err)
	}
}

func TestUnifiedOverallSpeedLimitChangesDuringTransfers(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	settings, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Overall = 4
	if err := settings.save(); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("limited"), 10000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
	defer server.Close()
	q, err := startConfiguredQueue(binary, 2, 1, 0, settings.path, func(string) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.send("quit"); <-q.done }()
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	dir := t.TempDir()
	for _, name := range []string{"one", "two"} {
		unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: filepath.Join(dir, name)}})
	}
	unifiedWait(t, q, func(j []queueEntry) bool { return len(j) == 2 && j[0].State == "running" && j[1].State == "running" })
	time.Sleep(350 * time.Millisecond)
	response := unifiedRequest(t, q, desktopRequest{Operation: "snapshot"})
	if response.Jobs[0].State != "running" || response.Jobs[1].State != "running" {
		t.Fatal("shared overall ceiling was not enforced")
	}
	settings.Overall = 0
	if err := settings.save(); err != nil {
		t.Fatal(err)
	}
	unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "completed" && j[1].State == "completed" })
	for _, name := range []string{"one", "two"} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !bytes.Equal(data, payload) {
			t.Fatalf("speed-limited output differs: %v", err)
		}
	}
}
