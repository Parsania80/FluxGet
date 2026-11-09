package main

import (
	"fmt"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"
)

type downloadSegment struct {
	Start      int64  `json:"start"`
	End        int64  `json:"end"`
	Downloaded int64  `json:"downloaded"`
	State      string `json:"state"`
}

func segmentState(state string) string {
	switch state {
	case "waiting":
		return "Waiting"
	case "connecting":
		return "Connecting"
	case "downloading":
		return "Downloading"
	case "verifying":
		return "Verifying"
	case "completed":
		return "Complete"
	case "saved":
		return "Saved"
	case "paused":
		return "Paused"
	case "canceled":
		return "Canceled"
	case "failed":
		return "Failed"
	default:
		return "Checking"
	}
}
func segmentValue(part downloadSegment) (float64, bool) {
	if part.Start >= 0 && part.End == part.Start && part.Downloaded == 0 && part.State == "completed" {
		return 1, true
	}
	if part.Start < 0 || part.End <= part.Start || part.Downloaded < 0 {
		return 0, false
	}
	value := float64(part.Downloaded) / float64(part.End-part.Start)
	if value > 1 {
		value = 1
	}
	return value, true
}
func segmentSummary(j queueEntry) string {
	if len(j.Segments) == 0 {
		if j.State == "running" {
			return "Checking transfer method…"
		}
		return "No saved part information"
	}
	if j.TransferMode == "sequential" {
		return "1 part · single connection"
	}
	active := 0
	for _, part := range j.Segments {
		if part.State == "connecting" || part.State == "downloading" {
			active++
		}
	}
	return fmt.Sprintf("%d parts · %d active · connection limit %d", len(j.Segments), active, j.ConnectionLimit)
}
func newSegmentCard() *fyne.Container {
	return container.NewVBox(widget.NewLabel("Part"), widget.NewProgressBar())
}
func newSegmentPanel() *fyne.Container {
	return container.NewVBox(container.NewBorder(nil, nil, nil, widget.NewButton("Parts…", nil), widget.NewLabel("Parts")), container.NewGridWithColumns(4))
}
func updateSegmentPanel(panel *fyne.Container, j queueEntry, show func()) {
	if len(j.Segments) == 0 && j.State != "running" && j.State != "paused" {
		panel.Hide()
		return
	}
	panel.Show()
	header := panel.Objects[0].(*fyne.Container)
	header.Objects[0].(*widget.Label).SetText(segmentSummary(j))
	button := header.Objects[1].(*widget.Button)
	button.OnTapped = show
	if len(j.Segments) == 0 {
		button.Disable()
	} else {
		button.Enable()
	}
	grid := panel.Objects[1].(*fyne.Container)
	if len(grid.Objects) != len(j.Segments) {
		grid.Objects = nil
		for range j.Segments {
			grid.Objects = append(grid.Objects, newSegmentCard())
		}
		columns := len(j.Segments)
		if columns < 1 {
			columns = 1
		}
		if columns > 4 {
			columns = 4
		}
		grid.Layout = layout.NewGridLayoutWithColumns(columns)
	}
	for i, part := range j.Segments {
		card := grid.Objects[i].(*fyne.Container)
		card.Objects[0].(*widget.Label).SetText(fmt.Sprintf("Part %d · %s", i+1, segmentState(part.State)))
		value, known := segmentValue(part)
		showProgress(card.Objects[1].(*widget.ProgressBar), value, known)
	}
	grid.Refresh()
}

func (v *downloadsView) showParts(key string) {
	v.partKey = key
	v.partDetails = container.NewVBox()
	v.updatePartDetails()
	note := widget.NewLabel("Parts are actual file ranges. Saved progress within each part is verified on resume. A crash may lose progress since the last checkpoint.")
	note.Wrapping = fyne.TextWrapWord
	v.partDialog = dialog.NewCustom("Download parts", "Close", container.NewBorder(note, nil, nil, nil, container.NewVScroll(v.partDetails)), v.window)
	v.partDialog.Resize(fyne.NewSize(740, 520))
	v.partDialog.SetOnClosed(func() { v.partKey = ""; v.partDetails = nil; v.partDialog = nil })
	v.partDialog.Show()
}
func (v *downloadsView) updatePartDetails() {
	if v.partDetails == nil {
		return
	}
	var entry queueEntry
	found := false
	for _, row := range downloadRows(v.entries, v.history, "All downloads") {
		if row.Key == v.partKey {
			entry = row.Entry
			found = true
			break
		}
	}
	if !found {
		v.partDetails.Objects = []fyne.CanvasObject{widget.NewLabel("This download is no longer in history.")}
		v.partDetails.Refresh()
		return
	}
	if len(v.partDetails.Objects) != len(entry.Segments)+1 {
		v.partDetails.Objects = []fyne.CanvasObject{widget.NewLabel("")}
		for range entry.Segments {
			detail := widget.NewLabel("")
			detail.Wrapping = fyne.TextWrapWord
			v.partDetails.Objects = append(v.partDetails.Objects, container.NewVBox(widget.NewLabel(""), detail, widget.NewProgressBar()))
		}
	}
	v.partDetails.Objects[0].(*widget.Label).SetText(segmentSummary(entry))
	unit := displayUnit(v.settings.DisplayUnit)
	for i, part := range entry.Segments {
		card := v.partDetails.Objects[i+1].(*fyne.Container)
		card.Objects[0].(*widget.Label).SetText(fmt.Sprintf("Part %d · %s", i+1, segmentState(part.State)))
		detail := "Received " + dataNumber(float64(part.Downloaded), unit) + " " + unit + " · total size unknown"
		if part.End >= part.Start {
			detail = fmt.Sprintf("Range %s–%s %s · received %s / %s %s", dataNumber(float64(part.Start), unit), dataNumber(float64(part.End), unit), unit, dataNumber(float64(part.Downloaded), unit), dataNumber(float64(part.End-part.Start), unit), unit)
		}
		card.Objects[1].(*widget.Label).SetText(detail)
		value, known := segmentValue(part)
		showProgress(card.Objects[2].(*widget.ProgressBar), value, known)
	}
	v.partDetails.Refresh()
}
