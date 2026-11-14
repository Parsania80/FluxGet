package main

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

func TestDisplayUnitsFormatSizesAndSpeeds(t *testing.T) {
	speed := 2500000.0
	for _, tc := range []struct{ unit, progress, speed string }{
		{"KB", "Downloaded 12500.00 / 100000.00 KB (12.5%)", "Speed: 2500.00 KB/s"},
		{"MB", "Downloaded 12.50 / 100.00 MB (12.5%)", "Speed: 2.50 MB/s"},
		{"GB", "Downloaded 0.01 / 0.10 GB (12.5%)", "Speed: 0.0025 GB/s"},
	} {
		if got := downloadProgress("Downloaded 12500000/100000000 bytes (12.5%), 9999 bytes/s", tc.unit); got != tc.progress {
			t.Errorf("%s progress: %q", tc.unit, got)
		}
		if got := downloadSpeed(queueEntry{State: "running", Speed: &speed}, tc.unit); got != tc.speed {
			t.Errorf("%s speed: %q", tc.unit, got)
		}
	}
	if got := downloadProgress("Downloaded 1500000 bytes, 12345 bytes/s", "MB"); got != "Downloaded 1.50 MB · total size unknown" {
		t.Errorf("unknown total: %q", got)
	}
	if got := downloadProgress("Downloaded 1/1000000000 bytes", "GB"); got != "Downloaded 0.000000001 / 1.00 GB (0.0%)" {
		t.Errorf("tiny size disappeared: %q", got)
	}
}

func TestDisplayUnitMigrationAndValidation(t *testing.T) {
	setTestConfig(t, t.TempDir())
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.DisplayUnit != "MB" {
		t.Fatal("new settings did not default to MB")
	}
	legacy := []byte(`{"enabled":true,"default_kib":512,"overrides":{"queue:1":128}}`)
	if err := os.WriteFile(s.path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	s, err = loadSpeedSettings()
	if err != nil || s.DisplayUnit != "MB" || s.Appearance != appearanceSystem || s.Default != 512 || s.Overrides["queue:1"] != 128 {
		t.Fatalf("legacy settings: %+v %v", s, err)
	}
	if err := os.WriteFile(s.path, []byte(`{"display_unit":"TB"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSpeedSettings(); err == nil {
		t.Fatal("unsupported unit accepted")
	}
}

func TestUnitChangesPreserveStoredLimitsExactly(t *testing.T) {
	for _, unit := range []string{"KB", "MB", "GB"} {
		for _, kib := range []int64{0, 1, 128, 512, 8193, 1048576, 1 << 30} {
			text := limitText(kib, unit)
			got, err := parseDisplayLimit(text, unit, true)
			if err != nil || got != kib {
				t.Errorf("%d KiB/s through %s %q changed to %d: %v", kib, unit, text, got, err)
			}
		}
	}
	got, err := parseDisplayLimit("0.5", "MB", false)
	if err != nil || got != 488 {
		t.Fatalf("decimal limit did not round down safely: %d %v", got, err)
	}
	for _, text := range []string{"NaN", "Inf", "-1", "0.0000000000000001", "1e-999999999", "1/2", "99999999999999999999"} {
		if _, err := parseDisplayLimit(text, "MB", true); err == nil {
			t.Errorf("invalid limit %q accepted", text)
		}
	}
}

func TestCommonLimitValuesReopenWithoutStorageDecimals(t *testing.T) {
	for _, tc := range []struct{ text, unit string }{
		{"1", "MB"}, {"0.5", "MB"}, {"0.1", "MB"}, {"1.5", "MB"}, {"2.25", "MB"},
		{"10", "MB"}, {"100", "MB"}, {"128", "KB"}, {"512", "KB"}, {"1", "GB"},
	} {
		t.Run(tc.text+tc.unit, func(t *testing.T) {
			stored, err := parseDisplayLimit(tc.text, tc.unit, false)
			if err != nil {
				t.Fatal(err)
			}
			for range 3 {
				text := limitText(stored, tc.unit)
				if text != tc.text {
					t.Fatalf("entered %s, reopened as %s", tc.text, text)
				}
				next, err := parseDisplayLimit(text, tc.unit, false)
				if err != nil || next != stored {
					t.Fatalf("reopening changed the engine limit: %d to %d: %v", stored, next, err)
				}
			}
		})
	}
}

func TestSettingsUnitSelectorPersistsWithoutChangingLimits(t *testing.T) {
	setTestConfig(t, t.TempDir())
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("Settings")
	defer w.Close()
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Default = 512
	s.Overall = 2048
	s.Enabled = true
	s.Overrides["queue:1"] = 128
	saved := 0
	page := s.page(w, func() { saved++ }, func() bool { return true }, nil)
	w.SetContent(page)
	var selectUnit *widget.Select
	var selectAppearance *widget.Select
	var sortFiles *widget.Check
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
		case *widget.Form:
			for _, item := range o.Items {
				if item.Text == "Appearance" {
					selectAppearance, _ = item.Widget.(*widget.Select)
				}
				walk(item.Widget)
			}
		case *widget.Select:
			if len(o.Options) == 3 && o.Options[0] == "KB" {
				selectUnit = o
			}
		case *widget.Check:
			if o.Text == "Sort new downloads into file-type folders" {
				sortFiles = o
			}
		case *widget.Button:
			if o.Text == "Save settings" {
				save = o
			}
		}
	}
	walk(page)
	if selectUnit == nil || save == nil || selectUnit.Selected != "MB" || sortFiles == nil || !sortFiles.Checked {
		t.Fatal("missing default MB unit selector")
	}
	if selectAppearance == nil || selectAppearance.Selected != appearanceSystem {
		t.Fatal("missing Follow system default")
	}
	for i, unit := range []string{"GB", "KB", "MB"} {
		appearance := []string{appearanceDark, appearanceLight, appearanceSystem}[i]
		selectUnit.SetSelected(unit)
		selectAppearance.SetSelected(appearance)
		test.Tap(sortFiles)
		test.Tap(save)
		loaded, err := loadSpeedSettings()
		if err != nil || loaded.SortFiles != sortFiles.Checked || loaded.DisplayUnit != unit || loaded.Appearance != appearance || loaded.Default != 512 || loaded.Overall != 2048 || loaded.Overrides["queue:1"] != 128 {
			t.Fatalf("saving %s changed limits: %+v %v", unit, loaded, err)
		}
	}
	if saved != 3 {
		t.Fatal("display refresh callback not triggered")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(s.path), "desktop-speed.json")); err != nil {
		t.Fatal(err)
	}
}

func TestAppearancePalette(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	base := theme.DefaultTheme()
	for _, tc := range []struct {
		appearance string
		system     fyne.ThemeVariant
		want       fyne.ThemeVariant
	}{
		{appearanceLight, theme.VariantDark, theme.VariantLight},
		{appearanceDark, theme.VariantLight, theme.VariantDark},
		{appearanceSystem, theme.VariantLight, theme.VariantLight},
		{appearanceSystem, theme.VariantDark, theme.VariantDark},
	} {
		applyAppearance(a, tc.appearance)
		for _, role := range []fyne.ThemeColorName{theme.ColorNameBackground, theme.ColorNameForeground, theme.ColorNameInputBackground} {
			if got, want := a.Settings().Theme().Color(role, tc.system), base.Color(role, tc.want); got != want {
				t.Errorf("%s on system variant %d uses %v for %s, want %v", tc.appearance, tc.system, got, role, want)
			}
		}
	}
}
