//go:build linux

package main

import (
	"bytes"
	"fmt"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/Parsania80/FluxGet/browserbridge"
)

func TestBrowserSettingsStartDisabled(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	a := test.NewApp()
	defer a.Quit()
	w := a.NewWindow("Browser settings")
	defer w.Close()
	settings, e := loadSpeedSettings()
	if e != nil {
		t.Fatal(e)
	}
	v := newDownloadsView(a, w, settings, nil, "")
	if batchAction(queueEntry{State: "paused", BrowserPending: true}, "start") != "" {
		t.Fatal("pending browser job can resume from UI")
	}
	if v.browsers.store == nil || v.browsers.store.Snapshot().Enabled {
		t.Fatal("browser enabled without consent")
	}
	w.SetContent(v.browsers.page())
	w.Resize(fyne.NewSize(900, 650))
	w.Show()
	if screenshot := os.Getenv("FLUXGET_BROWSER_SETTINGS_SCREENSHOT"); screenshot != "" {
		f, e := os.Create(screenshot)
		if e != nil {
			t.Fatal(e)
		}
		e = png.Encode(f, w.Canvas().Capture())
		f.Close()
		if e != nil {
			t.Fatal(e)
		}
	}
}

// Actual Chrome APIs/native messaging in a disposable profile. Only the copied
// test manifest pre-grants all-site capture; shipping grants remain optional.
func TestBrowserNativeChromeAcceptance(t *testing.T) {
	if os.Getenv("FLUXGET_BROWSER_NATIVE_SMOKE") != "1" {
		t.Skip("requires explicit installed-Chrome isolated-profile acceptance")
	}
	root, e := os.MkdirTemp("/tmp", "fg-browser-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_RUNTIME_DIR", filepath.Join(root, "runtime"))
	if e = os.MkdirAll(os.Getenv("XDG_RUNTIME_DIR"), 0700); e != nil {
		t.Fatal(e)
	}
	binary := unifiedCLI(t)
	host := filepath.Join(filepath.Dir(binary), "fluxget-native-chrome")
	build := exec.Command("go", "build", "-o", host, "./cmd/fluxget-native-host")
	build.Dir = ".."
	if output, e := build.CombinedOutput(); e != nil {
		t.Fatalf("native build: %v %s", e, output)
	}
	copyCmd := exec.Command("python3", "../packaging/build_browser_extensions.py", "--out", filepath.Join(root, "extensions"))
	if out, e := copyCmd.CombinedOutput(); e != nil {
		t.Fatalf("extension build: %v %s", e, out)
	}
	extension := filepath.Join(root, "extensions", "chrome")
	raw, e := os.ReadFile(filepath.Join(extension, "manifest.json"))
	if e != nil {
		t.Fatal(e)
	}
	raw = bytes.Replace(raw, []byte(`"permissions": [`), []byte(`"host_permissions": ["http://*/*", "https://*/*"], "permissions": ["webRequest", "scripting",`), 1)
	if e = os.WriteFile(filepath.Join(extension, "manifest.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	payload := bytes.Repeat([]byte("browser handoff\n"), 1<<19)
	const finalQuery = "token=test%2Btoken&part=1&part=2&empty="
	var queryRequests atomic.Int32
	var policyStore atomic.Pointer[browserbridge.Store]
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/disable-integration" || r.URL.Path == "/exclude-integration" {
			if store := policyStore.Load(); store != nil {
				if e := store.Edit(func(p *browserbridge.Policy) error {
					if r.URL.Path == "/exclude-integration" {
						p.Exclusions = []string{"127.0.0.1"}
					} else {
						p.Enabled = false
						p.Exclusions = nil
					}
					return nil
				}); e != nil {
					http.Error(w, "policy", 500)
					return
				}
			}
			w.WriteHeader(204)
			return
		}
		if r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<a id="download" href="/redirect.iso?token=initial">Download</a><br><a id="bypass" href="/bypass-redirect.iso?token=browser">Use browser</a>`))
			return
		}
		if r.URL.Path == "/bypass-redirect.iso" {
			http.Redirect(w, r, "/bypass.iso?token=browserFinal", http.StatusFound)
			return
		}
		if r.URL.Path == "/redirect.iso" {
			http.Redirect(w, r, "/native.iso?"+finalQuery, http.StatusFound)
			return
		}
		if r.URL.Path == "/native.iso" {
			if r.URL.RawQuery != finalQuery {
				http.Error(w, "download query was not preserved", http.StatusForbidden)
				return
			}
			queryRequests.Add(1)
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filepath.Base(r.URL.Path)))
		w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
		for at := 0; at < len(payload); at += 32768 {
			end := min(at+32768, len(payload))
			if _, e := w.Write(payload[at:end]); e != nil {
				return
			}
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
				return
			case <-time.After(10 * time.Millisecond):
			}
		}
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
	settings, e := loadSpeedSettings()
	if e != nil {
		t.Fatal(e)
	}
	settings.Folder = filepath.Join(root, "downloads")
	if e = os.Mkdir(settings.Folder, 0700); e != nil {
		t.Fatal(e)
	}
	var v *downloadsView
	var w fyne.Window
	driver.DoFromGoroutine(func() {
		w = a.NewWindow("Browser acceptance")
		v = newDownloadsView(a, w, settings, nil, binary)
		w.SetContent(v.content)
		w.Show()
		v.connect()
	}, true)
	defer func() {
		closed := make(chan struct{})
		driver.DoFromGoroutine(func() { v.close(func() { w.Close(); close(closed) }) }, true)
		select {
		case <-closed:
		case <-time.After(10 * time.Second):
			t.Error("desktop did not stop")
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		driver.DoFromGoroutine(func() { ready = v.ready }, true)
		if ready {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !ready {
		t.Fatal("desktop not ready")
	}
	ask := os.Getenv("FLUXGET_BROWSER_NATIVE_MODE") == "ask"
	var prompts atomic.Int32
	if e = v.browsers.store.Edit(func(p *browserbridge.Policy) error {
		p.Enabled = true
		p.Browsers["chrome"] = true
		p.Mode = "automatic"
		if ask {
			p.Mode = "ask"
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	policyStore.Store(v.browsers.store)
	bridge, e := browserbridge.Serve(v.browsers.handle)
	if e != nil {
		t.Fatal(e)
	}
	defer bridge.Close()
	if ask {
		stop, done := make(chan struct{}), make(chan struct{})
		go func() {
			defer close(done)
			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-stop:
					return
				case <-ticker.C:
					driver.DoFromGoroutine(func() {
						if v.browsers.promptID != "" {
							test.Tap(batchDialogButton(t, w, "Use FluxGet"))
							prompts.Add(1)
						}
					}, true)
				}
			}
		}()
		defer func() { close(stop); <-done }()
	}
	chrome, e := exec.LookPath("google-chrome")
	if e != nil {
		t.Skip("Chrome unavailable")
	}
	command := exec.Command("node", "../extensions/tests/native-chrome.mjs", chrome, filepath.Join(root, "chrome"), extension, host, server.URL+"/")
	command.Env = os.Environ()
	output, e := command.CombinedOutput()
	t.Log(string(output))
	if e != nil {
		t.Fatalf("Chrome acceptance: %v", e)
	}
	if ask && prompts.Load() != 1 {
		t.Fatal("Ask first did not show exactly one accepted destination prompt")
	}
	deadline = time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		var complete bool
		driver.DoFromGoroutine(func() { complete = len(v.entries) == 1 && v.entries[0].State == "completed" }, true)
		if complete {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	fallback, e := os.ReadFile(filepath.Join(root, "chrome", "downloads", "fallback.iso"))
	if e != nil || !bytes.Equal(fallback, payload) {
		t.Fatal("browser fallback output mismatch", e)
	}
	excluded, e := os.ReadFile(filepath.Join(root, "chrome", "downloads", "excluded.iso"))
	if e != nil || !bytes.Equal(excluded, payload) {
		t.Fatal("excluded-host browser output mismatch", e)
	}
	bypass, e := os.ReadFile(filepath.Join(root, "chrome", "downloads", "bypass.iso"))
	if e != nil || !bytes.Equal(bypass, payload) {
		t.Fatal("Ctrl-click browser output mismatch", e)
	}
	data, e := os.ReadFile(filepath.Join(settings.Folder, "Other", "native.iso"))
	if e != nil || !bytes.Equal(data, payload) {
		t.Fatal("native handoff output mismatch", e)
	}
	var entries []queueEntry
	driver.DoFromGoroutine(func() { entries = append(entries, v.entries...) }, true)
	if len(entries) != 1 || entries[0].State != "completed" {
		t.Fatalf("duplicate or incomplete jobs: %+v", entries)
	}
	if entries[0].URL != server.URL+"/native.iso?"+finalQuery || queryRequests.Load() < 2 {
		t.Fatal("native query handoff did not preserve the exact CDN URL")
	}
}
