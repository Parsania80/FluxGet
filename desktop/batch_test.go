//go:build linux

package main

import (
	"bytes"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func batchDialogButton(t *testing.T, w fyne.Window, text string) *widget.Button {
	t.Helper()
	var found *widget.Button
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch o := object.(type) {
		case *widget.PopUp:
			walk(o.Content)
		case *widget.Button:
			if o.Text == text {
				found = o
			}
		case *fyne.Container:
			for _, child := range o.Objects {
				walk(child)
			}
		case *container.Scroll:
			walk(o.Content)
		case fyne.Widget:
			for _, child := range test.WidgetRenderer(o).Objects() {
				walk(child)
			}
		}
	}
	walk(w.Canvas().Overlays().Top())
	if found == nil {
		t.Errorf("missing dialog button %q", text)
		return widget.NewButton("Missing", nil)
	}
	return found
}

func TestBatchCheckboxesFiltersAndDeleteConfirmationCancel(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("Batch downloads")
	defer w.Close()
	v := newDownloadsView(a, w, &speedSettings{MaxActive: 2, Connections: 4, Overrides: map[string]int64{}}, nil, "")
	w.SetContent(v.content)
	w.Resize(fyne.NewSize(1100, 720))
	w.Show()
	v.ready = true
	dir := t.TempDir()
	for _, name := range []string{"first.zip", "second.zip"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("keep until confirmed"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	v.entries = []queueEntry{{ID: 1, State: "completed", Destination: filepath.Join(dir, "first.zip")}, {ID: 2, State: "completed", Destination: filepath.Join(dir, "second.zip")}, {ID: 3, State: "paused", Destination: filepath.Join(dir, "a-very-long-filename-for-keyboard-selection-and-batch-controls.iso")}}
	v.refresh()
	row := v.list.CreateItem().(*fyne.Container)
	v.list.UpdateItem(0, row)
	check := row.Objects[0].(*fyne.Container).Objects[1].(*fyne.Container).Objects[3].(*widget.Check)
	test.Tap(check)
	if !v.checked["queue:1"] || v.selectionCount.Text != "1 selected" {
		t.Fatal("first checkbox did not select its job")
	}
	v.list.UpdateItem(1, row)
	if check.Checked {
		t.Fatal("recycled checkbox kept another job's selection")
	}
	test.Tap(check)
	if !v.checked["queue:1"] || !v.checked["queue:2"] || !v.open.Disabled() || !v.resume.Disabled() {
		t.Fatal("mixed batch actions incorrect")
	}
	v.refresh()
	if len(v.selectedRows()) != 2 {
		t.Fatal("poll refresh lost checked jobs")
	}
	capture := func(suffix string) {
		if path := os.Getenv("FLUXGET_BATCH_SCREENSHOT"); path != "" {
			f, err := os.Create(path + suffix + ".png")
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, w.Canvas().Capture())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	capture("-list")
	v.confirmRemoval(v.selectedRows(), true)
	capture("-delete")
	test.Tap(batchDialogButton(t, w, "Cancel"))
	if v.busy {
		t.Fatal("Cancel started deletion")
	}
	for _, name := range []string{"first.zip", "second.zip"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal("Cancel deleted a file", err)
		}
	}
	v.filters.SetSelected("Paused")
	if len(v.selectedRows()) != 0 {
		t.Fatal("filter retained invisible selections")
	}
	test.Tap(v.selectAll)
	if len(v.selectedRows()) != 1 || v.selectedRows()[0].Entry.ID != 3 || v.resume.Disabled() {
		t.Fatal("Select all included hidden jobs")
	}
	test.Tap(v.clearSelection)
	if len(v.selectedRows()) != 0 || !v.remove.Disabled() {
		t.Fatal("Clear selection retained a target")
	}
}

func TestBatchControllerStartsPausesDeletesAndKeepsFiles(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	payload := bytes.Repeat([]byte("batch"), 30000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"batch-v1"`)
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	base := test.NewApp()
	defer base.Quit()
	driver := &serialTestDriver{Driver: base.Driver(), events: make(chan func(), 128), stop: make(chan struct{})}
	go func() {
		for {
			select {
			case fn := <-driver.events:
				fn()
			case <-driver.stop:
				return
			}
		}
	}()
	defer close(driver.stop)
	a := serialTestApp{App: base, driver: driver}
	fyne.SetCurrentApp(a)
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	// Batch concurrency is deterministic in Manual; Automatic starts one file.
	s.ManagementMode = "manual"
	s.MaxActive, s.Connections = 2, 1
	s.Overrides["queue:1"], s.Overrides["queue:2"] = 16, 16
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	var v *downloadsView
	var w fyne.Window
	do := func(fn func()) { driver.DoFromGoroutine(fn, true) }
	do(func() {
		w = a.NewWindow("Batch")
		v = newDownloadsView(a, w, s, nil, binary)
		w.SetContent(v.content)
		w.Show()
		v.connect()
	})
	closed := make(chan struct{})
	defer func() {
		do(func() { v.close(func() { w.Close(); close(closed) }) })
		select {
		case <-closed:
		case <-time.After(10 * time.Second):
			t.Error("desktop failed to close")
		}
	}()
	wait := func(match func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			ok := false
			do(func() { ok = match() })
			if ok {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("batch state did not converge")
	}
	wait(func() bool { return v.ready && !v.busy })
	dir := t.TempDir()
	for _, name := range []string{"one.bin", "two.bin"} {
		do(func() { v.enqueue(downloadDraft{URL: server.URL, Destination: filepath.Join(dir, name), Later: true}) })
		wait(func() bool { return !v.busy })
	}
	do(func() { test.Tap(v.selectAll); test.Tap(v.resume) })
	wait(func() bool {
		one, e1 := os.Stat(filepath.Join(dir, "one.bin") + ".part")
		two, e2 := os.Stat(filepath.Join(dir, "two.bin") + ".part")
		return !v.busy && len(v.entries) == 2 && v.entries[0].State == "running" && v.entries[1].State == "running" && e1 == nil && e2 == nil && one.Size() > 0 && two.Size() > 0
	})
	do(func() { test.Tap(v.pause) })
	wait(func() bool { return !v.busy && v.entries[0].State == "paused" && v.entries[1].State == "paused" })
	for _, name := range []string{"one.bin", "two.bin"} {
		if _, err := os.Stat(filepath.Join(dir, name) + ".part"); err != nil {
			t.Fatal("pause lost partial", err)
		}
	}
	do(func() { test.Tap(v.resume) })
	wait(func() bool { return !v.busy && v.entries[0].State == "running" && v.entries[1].State == "running" })
	do(func() {
		if err := s.saveDownloadLimit("queue:1", 0); err != nil {
			t.Error(err)
		}
	})
	wait(func() bool { return v.entries[0].State == "completed" && v.entries[1].State == "running" })
	data, err := os.ReadFile(filepath.Join(dir, "one.bin"))
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("resumed batch output differs", err)
	}
	// Add a completed file, then change it while the deletion dialog is open.
	third := filepath.Join(dir, "third.bin")
	if err := os.WriteFile(third, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	var q *queueClient
	do(func() { q = v.client })
	unifiedRequest(t, q, desktopRequest{Operation: "adopt", Job: queueEntry{URL: server.URL, Destination: third, State: "completed"}})
	wait(func() bool { return len(v.entries) == 3 })
	do(func() { test.Tap(v.selectAll); v.confirmRemoval(v.selectedRows(), true) })
	if err := os.WriteFile(third, []byte("changed; keep this file"), 0600); err != nil {
		t.Fatal(err)
	}
	do(func() { test.Tap(batchDialogButton(t, w, "Delete files")) })
	wait(func() bool { return !v.busy && len(v.entries) == 1 && strings.Contains(v.status.Text, "1 failed") })
	for _, path := range []string{filepath.Join(dir, "one.bin"), filepath.Join(dir, "two.bin") + ".part"} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("batch deletion left data", path, err)
		}
	}
	if data, err := os.ReadFile(third); err != nil || string(data) != "changed; keep this file" {
		t.Fatal("changed file deleted", err)
	}
	do(func() {
		test.Tap(batchDialogButton(t, w, "Close"))
		test.Tap(v.selectAll)
		v.confirmRemoval(v.selectedRows(), false)
		test.Tap(batchDialogButton(t, w, "Remove entries"))
	})
	wait(func() bool { return !v.busy && len(v.entries) == 0 })
	if _, err := os.Stat(third); err != nil {
		t.Fatal("keep-files removal deleted output", err)
	}
	history, err := loadHistory()
	if err != nil {
		t.Fatal(err)
	}
	for _, h := range history {
		if !h.Archived {
			t.Fatal("removed entry was not archived")
		}
	}
	// A list-only removal must retain checkpoints without rediscovering the row.
	do(func() {
		if err := s.saveDownloadLimit("queue:4", 16); err != nil {
			t.Error(err)
		}
	})
	do(func() {
		v.enqueue(downloadDraft{URL: server.URL, Destination: filepath.Join(dir, "keep-part.bin"), Later: true})
	})
	wait(func() bool { return !v.busy && len(v.entries) == 1 })
	do(func() { test.Tap(v.selectAll); test.Tap(v.resume) })
	wait(func() bool {
		info, err := os.Stat(filepath.Join(dir, "keep-part.bin") + ".part")
		return !v.busy && v.entries[0].State == "running" && err == nil && info.Size() > 0
	})
	do(func() { v.confirmRemoval(v.selectedRows(), false); test.Tap(batchDialogButton(t, w, "Remove entries")) })
	wait(func() bool { return !v.busy && len(v.entries) == 0 })
	if _, err := os.Stat(filepath.Join(dir, "keep-part.bin") + ".part"); err != nil {
		t.Fatal("keep-files removal lost partial", err)
	}
	history, err = loadHistory()
	if err != nil {
		t.Fatal(err)
	}
	listing, err := exec.Command(binary, "-list").Output()
	if err != nil {
		t.Fatal(err)
	}
	discovered := mergeSavedJobs(history, string(listing))
	if len(downloadRows(nil, discovered, "All downloads")) != 0 {
		t.Fatal("removed saved download reappeared")
	}
}
