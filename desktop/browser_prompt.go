package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"
)

type browserDownloadChoice uint8

const (
	cancelBrowserDownload browserDownloadChoice = iota
	useFluxGet
	useBrowser
)

func newBrowserDownloadPrompt(a fyne.App, folder, filename *widget.Entry, sortFiles, later *widget.Check, decide func(browserDownloadChoice)) fyne.Window {
	w := a.NewWindow("Download with FluxGet?")
	var answered bool
	finish := func(choice browserDownloadChoice) {
		if answered {
			return
		}
		answered = true
		w.SetCloseIntercept(nil)
		w.Close()
		decide(choice)
	}
	form := widget.NewForm(widget.NewFormItem("Folder", folder), widget.NewFormItem("Filename", filename),
		widget.NewFormItem("", sortFiles), widget.NewFormItem("", later))
	form.SubmitText, form.CancelText = "Use FluxGet", "Use browser"
	form.OnSubmit = func() { finish(useFluxGet) }
	form.OnCancel = func() { finish(useBrowser) }
	w.SetContent(container.NewPadded(form))
	w.SetCloseIntercept(func() { finish(cancelBrowserDownload) })
	w.SetOnClosed(func() {
		if !answered {
			answered = true
			decide(cancelBrowserDownload)
		}
	})
	w.Resize(fyne.NewSize(580, 310))
	w.CenterOnScreen()
	if native, ok := w.(desktop.Window); ok {
		native.RequestAlwaysOnTop()
	}
	return w
}
