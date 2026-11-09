//go:build linux

package main

import (
	"bytes"
	"fmt"
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEachActiveDownloadReportsItsOwnSpeed(t *testing.T) {
	binary := unifiedCLI(t)
	for _, connections := range []int{1, 4} {
		t.Run(fmt.Sprintf("connections-%d", connections), func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			settings, err := loadSpeedSettings()
			if err != nil {
				t.Fatal(err)
			}
			settings.Overrides["queue:1"], settings.Overrides["queue:2"] = 64, 256
			if err := settings.save(); err != nil {
				t.Fatal(err)
			}
			payload := bytes.Repeat([]byte("speed"), 300000)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("ETag", `"speed-v1"`)
				http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
			}))
			defer server.Close()
			q, err := startConfiguredQueue(binary, 2, connections, 0, settings.path, func(string) {}, func(error) {})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = q.send("quit"); <-q.done }()
			unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
			dir := t.TempDir()
			for _, name := range []string{"slow", "fast"} {
				unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: filepath.Join(dir, name)}})
			}
			entries := unifiedWait(t, q, func(j []queueEntry) bool {
				return len(j) == 2 && j[0].State == "running" && j[1].State == "running" && j[0].Speed != nil && j[1].Speed != nil && *j[0].Speed > 0 && *j[1].Speed > *j[0].Speed*1.5
			})
			t.Logf("recent per-download rates: slow %.0f bytes/s, fast %.0f bytes/s", *entries[0].Speed, *entries[1].Speed)
			if err := q.send("pause 1"); err != nil {
				t.Fatal(err)
			}
			entries = unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "paused" })
			if entries[0].Speed != nil {
				t.Fatal("paused download retains active speed")
			}
		})
	}
}

func TestRemoveCustomLimitAppliesToRunningDownload(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	binary := unifiedCLI(t)
	settings, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	settings.Enabled = true
	settings.Default = 256
	settings.Overrides["queue:1"], settings.Overrides["queue:2"] = 8, 8
	if err := settings.save(); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("live-limit"), 60000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"limits-v1"`)
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	q, err := startConfiguredQueue(binary, 2, 1, 0, settings.path, func(string) {}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = q.send("quit"); <-q.done }()
	unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
	for _, name := range []string{"first", "second"} {
		unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL, Destination: filepath.Join(t.TempDir(), name)}})
	}
	entries := unifiedWait(t, q, func(j []queueEntry) bool {
		return len(j) == 2 && len(j[0].Segments) == 1 && len(j[1].Segments) == 1 && j[0].Segments[0].Downloaded >= 2048 && j[1].Segments[0].Downloaded >= 2048
	})
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("limits")
	defer w.Close()
	v := newDownloadsView(a, w, settings, nil, "")
	w.SetContent(v.content)
	v.ready = true
	v.entries = entries
	v.refresh()
	row := v.list.CreateItem().(*fyne.Container)
	v.list.UpdateItem(0, row)
	controls := row.Objects[0].(*fyne.Container).Objects[1].(*fyne.Container)
	remove := controls.Objects[2].(*widget.Button)
	if !remove.Visible() || remove.Disabled() {
		t.Fatal("running row has no usable Remove limit button")
	}
	test.Tap(remove)
	v.list.UpdateItem(0, row)
	if remove.Visible() || controls.Objects[1].(*widget.Button).Text != "Limit: default" {
		t.Fatal("row did not return to its default limit")
	}
	loaded, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Overrides["queue:1"] != 0 || loaded.Overrides["queue:2"] != 8 || !loaded.Enabled || loaded.Default != 256 {
		t.Fatal("removal changed the default or another download")
	}
	// With its old 8 KiB/s ceiling this download needs over a minute. The same
	// running worker must now complete within the normal ten-second wait.
	unifiedWait(t, q, func(j []queueEntry) bool { return j[0].State == "completed" && j[1].State == "running" })
	data, err := os.ReadFile(entries[0].Destination)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatalf("wrong completed file: %v", err)
	}
}
