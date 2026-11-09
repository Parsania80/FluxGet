package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

func (v *downloadsView) selectedRows() []downloadRow {
	var selected []downloadRow
	for _, row := range v.rows() {
		if v.checked[row.Key] || (len(v.checked) == 0 && row.Key == v.selected) {
			selected = append(selected, row)
		}
	}
	return selected
}

func batchAction(j queueEntry, action string) string {
	state := strings.ToLower(downloadState(j.State))
	switch action {
	case "start":
		if state == "paused" {
			return "resume"
		}
		if state == "failed" || state == "canceled" {
			return "retry"
		}
	case "pause":
		if state == "downloading" || state == "waiting" {
			return "pause"
		}
	case "retry":
		if state == "failed" || state == "canceled" {
			return "retry"
		}
	case "cancel":
		if state == "downloading" || state == "waiting" || state == "paused" || state == "failed" {
			return "cancel"
		}
	case "remove", "delete":
		return action
	case "discard":
		if state != "completed" && state != "already exists" {
			return action
		}
	}
	return ""
}

func (v *downloadsView) removalMenu() {
	rows := v.selectedRows()
	if len(rows) == 0 {
		return
	}
	menu := widget.NewPopUpMenu(fyne.NewMenu("Remove downloads",
		fyne.NewMenuItem("Remove from list (keep files)…", func() { v.confirmRemoval(rows, false) }),
		fyne.NewMenuItem("Delete files from disk…", func() { v.confirmRemoval(rows, true) }),
	), v.window.Canvas())
	menu.ShowAtPosition(v.app.Driver().AbsolutePositionForObject(v.remove).Add(fyne.NewPos(0, v.remove.Size().Height)))
}

func (v *downloadsView) confirmRemoval(rows []downloadRow, deleteFiles bool) {
	files := make(map[string]*replacement)
	paths := make([]string, 0, len(rows))
	for _, row := range rows {
		paths = append(paths, row.Entry.output())
		if deleteFiles && downloadState(row.Entry.State) == "Completed" {
			file, err := replacementFor(row.Entry.output())
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				dialog.ShowError(fmt.Errorf("cannot prepare %s for deletion: %w", row.Entry.output(), err), v.window)
				return
			}
			files[row.Key] = file
		}
	}
	action, confirm := "remove", "Remove entries"
	message := "Remove the selected entries from the list? Downloaded files and saved partial data will be kept. Selected active downloads will stop first."
	if deleteFiles {
		action, confirm = "delete", "Delete files"
		message = "Permanently delete the selected downloaded files or verified partial data, and remove their entries? Selected active downloads will stop first. This cannot be undone."
	}
	label := widget.NewLabel(message)
	label.Wrapping = fyne.TextWrapWord
	pathList := widget.NewLabel(strings.Join(paths, "\n"))
	pathList.Wrapping = fyne.TextWrapWord
	scroll := container.NewVScroll(pathList)
	scroll.SetMinSize(fyne.NewSize(560, 140))
	d := dialog.NewCustomConfirm(fmt.Sprintf("Remove %d download(s)?", len(rows)), confirm, "Cancel", container.NewVBox(label, scroll), func(yes bool) {
		if yes {
			v.batch(action, rows, files)
		}
	}, v.window)
	d.Show()
}

// Wait for the owner to finish writers and checkpoints before any removal.
func stopForRemoval(q *queueClient, id int) (queueEntry, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	stopping := false
	for {
		response, err := q.request(ctx, desktopRequest{Operation: "snapshot"})
		if err != nil {
			return queueEntry{}, err
		}
		var entry *queueEntry
		for i := range response.Jobs {
			if response.Jobs[i].ID == id {
				entry = &response.Jobs[i]
				break
			}
		}
		if entry == nil {
			return queueEntry{}, errors.New("download no longer exists")
		}
		if entry.State != "running" && entry.State != "queued" {
			return *entry, nil
		}
		if !stopping {
			_, err = q.request(ctx, desktopRequest{Operation: "action", Action: "pause", Job: *entry})
			if err != nil {
				return queueEntry{}, err
			}
			stopping = true
		}
		select {
		case <-ctx.Done():
			return queueEntry{}, errors.New("download is still stopping; files kept, try again shortly")
		case <-ticker.C:
		}
	}
}

