package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2/widget"
)

func progressValue(message string) (float64, bool) {
	start := strings.Index(message, "Downloaded ")
	if start < 0 {
		return 0, false
	}
	var done, total int64
	if _, err := fmt.Sscanf(message[start:], "Downloaded %d/%d bytes", &done, &total); err != nil || done < 0 || total <= 0 || done > total {
		return 0, false
	}
	return float64(done) / float64(total), true
}

func remainingTimeWithSpeed(message string, speed float64) string {
	var done, total int64
	start := strings.Index(message, "Downloaded ")
	if start < 0 || speed <= 0 || math.IsNaN(speed) || math.IsInf(speed, 0) {
		return ""
	}
	if _, err := fmt.Sscanf(message[start:], "Downloaded %d/%d bytes", &done, &total); err != nil || done < 0 || total <= done {
		return ""
	}
	seconds := float64(total-done) / speed
	if seconds > 365*24*60*60 {
		return ""
	}
	remaining := time.Duration(seconds * float64(time.Second)).Round(time.Second)
	if remaining < time.Second {
		remaining = time.Second
	}
	return " · about " + remaining.String() + " left"
}

func downloadSpeed(j queueEntry, unit string) string {
	if j.Media != nil && j.MediaPhase != "download" {
		return "Speed: —"
	}
	if downloadState(j.State) != "Downloading" {
		return "Speed: —"
	}
	if j.Speed == nil {
		return "Speed: measuring…"
	}
	speed := *j.Speed
	if speed < 0 || math.IsNaN(speed) || math.IsInf(speed, 0) {
		return "Speed: measuring…"
	}
	return "Speed: " + dataNumber(speed, unit) + " " + displayUnit(unit) + "/s"
}

func progressLine() *widget.ProgressBar {
	bar := widget.NewProgressBar()
	bar.TextFormatter = func() string { return "" }
	return bar
}

func showProgress(bar *widget.ProgressBar, value float64, known bool) {
	if !known {
		bar.Hide()
		return
	}
	bar.SetValue(value)
	bar.Show()
}
