package main

import (
	"context"
	"fmt"
	"github.com/Parsania80/FluxGet/internal/platformcmd"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/Parsania80/FluxGet/browserbridge"
)

type browserIntegration struct {
	promptID string

	store *browserbridge.Store
	view  *downloadsView
	note  *widget.Label
}

func newBrowserIntegration(v *downloadsView) *browserIntegration {
	store, err := browserbridge.OpenStore()
	b := &browserIntegration{store: store, view: v, note: widget.NewLabel("")}
	b.note.Wrapping = fyne.TextWrapWord
	if err != nil {
		b.note.SetText("Browser settings unavailable: " + err.Error())
	} else {
		b.note.SetText("Install the extension, enable its browser below, then connect this profile from the extension popup.")
	}
	return b
}
func (b *browserIntegration) page() fyne.CanvasObject {
	title := widget.NewLabelWithStyle("Browser downloads", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	if b.store == nil {
		return container.NewVBox(title, b.note)
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
			result.SetText("Saved. Changes apply to new handoffs. Existing FluxGet downloads continue.")
		}
	})
	explain := widget.NewLabel("Use the extension to grant site access for automatic capture. Public GET downloads are supported; logins, POST, private browsing, query links and uncertain requests stay in the browser. Firefox Snap and Opera need separate native-host verification.")
	explain.Wrapping = fyne.TextWrapWord
	profiles := widget.NewLabel("")
	profiles.Wrapping = fyne.TextWrapWord
	refresh := func() {
		p := b.store.Snapshot()
		counts := map[string]int{}
		for _, profile := range p.Profiles {
			if profile.Enabled {
				counts[profile.Browser]++
			}
		}
		profiles.SetText(fmt.Sprintf("Consenting profiles: Chrome %d · Firefox %d · Opera %d", counts["chrome"], counts["firefox"], counts["opera"]))
	}
	refresh()
	setup := widget.NewButton("Open extension folder", func() {
		exe, err := os.Executable()
		if err != nil {
			return
		}
		path := filepath.Join(filepath.Dir(exe), "extensions")
		u := &url.URL{Scheme: "file", Path: path}
		if err = b.view.app.OpenURL(u); err != nil {
			result.SetText(err.Error())
		}
	})
	reset := widget.NewButton("Disconnect all profiles", func() {
		dialog.ShowConfirm("Disconnect browser profiles?", "Each profile must opt in again. Existing FluxGet jobs continue.", func(yes bool) {
			if !yes {
				return
			}
			err := b.store.Edit(func(p *browserbridge.Policy) error { p.Profiles = map[string]browserbridge.Profile{}; return nil })
			if err != nil {
				result.SetText(err.Error())
			}
			refresh()
		}, b.view.window)
	})
	return container.NewVBox(title, explain, b.note, master, container.NewHBox(browsers["chrome"], browsers["firefox"], browsers["opera"]), widget.NewForm(widget.NewFormItem("Capture mode", mode), widget.NewFormItem("Excluded hosts", exclusions)), container.NewHBox(save, setup, widget.NewButton("Refresh profiles", refresh), reset), profiles, result)
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
		return b.store.Snapshot().Reply(r)
	}
	if r.Operation == "policy" {
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
			begin := func(folder, filename string, later bool) {
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
					output, err := platformcmd.Command(v.executable, append([]string{"-suggest-output"}, downloadArgs(r.URL, folder, filename)...)...).Output()
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
				begin(v.settings.Folder, r.Filename, false)
				return
			}
			folder, name := widget.NewEntry(), widget.NewEntry()
			folder.SetText(v.settings.Folder)
			folder.SetPlaceHolder("Downloads folder")
			name.SetText(r.Filename)
			later := widget.NewCheck("Save paused for later", nil)
			b.promptID = r.Key()
			prompt := dialog.NewForm("Download with FluxGet?", "Use FluxGet", "Use browser", []*widget.FormItem{widget.NewFormItem("Folder", folder), widget.NewFormItem("Filename", name), widget.NewFormItem("", later)}, func(yes bool) {
				if b.promptID == r.Key() {
					b.promptID = ""
				}
				if !yes {
					respond(browserbridge.Failure("Kept in the browser"))
					return
				}
				begin(strings.TrimSpace(folder.Text), name.Text, later.Checked)
			}, v.window)
			prompt.Resize(fyne.NewSize(580, 260))
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
