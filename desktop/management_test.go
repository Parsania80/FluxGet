package main

import (
	"encoding/json"
	"image/png"
	"os"
	"strings"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestManagementDefaultsAutomaticAndPreservesExplicitManual(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.requestPolicy() != (requestPolicy{Mode: "automatic", Total: 8, PerOrigin: 4}) {
		t.Fatalf("wrong default: %+v", s)
	}
	if err := os.WriteFile(s.path, []byte(`{"max_active":3,"connections":2,"overrides":{"queue:1":128}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = loadSpeedSettings()
	if err != nil || s.ManagementMode != "automatic" || s.MaxActive != 3 || s.Connections != 2 || s.Overrides["queue:1"] != 128 {
		t.Fatalf("legacy migration: %+v %v", s, err)
	}
	s.ManagementMode = "manual"
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	s, err = loadSpeedSettings()
	if err != nil || s.ManagementMode != "manual" {
		t.Fatal("explicit Manual choice was lost")
	}
	for _, input := range []string{`{"management_mode":"broken"}`, `{"total_requests":65}`, `{"total_requests":0}`, `{"origin_requests":0}`, `{"origin_requests":17}`} {
		if err := os.WriteFile(s.path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSpeedSettings(); err == nil {
			t.Fatalf("invalid management accepted: %s", input)
		}
	}
}

func TestManagementControlsCollapsedAndWaitForAcknowledgment(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("management")
	defer w.Close()
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	var pending func(error)
	var submitted speedSettings
	applyCalls := 0
	s.applyDownloads = func(next speedSettings, done func(error)) { applyCalls++; submitted = next; pending = done }
	page := s.page(w, nil, func() bool { return true }, nil)
	w.SetContent(page)
	w.Resize(fyne.NewSize(1100, 720))
	w.Show()
	if screenshot := os.Getenv("FLUXGET_AUTO_SETTINGS_SCREENSHOT"); screenshot != "" {
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

	var accordion *widget.Accordion
	var mode *widget.Select
	var save *widget.Button
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch o := object.(type) {
		case *container.AppTabs:
			for _, item := range o.Items {
				walk(item.Content)
			}
		case *widget.Card:
			walk(o.Content)
		case *container.Scroll:
			walk(o.Content)
		case *fyne.Container:
			for _, child := range o.Objects {
				walk(child)
			}
		case *widget.Accordion:
			accordion = o
			for _, item := range o.Items {
				walk(item.Detail)
			}
		case *widget.Form:
			for _, item := range o.Items {
				walk(item.Widget)
			}
		case *widget.Select:
			if len(o.Options) == 2 && o.Options[0] == "Automatic" {
				mode = o
			}
		case *widget.Button:
			if o.Text == "Save settings" {
				save = o
			}
		}
	}
	walk(page)
	if accordion == nil || mode == nil || save == nil || accordion.Items[0].Open || mode.Selected != "Automatic" {
		t.Fatal("default requires manual setup or exposes all controls")
	}
	mode.SetSelected("Manual")
	test.Tap(save)
	test.Tap(save)
	if pending == nil || submitted.ManagementMode != "manual" || applyCalls != 1 || !save.Disabled() {
		t.Fatal("configuration was not submitted")
	}
	if s.ManagementMode != "automatic" {
		t.Fatal("reported saved before scheduler acknowledgment")
	}
	data, _ := os.ReadFile(s.path)
	if string(data) != string(original) {
		t.Fatal("persisted policy before scheduler acknowledgment")
	}
	pending(os.ErrPermission)
	if s.ManagementMode != "automatic" || save.Disabled() {
		t.Fatal("rejected configuration changed settings")
	}
	test.Tap(save)
	if err := submitted.save(); err != nil {
		t.Fatal(err)
	} // Simulate successful acknowledged transaction.
	pending(nil)
	loaded, err := loadSpeedSettings()
	if err != nil || loaded.ManagementMode != "manual" || s.ManagementMode != "manual" {
		t.Fatal("acknowledged Manual choice did not persist")
	}
	// Management configuration is explicit; private callbacks are never JSON.
	if _, err := json.Marshal(s); err != nil {
		t.Fatal(err)
	}
}

func TestRequestWaitingRowsHaveHonestFiltersAndActions(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("requests")
	defer w.Close()
	s := &speedSettings{MaxActive: 5, Connections: 4, Overrides: map[string]int64{}}
	v := newDownloadsView(a, w, s, nil, "")
	w.SetContent(v.content)
	v.ready = true
	v.entries = []queueEntry{{ID: 1, State: "running", Destination: "/tmp/waiting", Requests: &jobBudgetSnapshot{Waiting: 4}}, {ID: 2, State: "running", Destination: "/tmp/active", Requests: &jobBudgetSnapshot{Active: 1}}}
	if len(downloadRows(v.entries, nil, "Waiting")) != 1 || len(downloadRows(v.entries, nil, "Downloading")) != 1 {
		t.Fatal("request wait was classified as active downloading")
	}
	v.category = "Waiting"
	v.refresh()
	v.list.Select(0)
	if v.pause.Disabled() {
		t.Fatal("request-waiting job cannot be paused")
	}
}

func TestAdaptiveStatusReasonsAndActivities(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("Automatic")
	defer w.Close()
	v := newDownloadsView(a, w, &speedSettings{MaxActive: 5, Connections: 4, Overrides: map[string]int64{}}, nil, "")
	w.SetContent(v.content)
	v.management = &budgetSnapshot{Policy: requestPolicy{Mode: "automatic"}, FileTarget: 1, Draining: true, Status: "Stable", Reason: "Additional download did not improve total speed"}
	v.entries = []queueEntry{{ID: 1, State: "running", Activity: "Verifying saved data"}, {ID: 2, State: "running", Activity: "Checking transfer method"}, {ID: 3, State: "running", Activity: "Waiting for retry"}, {ID: 4, State: "queued", AutomaticReason: "Waiting for the server cooldown; retry manually if requested"}}
	v.refresh()
	if !strings.Contains(v.summary.Text, "target 1 files") || !strings.Contains(v.summary.Text, "Letting active downloads finish") || !strings.Contains(v.managementNote.Text, v.management.Reason) {
		t.Fatal("adaptive summary missing reason/draining", v.summary.Text)
	}
	if len(downloadRows(v.entries, nil, "Downloading")) != 0 || len(downloadRows(v.entries, nil, "Waiting")) != 2 {
		t.Fatal("non-transfer work labeled downloading")
	}
	if !strings.Contains(downloadDetails(v.entries[3], "MB"), "server cooldown") {
		t.Fatal("queued cooldown hidden")
	}
}

func TestHardwareObservationsStayRuntimeOnlyAndRender(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("Hardware observations")
	defer w.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	page := s.page(w, nil, func() bool { return true }, nil)
	w.SetContent(page)
	w.Resize(fyne.NewSize(1100, 850))
	cpu, rss := 42.0, uint64(64<<20)
	state := &budgetSnapshot{Hardware: &hardwareSnapshot{CPUPercent: &cpu, RSS: &rss, CPUState: "Measured", MemoryState: "Measured"}, Buffers: &bufferSnapshot{Transfer: 65536, Hash: 65536, MaximumTransfer: 128 << 20, MaximumHash: 32 << 20}}
	text := hardwareDescription(state)
	if !strings.Contains(text, "42% of available capacity") || !strings.Contains(text, "Engine memory: 67.1 MB") || !strings.Contains(text, "Hash buffers") {
		t.Fatal(text)
	}
	if text := hardwareDescription(&budgetSnapshot{Hardware: &hardwareSnapshot{CPUState: "Unknown", MemoryState: "Unknown"}}); strings.Contains(text, "0%") || !strings.Contains(text, "Unknown") {
		t.Fatal(text)
	}
	s.hardwareNote.SetText(text)
	data, err := json.Marshal(s)
	if err != nil || strings.Contains(string(data), "hardware") || strings.Contains(string(data), "RSS") || strings.Contains(string(data), "CPU") {
		t.Fatalf("runtime data persisted: %s %v", data, err)
	}
	if path := os.Getenv("FLUXGET_HARDWARE_SCREENSHOT"); path != "" {
		var open func(fyne.CanvasObject)
		open = func(o fyne.CanvasObject) {
			switch o := o.(type) {
			case *container.AppTabs:
				o.SelectIndex(1)
				open(o.Items[1].Content)
			case *widget.Card:
				open(o.Content)
			case *container.Scroll:
				open(o.Content)
			case *fyne.Container:
				for _, child := range o.Objects {
					open(child)
				}
			case *widget.Accordion:
				o.Open(0)
			}
		}
		open(page)
		page.Refresh()
		w.Resize(fyne.NewSize(1100, 1000))
		w.Show()
		f, err := os.Create(path)
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
