package main

import (
	"context"
	"fmt"
	"image/color"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

type fluxTheme struct{ fyne.Theme }

func (t fluxTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	if name == theme.ColorNamePrimary {
		return color.NRGBA{R: 0, G: 125, B: 111, A: 255}
	}
	return t.Theme.Color(name, variant)
}

func main() {
	a := app.NewWithID(applicationID)
	a.SetIcon(appIcon)
	a.Settings().SetTheme(fluxTheme{theme.DefaultTheme()})
	w := a.NewWindow("FluxGet")
	w.SetIcon(appIcon)
	w.Resize(fyne.NewSize(1100, 720))
	lock, err := lockHistory()
	if err != nil {
		w.SetContent(widget.NewLabel("Cannot open FluxGet: " + err.Error()))
		w.ShowAndRun()
		return
	}
	defer lock.Close()
	history, err := loadHistory()
	if err != nil {
		w.SetContent(widget.NewLabel("Cannot open download history: " + err.Error()))
		w.ShowAndRun()
		return
	}
	settings, err := loadSpeedSettings()
	if err != nil {
		w.SetContent(widget.NewLabel("Cannot open settings: " + err.Error()))
		w.ShowAndRun()
		return
	}
	history, discoveryErr := discoverSavedJobs(history)
	executable, err := cliPath()
	if err != nil {
		w.SetContent(widget.NewLabel(err.Error()))
		w.ShowAndRun()
		return
	}
	view := newDownloadsView(a, w, settings, history, executable)
	w.SetContent(view.content)
	w.SetCloseIntercept(func() { view.close(func() { w.SetCloseIntercept(nil); w.Close() }) })
	view.connect()
	if discoveryErr != nil {
		view.status.SetText("Some saved downloads could not be discovered. Your existing history is kept.")
	}
	w.ShowAndRun()
}

type downloadsView struct {
	app                                                      fyne.App
	window                                                   fyne.Window
	settings                                                 *speedSettings
	history                                                  []job
	entries                                                  []queueEntry
	executable                                               string
	client                                                   *queueClient
	revision                                                 uint64
	ready, busy, closing                                     bool
	restoring                                                bool
	closeDone                                                func()
	category, selected                                       string
	checked                                                  map[string]bool
	prompted                                                 map[int]string
	finishing                                                map[int]bool
	content                                                  fyne.CanvasObject
	list                                                     *widget.List
	filters                                                  *widget.RadioGroup
	rowBase, partHeader, partCard                            float32
	partDialog                                               dialog.Dialog
	partKey                                                  string
	partDetails                                              *fyne.Container
	status, summary                                          *widget.Label
	selectionCount                                           *widget.Label
	management                                               *budgetSnapshot
	empty                                                    fyne.CanvasObject
	add, pause, resume, retry, open, folder, more, reconnect *widget.Button
	remove, selectAll, clearSelection                        *widget.Button
}

func newDownloadsView(a fyne.App, w fyne.Window, settings *speedSettings, history []job, executable string) *downloadsView {
	v := &downloadsView{app: a, window: w, settings: settings, history: history, executable: executable, category: "All downloads", checked: map[string]bool{}, prompted: map[int]string{}, finishing: map[int]bool{}}
	v.status = widget.NewLabel("Preparing your downloads…")
	v.status.Wrapping = fyne.TextWrapWord
	v.summary = widget.NewLabel("")
	v.add = widget.NewButtonWithIcon("Add download", theme.ContentAddIcon(), v.addDialog)
	v.add.Importance = widget.HighImportance
	v.pause = widget.NewButtonWithIcon("Pause", theme.MediaPauseIcon(), func() { v.command("pause") })
	v.resume = widget.NewButtonWithIcon("Start / Resume", theme.MediaPlayIcon(), func() { v.resumeSelected() })
	v.retry = widget.NewButtonWithIcon("Retry", theme.ViewRefreshIcon(), func() { v.command("retry") })
	v.open = widget.NewButtonWithIcon("Open file", theme.DocumentIcon(), func() { v.openSelected(false) })
	v.folder = widget.NewButtonWithIcon("Show in folder", theme.FolderOpenIcon(), func() { v.openSelected(true) })
	v.more = widget.NewButton("More…", v.moreActions)
	v.remove = widget.NewButtonWithIcon("Remove…", theme.DeleteIcon(), v.removalMenu)
	v.selectionCount = widget.NewLabel("0 selected")
	v.selectAll = widget.NewButton("Select all", func() {
		for _, row := range v.rows() {
			v.checked[row.Key] = true
		}
		v.refresh()
	})
	v.clearSelection = widget.NewButton("Clear selection", func() {
		clear(v.checked)
		v.selected = ""
		v.refresh()
	})
	v.reconnect = widget.NewButton("Try again", v.connect)
	v.reconnect.Hide()
	v.list = widget.NewList(func() int { return len(v.rows()) }, func() fyne.CanvasObject {
		name := widget.NewLabelWithStyle("Filename", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		name.Truncation = fyne.TextTruncateEllipsis
		detail := widget.NewLabel("Status")
		detail.Wrapping = fyne.TextWrapWord
		controls := container.NewHBox(widget.NewLabelWithStyle("Speed: —", fyne.TextAlignTrailing, fyne.TextStyle{Bold: true}), widget.NewButton("Limit: default", nil), widget.NewButton("Remove limit", nil), widget.NewCheck("Select", nil))
		return container.NewVBox(container.NewBorder(nil, nil, nil, controls, name), detail, progressLine(), newSegmentPanel())
	}, func(id widget.ListItemID, object fyne.CanvasObject) {
		rows := v.rows()
		if id < 0 || id >= len(rows) {
			return
		}
		row := rows[id]
		j := row.Entry
		box := object.(*fyne.Container)
		line := box.Objects[0].(*fyne.Container)
		name := filepath.Base(j.output())
		if j.output() == "" {
			name = "Saved download"
		}
		line.Objects[0].(*widget.Label).SetText(name)
		controls := line.Objects[1].(*fyne.Container)
		check := controls.Objects[3].(*widget.Check)
		check.OnChanged = nil
		check.SetChecked(v.checked[row.Key])
		check.OnChanged = func(checked bool) {
			v.restoring = true
			v.list.UnselectAll()
			v.selected = ""
			v.restoring = false
			if checked {
				v.checked[row.Key] = true
			} else {
				delete(v.checked, row.Key)
			}
			v.actions()
		}
		if v.busy || v.closing {
			check.Disable()
		} else {
			check.Enable()
		}
		controls.Objects[0].(*widget.Label).SetText(downloadSpeed(j, v.settings.DisplayUnit))
		button := controls.Objects[1].(*widget.Button)
		bindLimitButton(button, v.settings, row.Key, name, func(key, filename string) { v.settings.editDownload(w, key, filename, v.refresh) })
		remove := controls.Objects[2].(*widget.Button)
		bindRemoveLimitButton(remove, v.settings, row.Key, func(key string) {
			if err := v.settings.saveDownloadLimit(key, 0); err != nil {
				dialog.ShowError(err, w)
				return
			}
			v.status.SetText("Custom speed limit removed. Settings limits still apply.")
			v.refresh()
		})
		if !v.ready || v.busy {
			button.Disable()
			remove.Disable()
		} else {
			button.Enable()
			remove.Enable()
		}
		details := downloadState(j.State) + "  ·  " + downloadDetails(j, v.settings.DisplayUnit)
		if j.State == "running" && j.Requests != nil && j.Requests.Active == 0 && j.Requests.Waiting > 0 {
			details = "Waiting for a connection slot  ·  " + downloadDetails(j, v.settings.DisplayUnit)
		}
		box.Objects[1].(*widget.Label).SetText(details)
		value, known := progressValue(j.Progress)
		if !known && j.SavedProgress != nil {
			value, known = *j.SavedProgress, true
		}
		if downloadState(j.State) == "Completed" {
			value, known = 1, true
		}
		showProgress(box.Objects[2].(*widget.ProgressBar), value, known)
		key := row.Key
		updateSegmentPanel(box.Objects[3].(*fyne.Container), j, func() { v.showParts(key) })
	})
	template := v.list.CreateItem().(*fyne.Container)
	panel := template.Objects[3].(*fyne.Container)
	v.rowBase = template.MinSize().Height - panel.MinSize().Height - theme.Padding()
	v.partHeader = panel.Objects[0].MinSize().Height
	v.partCard = newSegmentCard().MinSize().Height
	v.list.OnSelected = func(id widget.ListItemID) {
		rows := v.rows()
		if id < 0 || id >= len(rows) {
			return
		}
		v.selected = rows[id].Key
		if !v.restoring {
			v.status.SetText(rows[id].Entry.output())
		}
		v.actions()
	}
	v.list.OnUnselected = func(widget.ListItemID) {
		if !v.restoring {
			v.selected = ""
			v.actions()
		}
	}
	categories := widget.NewRadioGroup([]string{"All downloads", "Downloading", "Waiting", "Paused", "Completed", "Failed"}, func(category string) {
		v.category = category
		v.selected = ""
		clear(v.checked)
		v.list.UnselectAll()
		v.refresh()
	})
	v.filters = categories
	sidebar := container.NewPadded(container.NewVBox(widget.NewLabelWithStyle("DOWNLOADS", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), categories))
	emptyTitle := widget.NewLabelWithStyle("Your downloads appear here", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	emptyText := widget.NewLabel("Add a download to begin. Extra files wait automatically.")
	emptyText.Alignment = fyne.TextAlignCenter
	emptyText.Wrapping = fyne.TextWrapWord
	v.empty = container.NewCenter(container.NewVBox(emptyTitle, emptyText))
	stack := container.NewStack(v.empty, v.list)
	split := container.NewHSplit(sidebar, container.NewPadded(stack))
	split.Offset = 0.19
	title := widget.NewLabelWithStyle("Downloads", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	toolbar := container.NewHBox(v.add, v.pause, v.resume, v.retry, v.open, v.folder, v.more)
	selectionBar := container.NewHBox(v.selectAll, v.clearSelection, v.selectionCount, v.remove)
	top := container.NewPadded(container.NewVBox(title, v.summary, toolbar, selectionBar, widget.NewSeparator()))
	downloads := container.NewBorder(top, nil, nil, nil, split)
	settings.applyDownloads = v.applySettings
	settingsPage := settings.page(w, func() { v.refresh() }, func() bool { return v.ready && !v.busy && !v.closing })
	tabs := container.NewAppTabs(container.NewTabItemWithIcon("Downloads", theme.DownloadIcon(), downloads), container.NewTabItemWithIcon("Settings", theme.SettingsIcon(), settingsPage))
	v.content = container.NewBorder(nil, container.NewPadded(container.NewBorder(nil, nil, nil, v.reconnect, v.status)), nil, nil, tabs)
	categories.SetSelected("All downloads")
	v.refresh()
	return v
}

func (v *downloadsView) rows() []downloadRow { return downloadRows(v.entries, v.history, v.category) }
func (v *downloadsView) selection() (downloadRow, bool) {
	for _, row := range v.rows() {
		if row.Key == v.selected {
			return row, true
		}
	}
	return downloadRow{}, false
}
func (v *downloadsView) actions() {
	for _, button := range []*widget.Button{v.pause, v.resume, v.retry, v.open, v.folder, v.more, v.remove, v.selectAll, v.clearSelection} {
		button.Disable()
	}
	v.add.Disable()
	rows := v.selectedRows()
	v.selectionCount.SetText(fmt.Sprintf("%d selected", len(rows)))
	if v.ready && !v.busy && !v.closing {
		v.add.Enable()
	} else {
		return
	}
	if len(v.rows()) > 0 {
		v.selectAll.Enable()
	}
	if len(rows) == 0 {
		return
	}
	v.clearSelection.Enable()
	v.more.Enable()
	v.remove.Enable()
	if len(rows) == 1 {
		j := rows[0].Entry
		if j.output() != "" {
			v.folder.Enable()
		}
		if downloadState(j.State) == "Completed" || j.State == "Already exists" {
			v.open.Enable()
		}
	}
	for _, row := range rows {
		if batchAction(row.Entry, "pause") != "" {
			v.pause.Enable()
		}
		if batchAction(row.Entry, "start") != "" {
			v.resume.Enable()
		}
		if batchAction(row.Entry, "retry") != "" {
			v.retry.Enable()
		}
	}
}
func (v *downloadsView) refresh() {
	selected := v.selected
	visible := make(map[string]bool)
	for _, row := range v.rows() {
		visible[row.Key] = true
	}
	for key := range v.checked {
		if !visible[key] {
			delete(v.checked, key)
		}
	}
	v.restoring = true
	v.list.UnselectAll()
	rows := v.rows()
	for i, row := range rows {
		height := v.rowBase
		if len(row.Entry.Segments) > 0 || row.Entry.State == "running" || row.Entry.State == "paused" {
			height += theme.Padding() + v.partHeader
			if n := len(row.Entry.Segments); n > 0 {
				lines := (n + 3) / 4
				height += theme.Padding() + float32(lines)*v.partCard + float32(lines-1)*theme.Padding()
			}
		}
		v.list.SetItemHeight(i, height)
	}
	v.list.Refresh()
	v.updatePartDetails()
	if len(rows) == 0 {
		v.list.Hide()
		v.empty.Show()
	} else {
		v.empty.Hide()
		v.list.Show()
	}
	v.selected = ""
	for i, row := range rows {
		if row.Key == selected {
			v.list.Select(i)
			break
		}
	}
	v.restoring = false
	running, waiting, paused := 0, 0, 0
	for _, row := range downloadRows(v.entries, v.history, "All downloads") {
		switch row.Entry.displayState() {
		case "Downloading":
			running++
		case "Waiting":
			waiting++
		case "Paused":
			paused++
		}
	}
	summary := fmt.Sprintf("%d downloading  ·  %d waiting  ·  %d paused  ·  up to %d at once", running, waiting, paused, v.settings.MaxActive)
	if v.management != nil {
		summary = map[string]string{"automatic": "Automatic", "manual": "Manual"}[v.management.Policy.Mode] + "  ·  " + summary
	}
	v.summary.SetText(summary)
	v.actions()
}
func (v *downloadsView) apply(entries []queueEntry) {
	v.entries = entries
	for i := range v.history {
		h := &v.history[i]
		for _, entry := range entries {
			if entry.ID != h.QueueID || h.QueueID == 0 {
				continue
			}
			if h.Replace != nil && entry.State == "completed" && h.State != "Needs attention" && !v.finishing[entry.ID] {
				v.finishReplacement(i, entry)
			}
			if h.Replace == nil && entry.State == "completed" && h.Path != "" && h.Path != entry.Destination && entry.OutputPath == "" {
				v.publishReplacement(entry.ID, h.Path)
			}
		}
	}
	v.refresh()
	for _, entry := range entries {
		if entry.State == "failed" && entry.Conflict != "" && v.prompted[entry.ID] != entry.Conflict && !v.closing {
			v.prompted[entry.ID] = entry.Conflict
			v.duplicate(downloadDraft{URL: entry.URL, Destination: entry.Conflict, TargetID: entry.ID}, entry.Conflict)
			break
		}
	}
}

func (v *downloadsView) accept(response desktopResponse) {
	if response.Revision < v.revision {
		return
	}
	v.revision = response.Revision
	v.management = response.Management
	v.apply(response.Jobs)
}

func (v *downloadsView) connect() {
	if v.client != nil || v.closing {
		return
	}
	v.busy = true
	v.reconnect.Hide()
	v.status.SetText("Loading saved downloads…")
	v.refresh()
	q, err := startConfiguredQueue(v.executable, v.settings.MaxActive, v.settings.Connections, 0, v.settings.path, func(line string) {
		if strings.HasPrefix(line, "Queue failed:") {
			fyne.Do(func() {
				v.status.SetText("Downloads unavailable. Close the other FluxGet CLI queue, then choose Try again. " + strings.TrimPrefix(line, "Queue failed: "))
			})
		}
		if strings.HasPrefix(line, "Cleanup failed:") || line == "Command is not valid for this job state." {
			fyne.Do(func() { v.status.SetText(line) })
		}
	}, func(err error) {
		fyne.Do(func() {
			v.client = nil
			v.ready = false
			v.busy = false
			v.refresh()
			if v.closing {
				v.finishClose()
			} else {
				v.reconnect.Show()
				if err != nil {
					v.status.SetText("Downloads service stopped. Close any other FluxGet CLI queue, then choose Try again.")
				}
			}
		})
	})
	if err != nil {
		v.busy = false
		v.status.SetText("Cannot prepare downloads: " + err.Error())
		v.reconnect.Show()
		v.refresh()
		return
	}
	v.client = q
	v.revision = 0
	historyCopy := append([]job(nil), v.history...)
	settingsCopy := *v.settings
	settingsCopy.Overrides = make(map[string]int64, len(v.settings.Overrides))
	for key, value := range v.settings.Overrides {
		settingsCopy.Overrides[key] = value
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		history, entries, err := migrateDownloads(ctx, q, historyCopy, &settingsCopy)
		fyne.Do(func() {
			if v.client != q {
				return
			}
			v.busy = false
			if err != nil {
				v.status.SetText("Saved downloads were kept, but setup could not finish: " + err.Error())
				_ = q.send("quit")
				return
			}
			v.history = history
			*v.settings = settingsCopy
			v.ready = true
			v.status.SetText("Ready. Add a download, or resume a saved one.")
			v.apply(entries)
			if v.closing {
				v.stopService()
				return
			}
			v.poll(q)
		})
	}()
}
func (v *downloadsView) poll(q *queueClient) {
	go func() {
		ticker := time.NewTicker(600 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-q.done:
				return
			case <-ticker.C:
				response, err := callDesktop(q, desktopRequest{Operation: "snapshot"})
				if err == nil {
					fyne.Do(func() {
						if v.client == q && !v.closing {
							v.accept(response)
						}
					})
				}
			}
		}
	}()
}
func (v *downloadsView) work(task func(*queueClient) (desktopResponse, error), done func(desktopResponse)) {
	if !v.ready || v.busy || v.closing {
		return
	}
	q := v.client
	v.busy = true
	v.refresh()
	go func() {
		response, err := task(q)
		fyne.Do(func() {
			if v.client != q {
				return
			}
			v.busy = false
			if err != nil {
				v.status.SetText(err.Error())
				dialog.ShowError(err, v.window)
			} else {
				v.accept(response)
				if done != nil {
					done(response)
				}
			}
			v.refresh()
			if v.closing {
				v.stopService()
			}
		})
	}()
}
func callDesktop(q *queueClient, request desktopRequest) (desktopResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return q.request(ctx, request)
}
func (v *downloadsView) configure() {
	configuration := v.settings.configuration()
	v.work(func(q *queueClient) (desktopResponse, error) {
		return callDesktop(q, configuration)
	}, nil)
}
func (v *downloadsView) command(action string) {
	v.batch(action, v.selectedRows(), nil)
}

func (v *downloadsView) perform(action string, id int) {
	if action == "retry" {
		delete(v.prompted, id)
	}
	v.status.SetText("Updating download…")
	v.work(func(q *queueClient) (desktopResponse, error) {
		if err := q.send(fmt.Sprintf("%s %d", action, id)); err != nil {
			return desktopResponse{}, err
		}
		return callDesktop(q, desktopRequest{Operation: "snapshot"})
	}, nil)
}
func (v *downloadsView) resumeSelected() {
	v.batch("start", v.selectedRows(), nil)
}
func (v *downloadsView) openSelected(folder bool) {
	rows := v.selectedRows()
	if len(rows) != 1 || rows[0].Entry.output() == "" {
		return
	}
	row := rows[0]
	path := row.Entry.output()
	if folder {
		path = filepath.Dir(path)
	}
	if err := v.app.OpenURL(&url.URL{Scheme: "file", Path: path}); err != nil {
		dialog.ShowError(err, v.window)
	}
}
func (v *downloadsView) moreActions() {
	rows := v.selectedRows()
	if len(rows) == 0 {
		return
	}
	cancel := fyne.NewMenuItem("Cancel downloads (keep files)", func() { v.batch("cancel", rows, nil) })
	cancel.Disabled = true
	for _, row := range rows {
		if batchAction(row.Entry, "cancel") != "" {
			cancel.Disabled = false
		}
	}
	remove := fyne.NewMenuItem("Remove from list (keep files)…", func() { v.confirmRemoval(rows, false) })
	deleteFiles := fyne.NewMenuItem("Delete files from disk…", func() { v.confirmRemoval(rows, true) })
	discard := fyne.NewMenuItem("Delete saved partial data only…", func() {
		dialog.ShowConfirm("Delete saved partial data?", "Selected active downloads will stop first. Only verified partial data is deleted; completed files and list entries are kept. This cannot be undone.", func(yes bool) {
			if yes {
				v.batch("discard", rows, nil)
			}
		}, v.window)
	})
	menu := widget.NewPopUpMenu(fyne.NewMenu("Downloads", cancel, remove, deleteFiles, fyne.NewMenuItemSeparator(), discard), v.window.Canvas())
	menu.ShowAtPosition(v.app.Driver().AbsolutePositionForObject(v.more).Add(fyne.NewPos(0, v.more.Size().Height)))
}
func (v *downloadsView) close(done func()) {
	if v.closing {
		return
	}
	v.closing = true
	v.closeDone = done
	v.status.SetText("Saving progress before closing…")
	v.refresh()
	if !v.busy {
		v.stopService()
	}
}
func (v *downloadsView) stopService() {
	if len(v.finishing) > 0 {
		return
	}
	if v.client == nil {
		v.finishClose()
		return
	}
	if err := v.client.send("quit"); err != nil {
		select {
		case <-v.client.done:
			v.finishClose()
		default:
			v.status.SetText("Could not close downloads safely: " + err.Error())
		}
	}
}
func (v *downloadsView) finishClose() {
	if v.closeDone != nil && len(v.finishing) == 0 {
		done := v.closeDone
		v.closeDone = nil
		done()
	}
}

type downloadDraft struct {
	URL, Destination     string
	AutomaticName, Later bool
	TargetID             int
	Replace              *replacement
}

func (v *downloadsView) addDialog() {
	link := widget.NewEntry()
	link.SetPlaceHolder("https://example.com/file.zip")
	folder := widget.NewEntry()
	folder.SetText(v.settings.Folder)
	folder.SetPlaceHolder("Your Downloads folder")
	name := widget.NewEntry()
	name.SetPlaceHolder("Use the server’s filename")
	note := widget.NewLabel("Download now starts when a slot is free. Download later stays paused until you resume it.")
	note.Wrapping = fyne.TextWrapWord
	browse := widget.NewButton("Browse…", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, v.window)
			} else if uri != nil {
				folder.SetText(uri.Path())
			}
		}, v.window)
	})
	var prompt dialog.Dialog
	submit := func(later bool) {
		raw, dir, filename := strings.TrimSpace(link.Text), strings.TrimSpace(folder.Text), strings.TrimSpace(name.Text)
		parsed, err := url.Parse(raw)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Hostname() == "" || parsed.User != nil {
			dialog.ShowInformation("Download link required", "Enter a public HTTP or HTTPS download link without credentials.", v.window)
			return
		}
		prompt.Hide()
		var destination string
		v.work(func(q *queueClient) (desktopResponse, error) {
			output, err := exec.Command(v.executable, append([]string{"-suggest-output"}, downloadArgs(raw, dir, filename)...)...).Output()
			if err != nil {
				return desktopResponse{}, fmt.Errorf("cannot choose the output path; check the filename and folder")
			}
			path, err := strconv.Unquote(strings.TrimSpace(string(output)))
			if err != nil {
				return desktopResponse{}, err
			}
			destination = path
			return callDesktop(q, desktopRequest{Operation: "snapshot"})
		}, func(response desktopResponse) {
			draft := downloadDraft{URL: raw, Destination: destination, AutomaticName: filename == "", Later: later}
			if _, err := os.Lstat(draft.Destination); err == nil {
				v.duplicate(draft, draft.Destination)
				return
			}
			for _, entry := range v.entries {
				if entry.Destination == draft.Destination {
					v.duplicate(draft, draft.Destination)
					return
				}
			}
			v.enqueue(draft)
		})
	}
	now := widget.NewButtonWithIcon("Download now", theme.DownloadIcon(), func() { submit(false) })
	now.Importance = widget.HighImportance
	later := widget.NewButton("Download later", func() { submit(true) })
	form := container.NewVBox(note, widget.NewForm(widget.NewFormItem("Download link", link), widget.NewFormItem("Folder", container.NewBorder(nil, nil, nil, browse, folder)), widget.NewFormItem("Filename", name)), container.NewHBox(now, later))
	prompt = dialog.NewCustom("Add download", "Cancel", form, v.window)
	prompt.Resize(fyne.NewSize(620, 360))
	prompt.Show()
	v.window.Canvas().Focus(link)
}
func (v *downloadsView) enqueue(draft downloadDraft) {
	state := "queued"
	if draft.Later || draft.Replace != nil {
		state = "paused"
	}
	entry := queueEntry{ID: draft.TargetID, URL: draft.URL, Destination: draft.Destination, AutomaticName: draft.AutomaticName, State: state}
	operation := "add"
	if draft.TargetID > 0 {
		operation = "retarget"
	}
	v.work(func(q *queueClient) (desktopResponse, error) {
		return callDesktop(q, desktopRequest{Operation: operation, Job: entry})
	}, func(response desktopResponse) {
		id := response.JobID
		if draft.TargetID > 0 {
			id = draft.TargetID
		}
		if draft.Replace != nil {
			index := -1
			for i, h := range v.history {
				if h.QueueID == id {
					index = i
					break
				}
			}
			h := job{URL: draft.URL, Path: draft.Destination, State: "Paused", Replace: draft.Replace, QueueID: id}
			if index < 0 {
				v.history = append(v.history, h)
			} else {
				v.history[index] = h
			}
			if err := saveHistory(v.history); err != nil {
				dialog.ShowError(err, v.window)
				return
			}
		}
		v.filters.SetSelected("All downloads")
		v.selected = fmt.Sprintf("queue:%d", id)
		v.refresh()
		if !draft.Later && (draft.TargetID > 0 || draft.Replace != nil) {
			v.perform("resume", id)
		}
		if draft.Later {
			v.status.SetText("Saved for later. Select the download and choose Resume when ready.")
		} else {
			v.status.SetText("Download added. It will start when a slot is free.")
		}
	})
}
func (v *downloadsView) availableCopy(path string) (string, error) {
	for i := 0; i < 10000; i++ {
		candidate, err := copyPath(path)
		if err != nil {
			return "", err
		}
		reserved := false
		for _, entry := range v.entries {
			if entry.Destination == candidate {
				reserved = true
				break
			}
		}
		if !reserved {
			return candidate, nil
		}
		path = candidate
	}
	return "", fmt.Errorf("choose another filename")
}
func (v *downloadsView) duplicate(draft downloadDraft, path string) {
	var prompt dialog.Dialog
	message := widget.NewLabel("This filename is already in use. Choose what to do; partial data is kept.")
	message.Wrapping = fyne.TextWrapWord
	keep := widget.NewButton("Keep existing file", func() {
		prompt.Hide()
		if draft.TargetID > 0 {
			v.selected = fmt.Sprintf("queue:%d", draft.TargetID)
			v.perform("cancel", draft.TargetID)
		}
		v.status.SetText("Existing file kept.")
	})
	copyButton := widget.NewButton("Save another copy", func() {
		prompt.Hide()
		copy, err := v.availableCopy(path)
		if err != nil {
			dialog.ShowError(err, v.window)
			return
		}
		draft.Destination = copy
		draft.AutomaticName = false
		draft.Replace = nil
		v.enqueue(draft)
	})
	replace := widget.NewButton("Replace existing file…", func() {
		prompt.Hide()
		dialog.ShowConfirm("Replace existing file?", "FluxGet downloads a separate copy first and replaces the original only after success, if the original is unchanged.", func(yes bool) {
			if !yes {
				return
			}
			original, err := replacementFor(path)
			if err != nil {
				dialog.ShowError(err, v.window)
				return
			}
			copy, err := v.availableCopy(path)
			if err != nil {
				dialog.ShowError(err, v.window)
				return
			}
			draft.Destination = copy
			draft.AutomaticName = false
			draft.Replace = original
			v.enqueue(draft)
		}, v.window)
	})
	if _, err := replacementFor(path); err != nil {
		replace.Disable()
	}
	prompt = dialog.NewCustom("Filename already used", "Cancel", container.NewVBox(message, widget.NewLabel(filepath.Base(path)), keep, copyButton, replace), v.window)
	prompt.Resize(fyne.NewSize(540, 320))
	prompt.Show()
}
func (v *downloadsView) publishReplacement(id int, path string) {
	if v.finishing[id] || v.client == nil {
		return
	}
	v.finishing[id] = true
	q := v.client
	go func() {
		response, err := callDesktop(q, desktopRequest{Operation: "published", Job: queueEntry{ID: id, OutputPath: path}})
		fyne.Do(func() {
			delete(v.finishing, id)
			if v.client == q && err == nil {
				v.accept(response)
			}
			if v.closing {
				v.stopService()
			}
		})
	}()
}
func (v *downloadsView) finishReplacement(index int, entry queueEntry) {
	h := v.history[index]
	h.Path = entry.Destination
	v.finishing[entry.ID] = true
	go func() {
		err := completeReplacement(&h)
		fyne.Do(func() {
			delete(v.finishing, entry.ID)
			if err != nil {
				h.State, h.Message = "Needs attention", err.Error()
			} else {
				h.State, h.Message = "Completed", "Existing file replaced."
			}
			v.history[index] = h
			if e := saveHistory(v.history); e != nil {
				v.status.SetText("Could not save replacement history: " + e.Error())
				dialog.ShowError(e, v.window)
			}
			if err == nil {
				v.publishReplacement(entry.ID, h.Path)
			}
			v.refresh()
			if v.closing {
				v.stopService()
			}
		})
	}()
}

// Apply scheduler configuration before reporting success. Persist only after
// acknowledgment, and restore the prior server policy if writing settings fails.
func (v *downloadsView) applySettings(next speedSettings, done func(error)) {
	if !v.ready || v.busy || v.closing || v.client == nil {
		done(fmt.Errorf("wait for downloads to finish loading"))
		return
	}
	q := v.client
	old := *v.settings
	next.Overrides = make(map[string]int64, len(v.settings.Overrides))
	for key, value := range v.settings.Overrides {
		next.Overrides[key] = value
	}
	v.busy = true
	v.refresh()
	go func() {
		response, err := callDesktop(q, next.configuration())
		if err == nil {
			if saveErr := next.save(); saveErr != nil {
				_, rollbackErr := callDesktop(q, old.configuration())
				err = fmt.Errorf("save: %w", saveErr)
				if rollbackErr != nil {
					err = fmt.Errorf("%v; restoring previous scheduler settings failed: %v", err, rollbackErr)
				}
			}
		}
		fyne.Do(func() {
			if v.client != q {
				done(fmt.Errorf("downloads service disconnected while saving settings"))
				return
			}
			v.busy = false
			done(err)
			if err == nil {
				v.accept(response)
			} else {
				v.refresh()
			}
		})
	}()
}
