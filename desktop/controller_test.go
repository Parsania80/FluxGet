//go:build linux

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
)

// Serialize test-driver callbacks just as the real Fyne event thread does.
// The stock test driver executes background callbacks on their calling goroutine.
type serialTestDriver struct {
	fyne.Driver
	events chan func()
	stop   chan struct{}
	once   sync.Once
}

func (d *serialTestDriver) DoFromGoroutine(fn func(), wait bool) {
	done := make(chan struct{})
	select {
	case d.events <- func() { fn(); close(done) }:
	case <-d.stop:
		return
	}
	if wait {
		select {
		case <-done:
		case <-d.stop:
		}
	}
}

type serialTestApp struct {
	fyne.App
	driver fyne.Driver
}

func (a serialTestApp) Driver() fyne.Driver { return a.driver }

func TestUnifiedControllerAddsFromFiltersAndCompletesReplacement(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	payload := []byte("new downloaded content")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(payload) }))
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
	settings, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	var v *downloadsView
	var w fyne.Window
	driver.DoFromGoroutine(func() {
		w = a.NewWindow("FluxGet")
		v = newDownloadsView(a, w, settings, nil, binary)
		w.SetContent(v.content)
		w.Show()
		v.connect()
	}, true)
	closed := make(chan struct{})
	defer func() {
		driver.DoFromGoroutine(func() { v.close(func() { w.Close(); close(closed) }) }, true)
		select {
		case <-closed:
		case <-time.After(10 * time.Second):
			t.Error("desktop failed to close safely")
		}
	}()
	wait := func(match func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for time.Now().Before(deadline) {
			var ok bool
			driver.DoFromGoroutine(func() { ok = match() }, true)
			if ok {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatal("desktop state did not converge")
	}
	wait(func() bool { return v.ready && !v.busy })
	dir := t.TempDir()
	original := filepath.Join(dir, "original.txt")
	if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	identity, err := replacementFor(original)
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged.txt")
	driver.DoFromGoroutine(func() {
		v.filters.SetSelected("Completed")
		v.enqueue(downloadDraft{URL: server.URL, Destination: staged, Replace: identity})
	}, true)
	wait(func() bool {
		return len(v.entries) == 1 && v.entries[0].State == "completed" && v.entries[0].OutputPath == original && !v.busy && len(v.finishing) == 0
	})
	data, err := os.ReadFile(original)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("replacement output: %q %v", data, err)
	}
	history, err := loadHistory()
	if err != nil || len(history) != 1 || history[0].Path != original || history[0].Replace != nil {
		t.Fatalf("replacement history: %+v %v", history, err)
	}
	paused := filepath.Join(dir, "later.txt")
	driver.DoFromGoroutine(func() {
		v.filters.SetSelected("Completed")
		v.enqueue(downloadDraft{URL: server.URL, Destination: paused, Later: true})
	}, true)
	wait(func() bool { return len(v.entries) == 2 && v.entries[1].State == "paused" && !v.busy })
	driver.DoFromGoroutine(func() {
		if v.category != "All downloads" {
			t.Error("new download stayed hidden by old filter")
		}
		v.accept(desktopResponse{Revision: v.revision - 1, Jobs: nil})
		if len(v.entries) != 2 {
			t.Error("stale snapshot removed current downloads")
		}
	}, true)
	if _, err := os.Stat(paused); !os.IsNotExist(err) {
		t.Fatal("Download later created a file")
	}
}
