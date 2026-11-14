package main

import (
	"os"
	"path/filepath"
	"testing"

	"fyne.io/fyne/v2/test"
	"github.com/Parsania80/FluxGet/browserbridge"
)

func TestBrowserSettingsActions(t *testing.T) {
	setTestConfig(t, t.TempDir())
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("Browser settings")
	defer w.Close()
	b := newBrowserIntegration(&downloadsView{app: a, window: w})
	page := b.page()
	w.SetContent(page)
	setup := findFluxGetButton(t, page, "Set up browser extension")
	test.Tap(setup)
	test.Tap(findFluxGetButton(t, w.Canvas().Overlays().Top(), "Copy browser address"))
	if a.Clipboard().Content() != "chrome://extensions" {
		t.Fatal("browser address was not copied")
	}
	test.Tap(findFluxGetButton(t, w.Canvas().Overlays().Top(), "Done"))
	disconnect := findFluxGetButton(t, page, "Disconnect all profiles")
	if !disconnect.Disabled() {
		t.Fatal("disconnect should be disabled with an empty profile list")
	}
	profile := browserbridge.Request{Browser: "chrome", Profile: "test-profile-123456"}
	if err := b.store.Edit(func(p *browserbridge.Policy) error {
		p.Enabled, p.Browsers["chrome"] = true, true
		p.Profiles[profile.Profile] = browserbridge.Profile{Browser: profile.Browser, Enabled: true}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	test.Tap(findFluxGetButton(t, page, "Refresh profiles"))
	if disconnect.Disabled() {
		t.Fatal("refresh did not find the connected profile")
	}
	test.Tap(disconnect)
	test.Tap(findFluxGetButton(t, w.Canvas().Overlays().Top(), "Disconnect all"))
	store, err := browserbridge.OpenStore()
	if err != nil {
		t.Fatal(err)
	}
	p := store.Snapshot()
	if !disconnect.Disabled() || len(p.Profiles) != 0 || !p.Enabled || !p.Browsers["chrome"] {
		t.Fatal("disconnect did not persist, or changed the browser settings")
	}
}

func TestBrowserSettingsActionsLocateExtension(t *testing.T) {
	root := filepath.Join(t.TempDir(), "FluxGet [test]")
	exe := filepath.Join(root, "fluxget-gui")
	folder := filepath.Join(root, "extensions", "chrome")
	if _, err := browserExtensionFolder(exe, "chrome"); err == nil {
		t.Fatal("missing extension was accepted")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "manifest.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := browserExtensionFolder(exe, "chrome"); err != nil || got != folder {
		t.Fatalf("extension folder %q, error %v", got, err)
	}
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("Browser settings")
	defer w.Close()
	b := &browserIntegration{view: &downloadsView{app: a, window: w}}
	b.showBrowserSetup(exe)
	test.Tap(findFluxGetButton(t, w.Canvas().Overlays().Top(), "Copy folder path"))
	if got := a.Clipboard().Content(); got != folder {
		t.Fatalf("copied path %q, want %q", got, folder)
	}
}
