package main

import (
	"context"
	"fmt"
	"image/color"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

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

func jobVisible(category, state string) bool {
	switch category {
	case "All downloads":
		return true
	case "Finished":
		return state == "Completed"
	case "Unfinished":
		return state != "Completed"
	default:
		return false
	}
}

func main() {
	a := app.NewWithID("io.github.parsania80.fluxget")
	a.Settings().SetTheme(fluxTheme{theme.DefaultTheme()})
	w := a.NewWindow("FluxGet")
	w.Resize(fyne.NewSize(980, 640))
	lock, err := lockHistory()
	if err != nil {
		w.SetContent(widget.NewLabel("Cannot open FluxGet: " + err.Error()))
		w.ShowAndRun()
		return
	}
	defer lock.Close()
	jobs, err := loadHistory()
	if err != nil {
		w.SetContent(widget.NewLabel("Cannot open download history: " + err.Error()))
		w.ShowAndRun()
		return
	}
	settings, err := loadSpeedSettings()
	if err != nil {
		w.SetContent(widget.NewLabel("Cannot open speed settings: " + err.Error()))
		w.ShowAndRun()
		return
	}
	jobs, discoveryErr := discoverSavedJobs(jobs)
	selected := -1
	var cancel context.CancelFunc
	closing := false
	var finishClose func()
	link := widget.NewEntry()
	link.SetPlaceHolder("https://example.com/file.zip")
	directory := widget.NewEntry()
	directory.SetPlaceHolder("Your Downloads folder")
	name := widget.NewEntry()
	name.SetPlaceHolder("Choose automatically")
	status := widget.NewLabel("Ready when you are.")
	if discoveryErr != nil {
		status.SetText("Saved downloads could not be checked: " + discoveryErr.Error())
	}
	status.Wrapping = fyne.TextWrapWord
	var list *widget.List
	var resume *widget.Button
	var newDownload dialog.Dialog
	category := "All downloads"
	visible := func() []int {
		var indexes []int
		for i, j := range jobs {
			if jobVisible(category, j.State) {
				indexes = append(indexes, i)
			}
		}
		return indexes
	}
	selectJob := func(index int) {
		list.UnselectAll()
		for row, jobIndex := range visible() {
			if jobIndex == index {
				list.Select(row)
				return
			}
		}
	}
	refresh := func() {
		if list != nil {
			list.Refresh()
		}
	}
	persist := func() bool {
		if err := saveHistory(jobs); err != nil {
			status.SetText("Could not save download history: " + err.Error())
			return false
		}
		return true
	}
	start := widget.NewButton("Start download", nil)
	start.SetIcon(theme.DownloadIcon())
	start.Importance = widget.HighImportance
	pause := widget.NewButton("Pause", func() {
		if cancel != nil {
			status.SetText("Pausing safely…")
			cancel()
		}
	})
	pause.SetIcon(theme.MediaPauseIcon())
	pause.Disable()
	resume = widget.NewButton("Resume", nil)
	resume.SetIcon(theme.MediaPlayIcon())
	resume.Disable()
	open := widget.NewButton("Open file", func() {
		if selected < 0 || selected >= len(jobs) || jobs[selected].Path == "" {
			return
		}
		if err := a.OpenURL(&url.URL{Scheme: "file", Path: jobs[selected].Path}); err != nil {
			dialog.ShowError(err, w)
		}
	})
	open.Disable()
	browse := widget.NewButton("Browse…", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
			} else if uri != nil {
				directory.SetText(uri.Path())
			}
		}, w)
	})
	browse.SetIcon(theme.FolderOpenIcon())

	list = widget.NewList(func() int { return len(visible()) }, func() fyne.CanvasObject {
		return container.NewVBox(container.NewBorder(nil, nil, nil, widget.NewButton("Limit: default", nil), container.NewGridWithColumns(2, widget.NewLabel("Filename"), widget.NewLabel("Status"))), progressLine())
	}, func(id widget.ListItemID, object fyne.CanvasObject) {
		row := object.(*fyne.Container)
		index := visible()[id]
		j := jobs[index]
		label := "Waiting for filename"
		if j.Path != "" {
			label = filepath.Base(j.Path)
		}
		line := row.Objects[0].(*fyne.Container)
		columns := line.Objects[0].(*fyne.Container)
		bindLimitButton(line.Objects[1].(*widget.Button), settings, fmt.Sprintf("download:%d", index), label, func(key, filename string) {
			settings.editDownload(w, key, filename, refresh)
		})
		columns.Objects[0].(*widget.Label).SetText(label)
		detail := j.State + "  ·  " + j.Message
		if j.Progress != nil && j.State != "Completed" {
			detail = fmt.Sprintf("%s  ·  %.1f%%  ·  %s", j.State, *j.Progress*100, j.Message)
		}
		columns.Objects[1].(*widget.Label).SetText(detail)
		bar := row.Objects[1].(*widget.ProgressBar)
		if j.State == "Completed" {
			showProgress(bar, 1, true)
		} else if j.Progress != nil {
			showProgress(bar, *j.Progress, true)
		} else {
			showProgress(bar, 0, false)
		}
	})
	list.OnSelected = func(id widget.ListItemID) {
		selected = visible()[id]
		j := jobs[selected]
		status.SetText(j.State + ": " + j.Message)
		_, partialErr := os.Lstat(j.Path + ".part")
		if cancel == nil && j.Path != "" && (j.State == "Paused" || j.State == "Interrupted" || (j.State == "Failed" && partialErr == nil)) {
			resume.Enable()
		} else {
			resume.Disable()
		}
		if (j.State == "Completed" || j.State == "Already exists") && j.Path != "" {
			open.Enable()
		} else {
			open.Disable()
		}
	}

	var runJob func(int, []string)
	var duplicate func(int)
	runJob = func(index int, args []string) {
		if cancel != nil {
			return
		}
		args = append(args, "-live-speed-file", settings.path, "-live-speed-key", fmt.Sprintf("download:%d", index))
		jobs[index].State, jobs[index].Message = "Downloading", "Connecting…"
		if !persist() {
			return
		}
		refresh()
		ctx, stop := context.WithCancel(context.Background())
		cancel = stop
		start.Disable()
		resume.Disable()
		pause.Enable()
		status.SetText("Connecting…")
		go func() {
			last := ""
			err := download(ctx, args, func(line string) {
				last = line
				fyne.Do(func() {
					if strings.HasPrefix(line, "Saving to ") {
						if path, e := strconv.Unquote(strings.TrimPrefix(line, "Saving to ")); e == nil {
							if abs, e := filepath.Abs(path); e == nil {
								jobs[index].Path = abs
								if !persist() {
									stop()
								}
							}
						}
					}
					jobs[index].Message = line
					if value, known := progressValue(line); known {
						jobs[index].Progress = &value
					}
					status.SetText(line)
					refresh()
				})
			})
			fyne.Do(func() {
				cancel = nil
				pause.Disable()
				start.Enable()
				j := &jobs[index]
				switch {
				case err != nil && ctx.Err() != nil:
					if j.Path == "" {
						j.State, j.Message = "Stopped", "No download data was saved; start again."
					} else {
						j.State, j.Message = "Paused", "Resume uses saved data when the server supports it."
					}
				case err != nil:
					j.State, j.Message = "Failed", last
					if last == "" {
						j.Message = err.Error()
					}
				default:
					j.State, j.Message = "Completed", "Download complete."
					if j.Replace != nil {
						if e := completeReplacement(j); e != nil {
							j.State, j.Message = "Needs attention", e.Error()
						} else {
							j.Message = "Existing file replaced."
						}
					}
				}
				persist()
				refresh()
				selectJob(index)
				status.SetText(j.State + ": " + j.Message)
				if closing {
					finishClose()
					return
				}
				if err != nil && ctx.Err() == nil && j.Path != "" && j.URL != "" {
					if info, e := os.Lstat(j.Path); e == nil && info.Mode().IsRegular() {
						duplicate(index)
					}
				}
			})
		}()
	}
	duplicate = func(index int) {
		path := jobs[index].Path
		jobs[index].State, jobs[index].Message = "Already exists", "Choose what to do with the existing file."
		persist()
		refresh()
		selectJob(index)
		message := widget.NewLabel("A file with this name already exists. What would you like to do?")
		message.Wrapping = fyne.TextWrapWord
		var prompt dialog.Dialog
		keep := widget.NewButton("Keep existing file", func() { prompt.Hide(); status.SetText("Existing file kept.") })
		copyButton := widget.NewButton("Save another copy", func() {
			prompt.Hide()
			newPath, err := copyPath(path)
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
			jobs[index].Path = newPath
			jobs[index].Replace = nil
			runJob(index, []string{"-url", jobs[index].URL, "-out", newPath})
		})
		replaceButton := widget.NewButton("Replace existing file…", func() {
			prompt.Hide()
			dialog.ShowConfirm("Replace existing file?", "The original will be replaced only after the new download completes. This cannot be undone.", func(yes bool) {
				if !yes {
					return
				}
				original, err := replacementFor(path)
				if err != nil {
					dialog.ShowInformation("Cannot replace", "The original file is unavailable or is not a regular file.", w)
					return
				}
				newPath, err := copyPath(path)
				if err != nil {
					dialog.ShowError(err, w)
					return
				}
				jobs[index].Path = newPath
				jobs[index].Replace = original
				runJob(index, []string{"-url", jobs[index].URL, "-out", newPath})
			}, w)
		})
		prompt = dialog.NewCustom("File already exists", "Close", container.NewVBox(message, widget.NewLabel(filepath.Base(path)), keep, copyButton, replaceButton), w)
		prompt.Show()
	}
	start.OnTapped = func() {
		raw := strings.TrimSpace(link.Text)
		if raw == "" {
			dialog.ShowInformation("Download link required", "Enter a download link to continue.", w)
			w.Canvas().Focus(link)
			return
		}

		newDownload.Hide()
		if strings.TrimSpace(directory.Text) == "" && strings.TrimSpace(name.Text) == "" {
			for _, previous := range jobs {
				if previous.URL == raw && previous.State == "Completed" && previous.Path != "" {
					if info, e := os.Lstat(previous.Path); e == nil && info.Mode().IsRegular() {
						jobs = append(jobs, job{URL: raw, Path: previous.Path})
						duplicate(len(jobs) - 1)
						return
					}
				}
			}
		}
		jobs = append(jobs, job{URL: raw})
		index := len(jobs) - 1
		runJob(index, downloadArgs(raw, strings.TrimSpace(directory.Text), strings.TrimSpace(name.Text)))
		selectJob(index)
	}
	resume.OnTapped = func() {
		if selected < 0 || selected >= len(jobs) || jobs[selected].Path == "" {
			return
		}
		runJob(selected, []string{"-resume", "-out", jobs[selected].Path})
	}
	form := container.NewVBox(widget.NewLabel("Download link"), link, widget.NewLabel("Save to"), container.NewBorder(nil, nil, nil, browse, directory), widget.NewLabel("Filename (optional)"), name, start)
	newDownload = dialog.NewCustom("Add a download", "Close", form, w)
	newDownload.Resize(fyne.NewSize(560, 440))
	add := widget.NewButtonWithIcon("Add URL", theme.ContentAddIcon(), func() { newDownload.Show(); w.Canvas().Focus(link) })
	add.Importance = widget.HighImportance

	open.SetIcon(theme.FolderOpenIcon())
	toolbar := container.NewHBox(add, resume, pause, open)
	categories := widget.NewRadioGroup([]string{"All downloads", "Unfinished", "Finished"}, func(value string) {
		category = value
		selected = -1
		list.UnselectAll()
		resume.Disable()
		open.Disable()
		refresh()
	})
	categories.SetSelected(category)
	sidebar := container.NewPadded(container.NewVBox(widget.NewLabelWithStyle("DOWNLOADS", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), categories))
	listHeader := container.NewPadded(container.NewGridWithColumns(2, widget.NewLabelWithStyle("File name", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), widget.NewLabelWithStyle("Status / Progress", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})))
	downloads := container.NewBorder(listHeader, nil, nil, nil, list)
	split := container.NewHSplit(sidebar, downloads)
	split.Offset = 0.2
	queueView, closeQueue := newQueueView(w, settings)
	finishClose = func() { closeQueue(func() { w.SetCloseIntercept(nil); w.Close() }) }
	tabs := container.NewAppTabs(container.NewTabItem("Downloads", split), container.NewTabItem("Queue", queueView), container.NewTabItem("Settings", settings.page(w)))
	activity := container.NewVBox(widget.NewSeparator(), status)
	w.SetContent(container.NewBorder(container.NewPadded(toolbar), container.NewPadded(activity), nil, nil, tabs))
	w.SetCloseIntercept(func() {
		if cancel != nil {
			closing = true
			status.SetText("Pausing before closing…")
			cancel()
			return
		}
		finishClose()
	})
	if len(jobs) == 0 && discoveryErr == nil {
		status.SetText("No downloads yet. Add a link to begin.")
	}
	w.ShowAndRun()
}
