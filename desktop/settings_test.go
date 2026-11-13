package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestSpeedSettingsPersist(t *testing.T) {
	setTestConfig(t, t.TempDir())
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !s.SortFiles {
		t.Fatal("new settings do not enable file-type folders")
	}
	s.SortFiles = false
	s.Enabled = true
	s.Default = 512
	s.MaxActive = 3
	s.Connections = 2
	s.Overall = 2048
	s.DisplayUnit = "GB"
	s.Overrides["queue:1"] = 128
	if err = s.save(); err != nil {
		t.Fatal(err)
	}
	got, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.SortFiles || !got.Enabled || got.Default != 512 || got.Overrides["queue:1"] != 128 || got.MaxActive != 3 || got.Connections != 2 || got.Overall != 2048 || got.DisplayUnit != "GB" {
		t.Fatalf("unexpected settings: %+v", got)
	}
}

func TestLimitButtonTracksReusedRow(t *testing.T) {
	app := test.NewApp()
	defer app.Quit()
	settings := &speedSettings{Overrides: map[string]int64{"download:1": 256, "queue:9": 512}}
	button := widget.NewButton("", nil)
	var gotKey, gotName string
	edit := func(key, filename string) { gotKey, gotName = key, filename }
	bindLimitButton(button, settings, "download:1", "first.zip", edit)
	if button.Text != "Limit: 0.26 MB/s" {
		t.Fatalf("unexpected label %q", button.Text)
	}
	test.Tap(button)
	if gotKey != "download:1" || gotName != "first.zip" {
		t.Fatalf("unexpected target %q %q", gotKey, gotName)
	}
	bindLimitButton(button, settings, "queue:9", "second.zip", edit)
	test.Tap(button)
	if gotKey != "queue:9" || gotName != "second.zip" || button.Text != "Limit: 0.52 MB/s" {
		t.Fatalf("reused row has wrong target or label: %q %q %q", gotKey, gotName, button.Text)
	}
	bindLimitButton(button, settings, "queue:10", "third.zip", edit)
	if button.Text != "Limit: default" {
		t.Fatalf("unexpected default label %q", button.Text)
	}
}

func TestRemoveLimitButtonTracksReusedRow(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	s := &speedSettings{Overrides: map[string]int64{"queue:1": 128, "queue:2": 256}}
	button := widget.NewButton("Remove limit", nil)
	removed := ""
	remove := func(key string) { removed = key }
	bindRemoveLimitButton(button, s, "queue:1", remove)
	if !button.Visible() {
		t.Fatal("custom limit has no visible removal action")
	}
	test.Tap(button)
	if removed != "queue:1" {
		t.Fatal("wrong first target")
	}
	bindRemoveLimitButton(button, s, "queue:2", remove)
	test.Tap(button)
	if removed != "queue:2" {
		t.Fatal("recycled removal button retained previous target")
	}
	bindRemoveLimitButton(button, s, "queue:3", remove)
	if button.Visible() {
		t.Fatal("removal offered without a custom limit")
	}
}

func TestCustomLimitTogglePersistsWithoutMagicZero(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("limits")
	defer w.Close()
	w.Resize(fyne.NewSize(700, 540))
	w.Show()
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Enabled = true
	s.Default = 512
	s.Overall = 2048
	s.Overrides["queue:1"] = 128
	s.Overrides["queue:2"] = 256
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	saves := 0
	inspect := func() (*widget.Check, *widget.Entry, *widget.Form) {
		var enabled *widget.Check
		var entry *widget.Entry
		var form *widget.Form
		var walk func(fyne.CanvasObject)
		walk = func(object fyne.CanvasObject) {
			switch o := object.(type) {
			case *widget.PopUp:
				walk(o.Content)
			case *fyne.Container:
				for _, child := range o.Objects {
					walk(child)
				}
			case *widget.Form:
				form = o
				for _, item := range o.Items {
					walk(item.Widget)
				}
			case *widget.Check:
				enabled = o
			case *widget.Entry:
				entry = o
			case fyne.Widget:
				for _, child := range test.WidgetRenderer(o).Objects() {
					walk(child)
				}
			}
		}
		walk(w.Canvas().Overlays().Top())
		if enabled == nil || entry == nil || form == nil {
			t.Fatal("missing custom-limit controls")
		}
		return enabled, entry, form
	}
	s.editDownload(w, "queue:1", "file.zip", func() { saves++ })
	enabled, entry, form := inspect()
	if !enabled.Checked || entry.Disabled() {
		t.Fatal("existing custom limit is not enabled")
	}
	if screenshot := os.Getenv("FLUXGET_LIMIT_DIALOG_SCREENSHOT"); screenshot != "" {
		f, err := os.Create(screenshot)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(f, w.Canvas().Capture())
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	test.Tap(enabled)
	if !entry.Disabled() {
		t.Fatal("turning off custom limit left numeric input enabled")
	}
	entry.SetText("not a number")
	form.OnSubmit()
	loaded, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Overrides["queue:1"] != 0 || loaded.Overrides["queue:2"] != 256 || !loaded.Enabled || loaded.Default != 512 || loaded.Overall != 2048 || saves != 1 {
		t.Fatalf("turning off changed unrelated limits: %+v saves=%d", loaded, saves)
	}
	if w.Canvas().Overlays().Top() != nil {
		t.Fatal("successful Apply did not dismiss the editor")
	}
	s.editDownload(w, "queue:1", "file.zip", func() { saves++ })
	enabled, entry, form = inspect()
	if enabled.Checked || !entry.Disabled() {
		t.Fatal("removed limit reopened as enabled")
	}
	test.Tap(enabled)
	entry.SetText("0.5")
	form.OnSubmit()
	loaded, err = loadSpeedSettings()
	if err != nil || loaded.Overrides["queue:1"] != 488 || saves != 2 {
		t.Fatalf("could not re-enable custom limit: %+v %v", loaded, err)
	}
	s.editDownload(w, "queue:1", "file.zip", func() { saves++ })
	enabled, _, form = inspect()
	test.Tap(enabled)
	form.OnCancel()
	loaded, err = loadSpeedSettings()
	if err != nil || loaded.Overrides["queue:1"] != 488 || saves != 2 {
		t.Fatalf("Cancel changed the custom limit: %+v %v", loaded, err)
	}
}

func TestRemoveLimitSaveFailureRestoresOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "blocked")
	if err := os.WriteFile(path, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	s := &speedSettings{path: filepath.Join(path, "settings.json"), Overrides: map[string]int64{"queue:1": 128}}
	if err := s.saveDownloadLimit("queue:1", 0); err == nil {
		t.Fatal("expected settings write failure")
	}
	if s.Overrides["queue:1"] != 128 {
		t.Fatal("failed save removed the in-memory limit")
	}
}