func (v *downloadsView) batch(action string, rows []downloadRow, files map[string]*replacement) {
	if len(rows) == 0 || !v.ready || v.busy || v.closing {
		return
	}
	blocked := make(map[string]bool)
	settingsCopy := *v.settings
	settingsCopy.Overrides = make(map[string]int64, len(v.settings.Overrides))
	for key, limit := range v.settings.Overrides {
		settingsCopy.Overrides[key] = limit
	}
	for _, row := range rows {
		if (action == "remove" || action == "delete" || action == "discard") && ((row.HistoryIndex >= 0 && v.history[row.HistoryIndex].Replace != nil) || v.finishing[row.Entry.ID]) {
			blocked[row.Key] = true
		}
		if action == "start" || action == "retry" {
			delete(v.prompted, row.Entry.ID)
		}
	}
	adopted := make(map[string]int)
	removed := make(map[string]bool)
	var failures []string
	updated, skipped := 0, 0
	v.status.SetText(fmt.Sprintf("Updating %d selected download(s)…", len(rows)))
	v.work(func(q *queueClient) (desktopResponse, error) {
		for _, row := range rows {
			j := row.Entry
			if blocked[row.Key] {
				failures = append(failures, filepath.Base(j.output())+": finish the pending file replacement before removal")
				continue
			}
			command := batchAction(j, action)
			if command == "" {
				skipped++
				continue
			}
			var err error
			if j.ID == 0 && action != "remove" {
				j.State = strings.ToLower(downloadState(j.State))
				response, e := callDesktop(q, desktopRequest{Operation: "adopt", Job: j})
				err = e
				if err == nil {
					j.ID = response.JobID
					adopted[row.Key] = j.ID
					if limit := settingsCopy.Overrides[row.Key]; limit > 0 {
						settingsCopy.Overrides[fmt.Sprintf("queue:%d", j.ID)] = limit
						err = settingsCopy.save()
					}
				}
			}
			if err == nil && j.ID != 0 {
				if action == "delete" || action == "remove" || action == "discard" {
					_, err = stopForRemoval(q, j.ID)
				}
				if err == nil {
					_, err = callDesktop(q, desktopRequest{Operation: "action", Action: command, Job: j, File: files[row.Key]})
				}
			}
			if err != nil {
				failures = append(failures, filepath.Base(j.output())+": "+err.Error())
				continue
			}
			updated++
			if action == "remove" || action == "delete" {
				removed[row.Key] = true
			}
		}
		return callDesktop(q, desktopRequest{Operation: "snapshot"})
	}, func(response desktopResponse) {
		for _, row := range rows {
			index := row.HistoryIndex
			if index < 0 && removed[row.Key] {
				v.history = append(v.history, job{URL: row.Entry.URL, Path: row.Entry.Destination, QueueID: row.Entry.ID, Archived: true})
				index = len(v.history) - 1
			}
			if index >= 0 {
				if id := adopted[row.Key]; id > 0 {
					v.history[index].QueueID = id
					if limit := v.settings.Overrides[row.Key]; limit > 0 {
						v.settings.Overrides[fmt.Sprintf("queue:%d", id)] = limit
					}
					if v.selected == row.Key {
						v.selected = fmt.Sprintf("queue:%d", id)
					}
				}
				if removed[row.Key] {
					v.history[index].Archived = true
				}
			}
			if removed[row.Key] {
				delete(v.checked, row.Key)
			}
		}
		if len(adopted) > 0 || len(removed) > 0 {
			if err := saveHistory(v.history); err != nil {
				failures = append(failures, "Save history: "+err.Error())
			}
		}
		if len(adopted) > 0 {
			if err := v.settings.save(); err != nil {
				failures = append(failures, "Save speed limits: "+err.Error())
			}
		}
		v.status.SetText(fmt.Sprintf("%d updated · %d skipped · %d failed", updated, skipped, len(failures)))
		if len(failures) > 0 {
			label := widget.NewLabel(strings.Join(failures, "\n"))
			label.Wrapping = fyne.TextWrapWord
			scroll := container.NewVScroll(label)
			scroll.SetMinSize(fyne.NewSize(560, 180))
			dialog.ShowCustom("Some downloads could not be updated", "Close", scroll, v.window)
		}
	})
}
