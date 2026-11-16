package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/test"
	"github.com/Parsania80/FluxGet/browserbridge"
)

// The stock test driver runs UI callbacks on the worker goroutine. Wait for
// the complete callback (including its final refresh) before tearing it down.
type settledTestDriver struct {
	fyne.Driver
	callbacks sync.WaitGroup
}

func (d *settledTestDriver) DoFromGoroutine(fn func(), wait bool) {
	d.callbacks.Add(1)
	defer d.callbacks.Done()
	d.Driver.DoFromGoroutine(fn, wait)
}

type settledTestApp struct {
	fyne.App
	driver *settledTestDriver
}

func (a settledTestApp) Driver() fyne.Driver { return a.driver }

func TestBrowserDuplicateChoicesKeepQueueOwnership(t *testing.T) {
	binary := unifiedCLI(t)
	for _, action := range []string{"continue-running", "continue-paused", "copy", "keep", "close", "replace", "reservation-race"} {
		t.Run(action, func(t *testing.T) {
			setTestConfig(t, t.TempDir())
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Length", "65536")
				w.Header().Set("ETag", `"duplicate-test"`)
				w.WriteHeader(http.StatusOK)
				if r.Method != http.MethodHead {
					_, _ = w.Write(bytes.Repeat([]byte("a"), 2048))
					w.(http.Flusher).Flush()
					<-r.Context().Done()
				}
			}))
			defer server.Close()
			q := unifiedQueue(t, binary, 1)
			// Stop the queue before the gated HTTP fixture closes.
			defer func() { _ = q.send("quit"); <-q.done }()
			unifiedRequest(t, q, desktopRequest{Operation: "initialize"})
			dir := t.TempDir()
			path := filepath.Join(dir, "file.rar")
			if action == "replace" {
				if err := os.WriteFile(path, []byte("original file"), 0600); err != nil {
					t.Fatal(err)
				}
			} else {
				unifiedRequest(t, q, desktopRequest{Operation: "add", Job: queueEntry{URL: server.URL + "/file.rar", Destination: path, State: "paused"}})
				if action == "continue-running" {
					unifiedRequest(t, q, desktopRequest{Operation: "action", Action: "resume", Job: queueEntry{ID: 1}})
					unifiedWait(t, q, func(entries []queueEntry) bool { return entries[0].State == "running" })
				}
			}
			base := test.NewApp()
			driver := &settledTestDriver{Driver: base.Driver()}
			a := settledTestApp{App: base, driver: driver}
			fyne.SetCurrentApp(a)
			w := a.NewWindow("Duplicate test")
			defer func() { driver.callbacks.Wait(); w.Close(); base.Quit() }()
			settings, err := loadSpeedSettings()
			if err != nil {
				t.Fatal(err)
			}
			v := newDownloadsView(a, w, settings, nil, binary)
			w.SetContent(v.content)
			v.client, v.ready = q, true
			v.accept(unifiedRequest(t, q, desktopRequest{Operation: "snapshot"}))
			r := browserbridge.Request{Version: 1, Browser: "chrome", Profile: "profile-duplicate1234", RequestID: "request-duplicate1234", Kind: "link", Operation: "prepare", URL: server.URL + "/file.rar"}
			if err := v.browsers.store.Edit(func(p *browserbridge.Policy) error {
				p.Enabled, p.Browsers["chrome"] = true, true
				p.Profiles[r.Profile] = browserbridge.Profile{Browser: "chrome", Enabled: true}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			responses := make(chan browserbridge.Response, 2)
			draft := downloadDraft{URL: r.URL, Destination: path, Later: true}
			if action == "continue-running" {
				// Browsers can suffix filenames. The exact same active URL still
				// offers the existing transfer instead of silently making a copy.
				draft.Destination = filepath.Join(dir, "file (2).rar")
			}
			respond := func(response browserbridge.Response) { responses <- response }
			if action == "reservation-race" {
				v.browsers.admitDownload(ctx, r, draft, respond)
			} else {
				v.browsers.prepareDownload(ctx, r, draft, respond)
				prompt := v.browsers.promptWindow
				if prompt == nil {
					t.Fatal("duplicate choices were bypassed")
				}
				select {
				case <-responses:
					t.Fatal("handoff resolved before duplicate choice")
				default:
				}
				if action == "replace" {
					test.Tap(findFluxGetButton(t, prompt.Content(), "Replace existing file…"))
					test.Tap(findFluxGetButton(t, prompt.Canvas().Overlays().Top(), "Yes"))
				} else if action == "close" {
					prompt.Close()
				} else {
					label := map[string]string{"continue-running": "Continue existing download", "continue-paused": "Continue existing download", "copy": "Save another copy", "keep": "Keep existing file"}[action]
					test.Tap(findFluxGetButton(t, prompt.Content(), label))
				}
			}
			var response browserbridge.Response
			select {
			case response = <-responses:
			case <-ctx.Done():
				t.Fatal("duplicate choice did not resolve the handoff")
			}
			entries := unifiedRequest(t, q, desktopRequest{Operation: "snapshot"}).Jobs
			if action == "copy" || action == "replace" {
				if !response.OK || response.Stage != "pending" {
					t.Fatalf("new copy lost browser receipt: %+v", response)
				}
				copy := entries[len(entries)-1]
				if copy.Destination == path || copy.IntakeID != r.Key() || copy.State != "paused" || !copy.BrowserPending {
					t.Fatalf("new copy is not a distinct pending handoff: %+v", copy)
				}
				if action == "replace" {
					data, err := os.ReadFile(path)
					if err != nil || string(data) != "original file" {
						t.Fatal("replacement modified the original before completion", err)
					}
					history, err := loadHistory()
					if err != nil || len(history) != 1 || history[0].Replace == nil || history[0].Replace.Path != path || history[0].QueueID != copy.ID {
						t.Fatal("replacement metadata was not persisted before admission", history, err)
					}
				} else if len(entries) != 2 || entries[0].State != "paused" {
					t.Fatal("saving a copy changed the original transfer", entries)
				}
			} else {
				if response.OK || response.Stage != "canceled" || len(entries) != 1 || entries[0].IntakeID != "" {
					t.Fatalf("extra request was not dismissed safely: response=%+v jobs=%+v", response, entries)
				}
				if action == "continue-running" || action == "continue-paused" {
					if entries[0].State != "running" && entries[0].State != "queued" {
						t.Fatal("existing transfer did not continue", entries)
					}
				} else if entries[0].State != "paused" {
					t.Fatal("discarding the extra request changed the existing transfer", entries)
				}
			}
		})
	}
}

