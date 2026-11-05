package main

import (
	"context"
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
	jobs, discoveryErr := discoverSavedJobs(jobs)
	selected := -1
	var cancel context.CancelFunc
	closing := false
	link := widget.NewEntry()
	link.SetPlaceHolder("https://example.com/file.zip")
	directory := widget.NewEntry()
	directory.SetPlaceHolder("Your Downloads folder")
	name := widget.NewEntry()
	name.SetPlaceHolder("Choose automatically")
	status := widget.NewLabel("Ready when you are.")
	progress := widget.NewProgressBar()
	if discoveryErr != nil {
		status.SetText("Saved downloads could not be checked: " + discoveryErr.Error())
	}
	status.Wrapping = fyne.TextWrapWord
	var list *widget.List
	var resume *widget.Button
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
	resume = widget.NewButton("Resume selected", nil)
	resume.SetIcon(theme.MediaPlayIcon())
	resume.Disable()
	open := widget.NewButton("Open selected", func() {
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

	list = widget.NewList(func() int { return len(jobs) }, func() fyne.CanvasObject {
		return container.NewVBox(widget.NewLabelWithStyle("Filename", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), widget.NewLabel("Status"))
	}, func(id widget.ListItemID, object fyne.CanvasObject) {
		row := object.(*fyne.Container)
		j := jobs[id]
		label := "Waiting for filename"
		if j.Path != "" {
			label = filepath.Base(j.Path)
		}
		row.Objects[0].(*widget.Label).SetText(label)
		row.Objects[1].(*widget.Label).SetText(j.State + "  •  " + j.Message)
	})
	list.OnSelected = func(id widget.ListItemID) {
		selected = id
		j := jobs[id]
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
		progress.SetValue(0)
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
					if start, end := strings.Index(line, "("), strings.Index(line, "%)"); start >= 0 && end > start {
						if percent, e := strconv.ParseFloat(line[start+1:end], 64); e == nil {
							progress.SetValue(percent / 100)
						}
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
					progress.SetValue(1)
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
				list.UnselectAll()
				list.Select(index)
				status.SetText(j.State + ": " + j.Message)
				if closing {
					w.SetCloseIntercept(nil)
					w.Close()
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
		list.UnselectAll()
		list.Select(index)
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
			status.SetText("Enter a download link to continue.")
			w.Canvas().Focus(link)
			return
		}
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
		list.Select(index)
	}
	resume.OnTapped = func() {
		if selected < 0 || selected >= len(jobs) || jobs[selected].Path == "" {
			return
		}
		runJob(selected, []string{"-resume", "-out", jobs[selected].Path})
	}
	title := widget.NewLabelWithStyle("FluxGet", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	subtitle := widget.NewLabel("Your downloads, in one place")
	form := container.NewVBox(widget.NewLabel("Download link"), link, widget.NewLabel("Save to"), container.NewBorder(nil, nil, nil, browse, directory), widget.NewLabel("Filename (optional)"), name, container.NewHBox(start, pause))
	controls := container.NewVBox(widget.NewCard("New download", "Paste a direct link to get started", form), widget.NewCard("Activity", "", container.NewVBox(progress, status)))
	header := container.NewHBox(widget.NewLabelWithStyle("Downloads", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), resume, open)
	downloads := container.NewBorder(header, nil, nil, nil, list)
	split := container.NewHSplit(container.NewVScroll(controls), downloads)
	split.Offset = 0.42
	w.SetContent(container.NewBorder(container.NewPadded(container.NewVBox(title, subtitle, widget.NewSeparator())), nil, nil, nil, split))
	w.SetCloseIntercept(func() {
		if cancel != nil {
			closing = true
			status.SetText("Pausing before closing…")
			cancel()
			return
		}
		w.SetCloseIntercept(nil)
		w.Close()
	})
	if len(jobs) == 0 && discoveryErr == nil {
		status.SetText("No downloads yet. Add a link to begin.")
	}
	w.ShowAndRun()
}
