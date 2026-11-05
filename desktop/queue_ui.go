package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type queueEntry struct {
	ID          int    `json:"id"`
	State       string `json:"state"`
	Destination string `json:"destination"`
}

func newQueueView(w fyne.Window, settings *speedSettings) (fyne.CanvasObject, func(func())) {
	var client *queueClient
	var entries []queueEntry
	progress := make(map[int]string)
	selected := 0
	status := widget.NewLabel("Open the queue to manage saved jobs. Active CLI queues must be closed first.")
	status.Wrapping = fyne.TextWrapWord
	link := widget.NewEntry()
	link.SetPlaceHolder("https://example.com/file.zip")
	output := widget.NewEntry()
	output.SetPlaceHolder("Optional full path; defaults to Downloads")
	limit := widget.NewEntry()
	limit.SetText("5")
	speed := widget.NewEntry()
	speed.SetPlaceHolder("Unlimited")
	connect := widget.NewButton("Open queue", nil)
	disconnect := widget.NewButton("Close queue", nil)
	start := widget.NewButton("Start queue", nil)
	add := widget.NewButton("Add to queue", nil)
	importJob := widget.NewButton("Import saved download", nil)
	pause := widget.NewButton("Pause", nil)
	resume := widget.NewButton("Resume", nil)
	cancel := widget.NewButton("Cancel", nil)
	retry := widget.NewButton("Retry", nil)
	remove := widget.NewButton("Remove from queue", nil)
	open := widget.NewButton("Open file", nil)

	buttons := []*widget.Button{disconnect, start, add, importJob, pause, resume, cancel, retry, remove, open}
	for _, button := range buttons {
		button.Disable()
	}
	var list *widget.List
	refresh := func() {
		if list != nil {
			list.Refresh()
		}
	}
	buttonState := func() {
		for _, button := range []*widget.Button{pause, resume, cancel, retry, remove, open} {
			button.Disable()
		}
		if client == nil {
			return
		}
		for _, j := range entries {
			if j.ID != selected {
				continue
			}
			switch j.State {
			case "queued", "running":
				pause.Enable()
				cancel.Enable()
			case "paused":
				resume.Enable()
				cancel.Enable()
				remove.Enable()
			case "failed":
				retry.Enable()
				cancel.Enable()
				remove.Enable()
			case "canceled":
				retry.Enable()
				remove.Enable()
			case "completed":
				remove.Enable()
				open.Enable()
			}
		}
	}
	send := func(command string) bool {
		if client == nil {
			status.SetText("Open the queue first.")
			return false
		}
		if err := client.send(command); err != nil {
			status.SetText(err.Error())
			return false
		}
		if err := client.send("snapshot"); err != nil {
			status.SetText(err.Error())
		}
		return true
	}
	list = widget.NewList(func() int { return len(entries) }, func() fyne.CanvasObject {
		return container.NewVBox(container.NewBorder(nil, nil, nil, widget.NewButton("Limit: default", nil), widget.NewLabelWithStyle("Filename", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})), widget.NewLabel("Status"), progressLine())
	}, func(id widget.ListItemID, object fyne.CanvasObject) {
		j := entries[id]
		row := object.(*fyne.Container)
		line := row.Objects[0].(*fyne.Container)
		name := filepath.Base(j.Destination)
		line.Objects[0].(*widget.Label).SetText(name)
		bindLimitButton(line.Objects[1].(*widget.Button), settings, fmt.Sprintf("queue:%d", j.ID), name, func(key, filename string) {
			settings.editDownload(w, key, filename, refresh)
		})
		detail := j.State
		if j.State == "running" && progress[j.ID] != "" {
			detail += " • " + progress[j.ID]
		}
		value, known := progressValue(progress[j.ID])
		if known && j.State != "running" && j.State != "completed" {
			detail += fmt.Sprintf(" · %.1f%%", value*100)
		}
		row.Objects[1].(*widget.Label).SetText(fmt.Sprintf("#%d  %s", j.ID, detail))
		if j.State == "completed" {
			value, known = 1, true
		}
		showProgress(row.Objects[2].(*widget.ProgressBar), value, known)
	})
	list.OnSelected = func(id widget.ListItemID) {
		selected = entries[id].ID
		status.SetText(entries[id].Destination)
		buttonState()
	}
	connect.OnTapped = func() {
		maxActive, err := strconv.Atoi(strings.TrimSpace(limit.Text))
		if err != nil || maxActive < 1 || maxActive > 64 {
			status.SetText("Simultaneous downloads must be between 1 and 64.")
			return
		}
		var speedLimit int64
		if value := strings.TrimSpace(speed.Text); value != "" {
			speedLimit, err = strconv.ParseInt(value, 10, 64)
			if err != nil || speedLimit < 0 || speedLimit > 1<<30 {
				status.SetText("Speed limit must be a non-negative number of KiB/s.")
				return
			}
		}
		executable, err := cliPath()
		if err != nil {
			status.SetText(err.Error())
			return
		}
		connect.Disable()
		status.SetText("Opening queue…")
		var failure string
		q, err := startQueueWithSettings(executable, maxActive, speedLimit, settings.path, func(line string) {
			if strings.HasPrefix(line, "Queue failed:") {
				failure = line
			}
			fyne.Do(func() {
				if strings.HasPrefix(line, "QUEUE_JSON ") {
					var snapshot []queueEntry
					if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "QUEUE_JSON ")), &snapshot); err != nil {
						status.SetText("Invalid queue response: " + err.Error())
						return
					}
					changed := len(entries) != len(snapshot)
					if !changed {
						for i := range entries {
							if entries[i].ID != snapshot[i].ID {
								changed = true
								break
							}
						}
					}
					oldSelection := selected
					entries = snapshot

					refresh()
					if changed {
						list.UnselectAll()
						selected = 0
						for i, entry := range entries {
							if entry.ID == oldSelection {
								list.Select(i)
								break
							}
						}
					}
					buttonState()
					return
				}
				if strings.HasPrefix(line, "Queue ready.") {
					status.SetText("Queue open. Add jobs, then choose Start queue.")
					start.Enable()
					disconnect.Enable()
					add.Enable()
					importJob.Enable()
					send("snapshot")
					return
				}
				var id int
				if _, err := fmt.Sscanf(line, "Job %d:", &id); err == nil && strings.Contains(line, "Downloaded ") {
					progress[id] = strings.TrimSpace(strings.TrimPrefix(line, fmt.Sprintf("Job %d:", id)))
					refresh()
					return
				}
				if line != "" {
					status.SetText(line)
				}
			})
		}, func(err error) {
			fyne.Do(func() {
				client = nil
				connect.Enable()
				limit.Enable()
				speed.Enable()
				for _, button := range buttons {
					button.Disable()
				}
				if failure != "" {
					status.SetText(failure)
				} else if err != nil {
					status.SetText("Queue stopped: " + err.Error())
				} else {
					status.SetText("Queue closed; active jobs were paused.")
				}
			})
		})
		if err != nil {
			connect.Enable()
			status.SetText("Cannot start queue: " + err.Error())
			return
		}
		client = q
		limit.Disable()
		speed.Disable()
		go func() {
			ticker := time.NewTicker(1500 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					_ = q.send("snapshot")
				case <-q.done:
					return
				}
			}
		}()
	}
	disconnect.OnTapped = func() {
		if client == nil {
			return
		}
		if err := client.send("quit"); err != nil {
			status.SetText(err.Error())
			return
		}
		for _, button := range buttons {
			button.Disable()
		}
		status.SetText("Pausing queue before closing…")
	}
	start.OnTapped = func() {
		if send("start") {
			status.SetText("Starting queue…")
		}
	}
	add.OnTapped = func() {
		raw, dest := strings.TrimSpace(link.Text), strings.TrimSpace(output.Text)
		if raw == "" {
			status.SetText("Enter a download link.")
			return
		}
		if client == nil {
			status.SetText("Open the queue first.")
			return
		}
		q := client
		add.Disable()
		status.SetText("Preparing queue job…")
		go func() {
			if dest == "" {
				cli, err := cliPath()
				if err != nil {
					fyne.Do(func() {
						if client == q {
							add.Enable()
							status.SetText(err.Error())
						}
					})
					return
				}
				result, err := exec.Command(cli, "-suggest-output", "-url", raw).Output()
				if err != nil {
					fyne.Do(func() {
						if client == q {
							add.Enable()
							status.SetText("Cannot choose a filename. Check the link or enter a full path.")
						}
					})
					return
				}
				dest, err = strconv.Unquote(strings.TrimSpace(string(result)))
				if err != nil {
					fyne.Do(func() {
						if client == q {
							add.Enable()
							status.SetText("Cannot read suggested filename.")
						}
					})
					return
				}
			}
			payload, err := json.Marshal(struct {
				URL         string `json:"url"`
				Destination string `json:"destination"`
			}{raw, dest})
			if err == nil {
				err = q.send("add-json " + string(payload))
			}
			if err == nil {
				err = q.send("snapshot")
			}
			fyne.Do(func() {
				if client == q {
					add.Enable()
				}
				if err != nil {
					status.SetText(err.Error())
				}
			})
		}()
	}
	importJob.OnTapped = func() {
		dest := strings.TrimSpace(output.Text)
		if dest == "" {
			status.SetText("Enter the saved download's full output path to import it.")
			return
		}
		payload, _ := json.Marshal(struct {
			Destination string `json:"destination"`
		}{dest})
		send("import-json " + string(payload))
	}
	pause.OnTapped = func() { send(fmt.Sprintf("pause %d", selected)) }
	resume.OnTapped = func() { send(fmt.Sprintf("resume %d", selected)); send("start") }
	cancel.OnTapped = func() { send(fmt.Sprintf("cancel %d", selected)) }
	retry.OnTapped = func() { send(fmt.Sprintf("retry %d", selected)); send("start") }
	remove.OnTapped = func() {
		id := selected
		dialog.ShowConfirm("Remove job?", "Remove this job from the queue? Downloaded and partial files are kept.", func(ok bool) {
			if ok {
				send(fmt.Sprintf("remove %d", id))
			}
		}, w)
	}
	open.OnTapped = func() {
		for _, j := range entries {
			if j.ID == selected && j.State == "completed" {
				if err := fyne.CurrentApp().OpenURL(&url.URL{Scheme: "file", Path: j.Destination}); err != nil {
					dialog.ShowError(err, w)
				}
				return
			}
		}
	}
	form := container.NewVBox(widget.NewLabel("Simultaneous downloads (1–64)"), limit, widget.NewLabel("Total speed limit in KiB/s (optional)"), speed, container.NewHBox(connect, disconnect), widget.NewSeparator(), widget.NewLabel("Download link"), link, widget.NewLabel("Save as (optional full path)"), output, add, importJob, start, widget.NewSeparator(), status)
	header := container.NewVBox(widget.NewLabelWithStyle("Queue", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), container.NewHBox(pause, resume, cancel), container.NewHBox(retry, open, remove))
	split := container.NewHSplit(container.NewVScroll(form), container.NewBorder(header, nil, nil, nil, list))
	split.Offset = 0.4
	closeQueue := func(done func()) {
		if client == nil {
			done()
			return
		}
		q := client
		if err := q.send("quit"); err != nil {
			status.SetText("Cannot close queue: " + err.Error())
			return
		}
		status.SetText("Pausing queue before closing…")
		go func() { <-q.done; fyne.Do(done) }()
	}
	return split, closeQueue
}
