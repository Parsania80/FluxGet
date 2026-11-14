package main

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/Parsania80/FluxGet/browserbridge"
	"github.com/Parsania80/FluxGet/internal/platformcmd"
)

type browserIntegration struct {
	promptID string

	store           *browserbridge.Store
	view            *downloadsView
	note            *widget.Label
	refreshProfiles func()
}

func newBrowserIntegration(v *downloadsView) *browserIntegration {
	store, err := browserbridge.OpenStore()
	note := widget.NewLabel("")
	note.Wrapping = fyne.TextWrapWord
	if err != nil {
		note.SetText("Browser settings unavailable: " + err.Error())
	}
	return &browserIntegration{store: store, view: v, note: note}
}
func (b *browserIntegration) page() fyne.CanvasObject {
	if b.store == nil {
		return b.note
	}
	p := b.store.Snapshot()
	master := widget.NewCheck("Enable browser integration", nil)
	master.SetChecked(p.Enabled)
	browsers := map[string]*widget.Check{}
	for _, name := range []string{"chrome", "firefox", "opera"} {
		c := widget.NewCheck(map[string]string{"chrome": "Chrome", "firefox": "Firefox", "opera": "Opera"}[name], nil)
		c.SetChecked(p.Browsers[name])
		browsers[name] = c
	}
	mode := widget.NewSelect([]string{"Ask before downloading", "Download automatically", "Manual sending only"}, nil)
	names := map[string]string{"ask": "Ask before downloading", "automatic": "Download automatically", "manual": "Manual sending only"}
	mode.SetSelected(names[p.Mode])
	exclusions := widget.NewEntry()
	exclusions.SetText(strings.Join(p.Exclusions, ", "))
	exclusions.SetPlaceHolder("example.com, another.example")
	result := widget.NewLabel("")
	result.Wrapping = fyne.TextWrapWord
	save := widget.NewButton("Save browser settings", func() {
		nextMode := map[string]string{"Ask before downloading": "ask", "Download automatically": "automatic", "Manual sending only": "manual"}[mode.Selected]
		var excluded []string
		for _, s := range strings.Split(exclusions.Text, ",") {
			s = strings.ToLower(strings.TrimSpace(s))
			if s != "" {
				excluded = append(excluded, s)
			}
		}
		err := b.store.Edit(func(p *browserbridge.Policy) error {
			p.Enabled = master.Checked
			p.Mode = nextMode
			p.Exclusions = excluded
			for name, c := range browsers {
				p.Browsers[name] = c.Checked
			}
			return nil
		})
		if err != nil {
			result.SetText("Could not save: " + err.Error())
		} else {
			result.SetText("Browser settings saved.")
		}
	})
	save.Importance = widget.HighImportance
	intro := widget.NewLabel("Use the FluxGet extension to send public downloads from your browser to this app.")
	intro.Wrapping = fyne.TextWrapWord
	setup := widget.NewButton("Set up browser extension", b.showSetup)
	setup.Importance = widget.HighImportance
	profiles := widget.NewLabel("")
	profiles.Wrapping = fyne.TextWrapWord
	reset := widget.NewButton("Disconnect all profiles", nil)
	b.refreshProfiles = func() {
		p := b.store.Snapshot()
		counts := map[string]int{}
		for _, profile := range p.Profiles {
			if profile.Enabled {
				counts[profile.Browser]++
			}
		}
		if len(counts) == 0 {
			profiles.SetText("No profiles connected. Open the FluxGet extension in your browser and turn on Enable this browser profile.")
			reset.Disable()
			return
		}
		var connected []string
		for _, name := range []string{"Chrome", "Firefox", "Opera"} {
			if count := counts[strings.ToLower(name)]; count > 0 {
				connected = append(connected, fmt.Sprintf("%s (%d)", name, count))
			}
		}
		profiles.SetText(strings.Join(connected, " · "))
		reset.Enable()
	}
	reset.OnTapped = func() {
		confirm := dialog.NewConfirm("Disconnect all browser profiles?", "To reconnect, open the FluxGet extension in each browser profile and enable it again. Current downloads will continue.", func(yes bool) {
			if !yes {
				return
			}
			err := b.store.Edit(func(p *browserbridge.Policy) error { p.Profiles = map[string]browserbridge.Profile{}; return nil })
			if err != nil {
				result.SetText("Could not disconnect profiles: " + err.Error())
				return
			}
			b.refreshProfiles()
			result.SetText("All browser profiles disconnected.")
		}, b.view.window)
		confirm.SetConfirmText("Disconnect all")
		confirm.Show()
	}
	reset.Importance = widget.LowImportance
	b.refreshProfiles()
	refresh := widget.NewButton("Refresh profiles", func() {
		b.refreshProfiles()
		result.SetText("Profile list refreshed.")
	})
	options := widget.NewCard("Browser settings", "", container.NewVBox(master,
		container.NewHBox(browsers["chrome"], browsers["firefox"], browsers["opera"]),
		widget.NewForm(widget.NewFormItem("Capture mode", mode), widget.NewFormItem("Excluded hosts", exclusions))))
	connection := widget.NewCard("Connected profiles", "Browser profiles you enabled from the extension", container.NewVBox(profiles,
		container.NewGridWithColumns(2, refresh, reset)))
	return container.NewBorder(nil,
		container.NewVBox(widget.NewSeparator(), container.NewBorder(nil, nil, nil, save, result)), nil, nil,
		container.NewVScroll(container.NewPadded(container.NewVBox(intro, setup, options, connection, b.note))))
}
func (b *browserIntegration) handle(ctx context.Context, r browserbridge.Request) browserbridge.Response {
	if b.store == nil {
		return browserbridge.Failure("Browser settings are unavailable")
	}
	if err := browserbridge.Validate(r); err != nil {
		return browserbridge.Failure(err.Error())
	}
	if r.Operation == "profile" {
		err := b.store.Edit(func(p *browserbridge.Policy) error {
			if existing, ok := p.Profiles[r.Profile]; ok && existing.Browser != r.Browser {
				return fmt.Errorf("profile belongs to another browser")
			}
			p.Profiles[r.Profile] = browserbridge.Profile{Browser: r.Browser, Enabled: r.Consent}
			return nil
		})
		if err != nil {
			return browserbridge.Failure(err.Error())
		}
		fyne.Do(func() {
			if b.refreshProfiles != nil && !b.view.closing {
				b.refreshProfiles()
			}
		})
		return b.store.Snapshot().Reply(r)
	}
	if r.Operation == "policy" || r.Operation == "profile-status" {
		return b.store.Snapshot().Reply(r)
	}
	reply := make(chan browserbridge.Response, 1)
	respond := func(r browserbridge.Response) {
		select {
		case reply <- r:
		default:
		}
	}
	fyne.Do(func() {
		v := b.view
		if ctx.Err() != nil || !v.ready || v.client == nil || v.closing || v.busy {
			respond(browserbridge.Failure("FluxGet is busy or not ready; keep this download in the browser"))
			return
		}
		if r.Operation == "prepare" {
			if b.promptID != "" {
				respond(browserbridge.Failure("Answer the current browser download prompt first"))
				return
			}
			p := b.store.Snapshot()
			if !p.Allowed(r) || p.Excluded(r.URL) || (r.Kind == "capture" && p.Mode == "manual") {
				respond(browserbridge.Failure("Capture is disabled for this browser, profile or site"))
				return
			}
			begin := func(folder, filename string, later, sortFiles bool) {
				if ctx.Err() != nil {
					respond(browserbridge.Failure("Handoff timed out; use the browser"))
					return
				}
				fresh := b.store.Snapshot()
				if !fresh.Allowed(r) || fresh.Excluded(r.URL) || (r.Kind == "capture" && fresh.Mode == "manual") {
					respond(browserbridge.Failure("Capture was disabled"))
					return
				}
				filename = strings.TrimSpace(filename)
				if filename == "" || filepath.Base(filename) != filename || strings.ContainsAny(filename, "/\\") {
					respond(browserbridge.Failure("Choose a plain filename"))
					return
				}
				route, routeErr := proxyForDownload(v.settings.Proxy, v.settings.Proxy.Enabled)
				if routeErr != nil {
					respond(browserbridge.Failure("Configure the proxy address in FluxGet Settings"))
					return
				}
				v.workResult(func(q *queueClient) (desktopResponse, error) {
					args := append([]string{"-suggest-output"}, downloadArgs(r.URL, folder, filename)...)
					if sortFiles {
						args = append(args, "-sort-files")
					}
					output, err := platformcmd.Command(v.executable, args...).Output()
					if err != nil {
						return desktopResponse{}, fmt.Errorf("cannot choose the download folder or filename")
					}
					dest, err := strconv.Unquote(strings.TrimSpace(string(output)))
					if err != nil {
						return desktopResponse{}, err
					}
					// Identical IDs must always choose the identical destination. A conflict
					// leaves the browser item intact; there is no implicit overwrite.
					return q.request(ctx, desktopRequest{Operation: "browser-prepare", Action: r.Kind, BrowserLater: later, Job: queueEntry{Proxy: route, URL: r.URL, Destination: dest, State: "paused", IntakeID: r.Key()}})
				}, func(response desktopResponse) {
					respond(browserbridge.Response{Version: browserbridge.Version, OK: true, JobID: response.JobID, Stage: response.BrowserStage, Filename: filename})
				}, func(err error) { respond(browserbridge.Failure(err.Error())) })
				// v.work reports errors on screen; the caller also needs an immediate
				// protocol result rather than waiting for the entire browser timeout.
			}
			if p.Mode == "automatic" && r.Kind == "capture" {
				begin(v.settings.Folder, r.Filename, false, v.settings.SortFiles)
				return
			}
			folder, name := widget.NewEntry(), widget.NewEntry()
			folder.SetText(v.settings.Folder)
			folder.SetPlaceHolder("Downloads folder")
			name.SetText(r.Filename)
			later := widget.NewCheck("Save paused for later", nil)
			sortFiles := widget.NewCheck("Sort into a file-type folder", nil)
			sortFiles.SetChecked(v.settings.SortFiles)
			b.promptID = r.Key()
			prompt := dialog.NewForm("Download with FluxGet?", "Use FluxGet", "Use browser", []*widget.FormItem{widget.NewFormItem("Folder", folder), widget.NewFormItem("Filename", name), widget.NewFormItem("", sortFiles), widget.NewFormItem("", later)}, func(yes bool) {
				if b.promptID == r.Key() {
					b.promptID = ""
				}
				if !yes {
					respond(browserbridge.Failure("Kept in the browser"))
					return
				}
				begin(strings.TrimSpace(folder.Text), name.Text, later.Checked, sortFiles.Checked)
			}, v.window)
			prompt.Resize(fyne.NewSize(580, 310))
			v.window.Show()
			v.window.RequestFocus()
			prompt.Show()
			go func() {
				select {
				case <-ctx.Done():
					fyne.Do(func() {
						if b.promptID == r.Key() {
							b.promptID = ""
						}
						prompt.Hide()
					})
				case <-time.After(60 * time.Second):
					fyne.Do(func() {
						if b.promptID == r.Key() {
							b.promptID = ""
						}
						prompt.Hide()
					})
				}
			}()
			return
		}
		policy := b.store.Snapshot()
		if r.Operation == "activate" && !r.Canceled {
			respond(browserbridge.Failure("Browser cancellation is not confirmed"))
			return
		}
		op := map[string]string{"activate": "browser-activate", "abort": "browser-abort", "status": "browser-status"}[r.Operation]
		state := "paused"
		if policy.Allowed(r) {
			state = "queued"
		}
		action := "browser-canceled"
		if r.Kind == "link" {
			action = "link"
		}
		v.browserWork(ctx, desktopRequest{Operation: op, Action: action, Job: queueEntry{IntakeID: r.Key(), State: state}}, respond)
	})
	select {
	case response := <-reply:
		return response
	case <-ctx.Done():
		return browserbridge.Failure("Handoff timed out; check the extension before retrying")
	}
}
func (v *downloadsView) browserWork(ctx context.Context, request desktopRequest, respond func(browserbridge.Response)) {
	v.workResult(func(q *queueClient) (desktopResponse, error) {
		if request.Operation == "browser-activate" {
			current, err := q.request(ctx, desktopRequest{Operation: "snapshot"})
			if err != nil {
				return desktopResponse{}, err
			}
			p := v.browsers.store.Snapshot()
			for _, job := range current.Jobs {
				if job.IntakeID == request.Job.IntakeID {
					parts := strings.Split(request.Job.IntakeID, ":")
					identity := browserbridge.Request{Browser: parts[0], Profile: parts[1]}
					if !p.Allowed(identity) || p.Excluded(job.URL) || (request.Action != "link" && p.Mode == "manual") {
						request.Job.State = "paused"
					}
				}
			}
		}
		return q.request(ctx, request)
	}, func(response desktopResponse) {
		respond(browserbridge.Response{Version: browserbridge.Version, OK: true, JobID: response.JobID, Stage: response.BrowserStage})
	}, func(err error) { respond(browserbridge.Failure(err.Error())) })
}
