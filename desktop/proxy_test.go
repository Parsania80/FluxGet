package main

import (
	"bytes"
	"image/png"
	"os"
	"testing"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/Parsania80/FluxGet/networkproxy"
)

func TestProxySettingsPersistAndDefaultDisabled(t *testing.T) {
	setTestConfig(t, t.TempDir())
	s, err := loadSpeedSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.Proxy.Enabled || s.Proxy.URL != "" {
		t.Fatal("proxy unexpectedly enabled by default")
	}
	s.Proxy = networkproxy.Config{Enabled: true, URL: "socks5://127.0.0.1:10808"}
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	s, err = loadSpeedSettings()
	if err != nil || !s.Proxy.Enabled || s.Proxy.URL != "socks5h://127.0.0.1:10808" {
		t.Fatal("saved proxy lost", err)
	}
	s.Proxy.Enabled = false
	if err := s.save(); err != nil {
		t.Fatal(err)
	}
	s, err = loadSpeedSettings()
	if err != nil || s.Proxy.Enabled || s.Proxy.URL == "" {
		t.Fatal("disable lost saved address", err)
	}
	if route, err := proxyForDownload(s.Proxy, true); err != nil || route == nil || !route.Enabled {
		t.Fatal("one-off proxy cannot use disabled default", err)
	}
	if route, err := proxyForDownload(networkproxy.Config{URL: "unfinished edit"}, false); err != nil || route != nil {
		t.Fatal("Direct depends on fixing address", err)
	}
	validFile, _ := os.ReadFile(s.path)
	s.Proxy = networkproxy.Config{Enabled: true, URL: "http://user:private-token@host:80"}
	if err := s.save(); err == nil {
		t.Fatal("saved rejected proxy credentials")
	}
	stillValid, _ := os.ReadFile(s.path)
	if !bytes.Equal(stillValid, validFile) {
		t.Fatal("invalid proxy replaced previous settings")
	}
}

func TestProxySettingsToggleAndRecycledRowButton(t *testing.T) {
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("Proxy settings")
	defer w.Close()
	current := networkproxy.Config{Enabled: true, URL: "http://127.0.0.1:10809"}
	fields, read := proxySettingsFields(current)
	w.SetContent(fields)
	w.Resize(fyne.NewSize(680, 320))
	w.Show()
	var toggle *widget.Check
	var address *widget.Entry
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch o := object.(type) {
		case *fyne.Container:
			for _, child := range o.Objects {
				walk(child)
			}
		case *widget.Form:
			for _, item := range o.Items {
				walk(item.Widget)
			}
		case *widget.Check:
			toggle = o
		case *widget.Entry:
			address = o
		}
	}
	walk(fields)
	if toggle == nil || address == nil {
		t.Fatal("missing proxy controls")
	}
	// The page remains open after a successful save with a different endpoint.
	current.URL = "socks5h://127.0.0.1:10808"
	address.SetText(current.URL)
	if saved, err := read(current); err != nil || saved != current {
		t.Fatal("cannot save updated proxy address", err)
	}
	address.SetText("unfinished edit")
	test.Tap(toggle)
	config, err := read(current)
	if err != nil || config.Enabled || config.URL != current.URL {
		t.Fatal("cannot disable proxy without fixing draft address", err)
	}
	if screenshot := os.Getenv("FLUXGET_PROXY_SCREENSHOT"); screenshot != "" {
		file, err := os.Create(screenshot)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, w.Canvas().Capture())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	button := widget.NewButton("", nil)
	selected := 0
	bindProxyButton(button, queueEntry{ID: 1, State: "paused", Proxy: &networkproxy.Config{Enabled: true, URL: "http://127.0.0.1:10809"}}, func() { selected = 1 })
	if button.Text != "Proxy" {
		t.Fatal("proxy state not visible")
	}
	test.Tap(button)
	bindProxyButton(button, queueEntry{ID: 2, State: "paused"}, func() { selected = 2 })
	test.Tap(button)
	if button.Text != "Direct" || selected != 2 {
		t.Fatal("row retained another download's route or callback")
	}
}
