package main

import (
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"testing"
)

func TestSpeedSettingsPersist(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	s.Enabled = true
	s.Default = 512
	s.Overrides["queue:1"] = 128
	if err = s.save(); err != nil {
		t.Fatal(err)
	}
	got, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Enabled || got.Default != 512 || got.Overrides["queue:1"] != 128 {
		t.Fatalf("unexpected settings: %+v", got)
	}
}

func TestParseSpeed(t *testing.T) {
	for _, tc := range []struct {
		input              string
		allowZero, wantErr bool
	}{{"512", false, false}, {"0", true, false}, {"0", false, true}, {"-1", true, true}, {"abc", true, true}, {"1073741825", true, true}} {
		_, err := parseSpeed(tc.input, tc.allowZero)
		if (err != nil) != tc.wantErr {
			t.Errorf("parseSpeed(%q) error %v", tc.input, err)
		}
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
	if button.Text != "Limit: 256 KiB/s" {
		t.Fatalf("unexpected label %q", button.Text)
	}
	test.Tap(button)
	if gotKey != "download:1" || gotName != "first.zip" {
		t.Fatalf("unexpected target %q %q", gotKey, gotName)
	}
	bindLimitButton(button, settings, "queue:9", "second.zip", edit)
	test.Tap(button)
	if gotKey != "queue:9" || gotName != "second.zip" || button.Text != "Limit: 512 KiB/s" {
		t.Fatalf("reused row has wrong target or label: %q %q %q", gotKey, gotName, button.Text)
	}
	bindLimitButton(button, settings, "queue:10", "third.zip", edit)
	if button.Text != "Limit: default" {
		t.Fatalf("unexpected default label %q", button.Text)
	}
}