func TestSharedDuplicateDialogResolvesOnce(t *testing.T) {
	for _, label := range []string{"Cancel", "Keep existing file", "Save another copy"} {
		t.Run(label, func(t *testing.T) {
			setTestConfig(t, t.TempDir())
			a := test.NewApp()
			defer a.Quit()
			w := a.NewWindow("Normal add")
			defer w.Close()
			settings, err := loadSpeedSettings()
			if err != nil {
				t.Fatal(err)
			}
			v := newDownloadsView(a, w, settings, nil, "")
			w.SetContent(v.content)
			w.Show()
			path := filepath.Join(t.TempDir(), "file.rar")
			v.entries = []queueEntry{{ID: 1, State: "running", Destination: path}}
			var choices []duplicateChoice
			v.resolveDuplicate(downloadDraft{Destination: path}, path, false, func(choice duplicateChoice) { choices = append(choices, choice) })
			test.Tap(findFluxGetButton(t, w.Canvas().Overlays().Top(), label))
			if len(choices) != 1 {
				t.Fatalf("duplicate decision count: %d", len(choices))
			}
			if label == "Save another copy" && (choices[0].Draft == nil || choices[0].Draft.Destination == path) {
				t.Fatal("copy choice did not choose a distinct destination")
			}
			if choices[0].Keep != (label == "Keep existing file") {
				t.Fatal("cancel and keep were conflated")
			}
		})
	}
}
