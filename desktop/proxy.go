package main

import (
	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/Parsania80/FluxGet/networkproxy"
)

func proxyForDownload(saved networkproxy.Config, enabled bool) (*networkproxy.Config, error) {
	if !enabled {
		return nil, nil
	}
	saved.Enabled = true
	normalized, err := saved.Normalize()
	if err != nil {
		return nil, err
	}
	return &normalized, nil
}

func proxyDefaultDescription(config networkproxy.Config) string {
	if config.URL == "" {
		return "Set a proxy address in Settings to use this option."
	}
	return "Configured proxy: " + config.URL + ". Turning this option off connects directly."
}

func proxySettingsFields(current networkproxy.Config) (fyne.CanvasObject, func(networkproxy.Config) (networkproxy.Config, error)) {
	enabled := widget.NewCheck("Use proxy by default for new downloads", nil)
	enabled.SetChecked(current.Enabled)
	address := widget.NewEntry()
	address.SetText(current.URL)
	address.SetPlaceHolder("socks5://127.0.0.1:10808 or http://127.0.0.1:10809")
	note := widget.NewLabel("Enter the HTTP, HTTPS or SOCKS5 listening address from your proxy app, such as v2ray. Keep that app running. You can choose Proxy or Direct for each download. Existing downloads keep their saved route; pause one before changing it. This field accepts a proxy address without a username or password, not a vmess/vless or subscription link.")
	note.Wrapping = fyne.TextWrapWord
	fields := container.NewVBox(widget.NewLabelWithStyle("Proxy", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), note, enabled, widget.NewForm(widget.NewFormItem("Proxy address", address)))
	read := func(saved networkproxy.Config) (networkproxy.Config, error) {
		config, err := (networkproxy.Config{Enabled: enabled.Checked, URL: address.Text}).Normalize()
		// Disabling never depends on fixing an unfinished address edit.
		if err != nil && !enabled.Checked {
			config = saved
			config.Enabled = false
			err = nil
		}
		if err == nil {
			address.SetText(config.URL)
		}
		return config, err
	}
	return fields, read
}

func bindProxyButton(button *widget.Button, entry queueEntry, edit func()) {
	button.SetText("Direct")
	if entry.Proxy != nil && entry.Proxy.Enabled {
		button.SetText("Proxy")
	}
	button.OnTapped = edit
	button.Enable()
	if entry.ID == 0 || entry.State == "completed" || entry.BrowserPending {
		button.Disable()
	}
}

func (v *downloadsView) editProxy(key string) {
	var entry queueEntry
	for _, row := range v.rows() {
		if row.Key == key {
			entry = row.Entry
			break
		}
	}
	if entry.ID == 0 {
		return
	}
	if entry.State != "paused" && entry.State != "failed" && entry.State != "canceled" {
		dialog.ShowInformation("Pause before changing proxy", "Pause this download first so its progress can be saved. Then choose Proxy or Direct and resume it.", v.window)
		return
	}
	current := v.settings.Proxy
	if entry.Proxy != nil {
		current = *entry.Proxy
	}
	enabled := widget.NewCheck("Download with proxy", nil)
	address := widget.NewEntry()
	address.SetText(current.URL)
	address.SetPlaceHolder("socks5://127.0.0.1:10808")
	enabled.OnChanged = func(checked bool) {
		if checked {
			address.Enable()
		} else {
			address.Disable()
		}
	}
	enabled.SetChecked(entry.Proxy != nil && entry.Proxy.Enabled)
	enabled.OnChanged(enabled.Checked)
	note := widget.NewLabel("Turn this off to connect directly. If the proxy cannot connect, the download reports an error and never switches to Direct automatically. Resume the download after applying.")
	note.Wrapping = fyne.TextWrapWord
	form := widget.NewForm(widget.NewFormItem("Proxy address", address))
	form.SubmitText = "Apply"
	form.CancelText = "Cancel"
	var popup *dialog.CustomDialog
	form.OnCancel = func() { popup.Hide() }
	form.OnSubmit = func() {
		route, err := proxyForDownload(networkproxy.Config{URL: address.Text}, enabled.Checked)
		if err != nil {
			dialog.ShowError(err, v.window)
			return
		}
		v.work(func(q *queueClient) (desktopResponse, error) {
			return callDesktop(q, desktopRequest{Operation: "proxy", Job: queueEntry{ID: entry.ID, Proxy: route}})
		}, func(desktopResponse) { popup.Hide(); v.status.SetText("Download connection saved. Resume when ready.") })
	}
	popup = dialog.NewCustomWithoutButtons("Download connection", container.NewVBox(note, enabled, form), v.window)
	popup.Resize(fyne.NewSize(560, 320))
	popup.Show()
}
