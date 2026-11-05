package main

import (
	"fmt"
	"strings"

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
