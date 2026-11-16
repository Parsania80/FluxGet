package main

import (
	"fmt"
	"os"
	"path/filepath"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

type duplicateChoice struct {
	Draft      *downloadDraft
	ExistingID int
	Keep       bool
}

func (v *downloadsView) downloadConflict(draft downloadDraft, sameURL bool) string {
	for _, entry := range v.entries {
		if entry.ID == draft.TargetID {
			continue
		}
		if entry.Destination == draft.Destination || entry.output() == draft.Destination {
			return draft.Destination
		}
	}
	for _, entry := range v.entries {
		if entry.ID != draft.TargetID && sameURL && entry.Media == nil && entry.URL == draft.URL &&
			(entry.State == "running" || entry.State == "queued" || entry.State == "paused") {
			return entry.Destination
		}
	}
	for _, path := range []string{draft.Destination, draft.Destination + ".part"} {
		if _, err := os.Lstat(path); err == nil {
			return draft.Destination
		}
	}
	return ""
}

func (v *downloadsView) saveDraftReplacement(draft downloadDraft, id int) error {
	if draft.Replace == nil {
		return nil
	}
	h := job{URL: draft.URL, Path: draft.Destination, State: "Paused", Replace: draft.Replace, QueueID: id}
	for i := range v.history {
		if v.history[i].QueueID == id {
			v.history[i] = h
			return saveHistory(v.history)
		}
	}
	v.history = append(v.history, h)
	return saveHistory(v.history)
}

func (v *downloadsView) continueDuplicate(id int) {
	v.filters.SetSelected("All downloads")
	v.selected = fmt.Sprintf("queue:%d", id)
	for _, entry := range v.entries {
		if entry.ID == id && entry.State == "paused" {
			v.perform("resume", id)
			return
		}
	}
	v.status.SetText("Continuing the existing download.")
	v.refresh()
}

// Both normal additions and browser handoffs use these choices. The browser
// supplies its own admission callback so every new copy retains its receipt.
func (v *downloadsView) resolveDuplicate(draft downloadDraft, path string, separate bool, decide func(duplicateChoice)) fyne.Window {
	parent := v.window
	if separate {
		parent = v.app.NewWindow("Download already exists")
	}
	var hide func()
	answered := false
	finish := func(choice duplicateChoice) {
		if answered {
			return
		}
		answered = true
		hide()
		decide(choice)
	}
	message := widget.NewLabel("This download or filename is already in use. Choose what to do; partial data is kept.")
	message.Wrapping = fyne.TextWrapWord
	choices := container.NewVBox(message, widget.NewLabel(filepath.Base(path)))
	for _, entry := range v.entries {
		if draft.TargetID == 0 && entry.Destination == path && !entry.BrowserPending &&
			(entry.State == "running" || entry.State == "queued" || entry.State == "paused") {
			id := entry.ID
			choices.Add(widget.NewButton("Continue existing download", func() { finish(duplicateChoice{ExistingID: id}) }))
			break
		}
	}
	choices.Add(widget.NewButton("Keep existing file", func() { finish(duplicateChoice{Keep: true}) }))
	chooseCopy := func(original *replacement) {
		copy, err := v.availableCopy(path)
		if err != nil {
			dialog.ShowError(err, parent)
			return
		}
		draft.Destination, draft.AutomaticName, draft.SortFiles, draft.Replace = copy, false, false, original
		finish(duplicateChoice{Draft: &draft})
	}
	choices.Add(widget.NewButton("Save another copy", func() { chooseCopy(nil) }))
	replace := widget.NewButton("Replace existing file…", func() {
		dialog.ShowConfirm("Replace existing file?", "FluxGet downloads a separate copy first and replaces the original only after success, if the original is unchanged.", func(yes bool) {
			if yes && !answered {
				original, err := replacementFor(path)
				if err != nil {
					dialog.ShowError(err, parent)
					return
				}
				chooseCopy(original)
			}
		}, parent)
	})
	if _, err := replacementFor(path); err != nil {
		replace.Disable()
	}
	choices.Add(replace)
	if separate {
		choices.Add(widget.NewButton("Cancel", func() { finish(duplicateChoice{}) }))
		hide = func() { parent.SetCloseIntercept(nil); parent.Close() }
		parent.SetCloseIntercept(func() { finish(duplicateChoice{}) })
		parent.SetOnClosed(func() {
			if !answered {
				answered = true
				decide(duplicateChoice{})
			}
		})
		parent.SetContent(container.NewPadded(choices))
		parent.Resize(fyne.NewSize(540, 340))
		parent.CenterOnScreen()
		if native, ok := parent.(desktop.Window); ok {
			native.RequestAlwaysOnTop()
		}
		parent.Show()
		parent.RequestFocus()
		return parent
	}
	prompt := dialog.NewCustom("Filename already used", "Cancel", choices, parent)
	hide = prompt.Hide
	prompt.SetOnClosed(func() { finish(duplicateChoice{}) })
	prompt.Resize(fyne.NewSize(540, 340))
	prompt.Show()
	return nil
}
