package main

import (
	"encoding/json"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func TestDesktopWorkspacesKeepActionsVisibleAndDraftsIntact(t *testing.T) {
	for _, screen := range []struct {
		name  string
		size  fyne.Size
		theme fyne.Theme
	}{
		{"dark", fyne.NewSize(1100, 720), theme.DarkTheme()},
		{"small", fyne.NewSize(900, 600), theme.DarkTheme()},
		{"light", fyne.NewSize(1100, 720), theme.LightTheme()},
	} {
		t.Run(screen.name, func(t *testing.T) {
			setTestConfig(t, t.TempDir())
			a := test.NewApp()
			a.Settings().SetTheme(fluxTheme{screen.theme})
			defer a.Quit()
			w := a.NewWindow("FluxGet")
			defer w.Close()
			s, err := loadSpeedSettings()
			if err != nil {
				t.Fatal(err)
			}
			v := newDownloadsView(a, w, s, nil, "/fixture/fluxget")
			w.SetContent(v.content)
			w.Show()
			v.ready = true
			v.status.SetText("Ready. Add a download, or resume a saved one.")
			v.refresh()
			var tabs, sections *container.AppTabs
			var save, browserSave *widget.Button
			var unit *widget.Select
			var limit *widget.Entry
			var limitEnabled *widget.Check
			var walk func(fyne.CanvasObject)
			walk = func(object fyne.CanvasObject) {
				switch o := object.(type) {
				case *fyne.Container:
					for _, child := range o.Objects {
						walk(child)
					}
				case *container.AppTabs:
					if o.Items[0].Text == "Downloads" {
						tabs = o
					} else if o.Items[0].Text == "General" {
						sections = o
					}
					for _, item := range o.Items {
						walk(item.Content)
					}
				case *container.Scroll:
					walk(o.Content)
				case *widget.Card:
					walk(o.Content)
				case *widget.Form:
					for _, item := range o.Items {
						if strings.HasPrefix(item.Text, "Per download (") {
							limit = item.Widget.(*widget.Entry)
						}
						walk(item.Widget)
					}
				case *widget.Check:
					if o.Text == "Limit the speed of each download" {
						limitEnabled = o
					}
				case *widget.Select:
					if len(o.Options) == 3 && o.Options[0] == "KB" {
						unit = o
					}
				case *widget.Button:
					if o.Text == "Save settings" {
						save = o
					} else if o.Text == "Save browser settings" {
						browserSave = o
					}
				}
			}
			walk(v.content)
			if tabs == nil || sections == nil || len(sections.Items) != 4 || save == nil || browserSave == nil || unit == nil {
				t.Fatal("settings sections or their save controls are missing")
			}
			if limit == nil || limitEnabled == nil || !limit.Disabled() {
				t.Fatal("unused per-download speed input is active")
			}
			test.Tap(limitEnabled)
			if limit.Disabled() {
				t.Fatal("per-download speed input cannot be enabled")
			}
			test.Tap(limitEnabled)
			before, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			policy, _ := json.Marshal(v.browsers.store.Snapshot())
			unit.SetSelected("GB")
			capture := func(name string) {
				if directory := os.Getenv("FLUXGET_WORKSPACE_SCREENSHOTS"); directory != "" {
					f, err := os.Create(filepath.Join(directory, "ui-"+name+".png"))
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

			w.Resize(screen.size)
			if w.Canvas().Size() != screen.size {
				t.Fatalf("workspace minimum size exceeds %v: %v", screen.size, w.Canvas().Size())
			}
			tabs.SelectIndex(2)
			for i, name := range []string{"general", "connections", "proxy", "browser"} {
				sections.SelectIndex(i)
				button := save
				if i == 3 {
					button = browserSave
				}
				assertControlsVisible(t, w, button)
				w.Canvas().Focus(button)
				if w.Canvas().Focused() != button {
					t.Fatal("save control cannot receive keyboard focus")
				}
				capture("settings-" + name + "-" + screen.name)
			}
			w.Resize(fyne.NewSize(900, 600))
			sections.SelectIndex(0)
			assertControlsVisible(t, w, save)
			w.Resize(screen.size)
			tabs.SelectIndex(0)
			assertControlsVisible(t, w, v.add, v.selectAll, v.clearSelection, v.remove)
			if v.list.Visible() || !v.empty.Visible() || v.selectionActions.Visible() || !v.selectionHint.Visible() {
				t.Fatal("empty Downloads view shows transfer actions")
			}
			capture("downloads-empty-" + screen.name)
			v.entries = []queueEntry{{ID: 1, State: "paused", Destination: "/tmp/ubuntu-desktop-amd64-a-long-filename-for-review.iso"}, {ID: 2, State: "completed", Destination: "/tmp/guide.pdf"}}
			v.refresh()
			v.list.Select(0)
			if !v.selectionActions.Visible() || v.selectionHint.Visible() || v.resume.Disabled() {
				t.Fatal("selected download has no contextual actions")
			}
			assertControlsVisible(t, w, v.add, v.pause, v.resume, v.retry, v.open, v.folder, v.more)
			row := v.list.CreateItem().(*fyne.Container)
			row.Resize(fyne.NewSize(v.list.Size().Width, row.MinSize().Height))
			v.list.UpdateItem(0, row)
			filename := row.Objects[0].(*fyne.Container).Objects[0].(*widget.Label)
			if filename.Size().Width < v.list.Size().Width-16 {
				t.Fatal("row controls squeeze the filename", filename.Size(), v.list.Size())
			}
			capture("downloads-selected-" + screen.name)
			v.filters.SetSelected("Failed")
			if !v.empty.Visible() || v.emptyTitle.Text != "No downloads in this view" || v.selectionActions.Visible() {
				t.Fatal("empty filter retained a selection or its actions")
			}
			v.entries = nil
			v.filters.SetSelected("All downloads")
			after, err := os.ReadFile(s.path)
			afterPolicy, _ := json.Marshal(v.browsers.store.Snapshot())
			if err != nil || string(after) != string(before) || string(afterPolicy) != string(policy) || s.DisplayUnit != "MB" || unit.Selected != "GB" {
				t.Fatal("section navigation saved settings, changed browser consent or discarded the unsaved draft")
			}

		})
	}
}
