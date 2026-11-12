package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Parsania80/FluxGet/internal/platformfile"
	"github.com/Parsania80/FluxGet/networkproxy"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
)

type speedSettings struct {
	Proxy          networkproxy.Config `json:"proxy,omitempty"`
	hardwareNote   *widget.Label
	Enabled        bool             `json:"enabled"`
	Default        int64            `json:"default_kib"`
	Overrides      map[string]int64 `json:"overrides"`
	MaxActive      int              `json:"max_active,omitempty"`
	Connections    int              `json:"connections,omitempty"`
	Folder         string           `json:"folder,omitempty"`
	Overall        int64            `json:"overall_kib,omitempty"`
	DisplayUnit    string           `json:"display_unit,omitempty"`
	ManagementMode string           `json:"management_mode,omitempty"`
	TotalRequests  int              `json:"total_requests,omitempty"`
	OriginRequests int              `json:"origin_requests,omitempty"`
	applyDownloads func(speedSettings, func(error))
	path           string
}

func loadSpeedSettings() (*speedSettings, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	s := &speedSettings{path: filepath.Join(dir, "fluxget", "desktop-speed.json"), Overrides: map[string]int64{}, MaxActive: 5, Connections: 4, DisplayUnit: "MB", ManagementMode: "automatic", TotalRequests: 8, OriginRequests: 4}
	data, err := platformfile.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s, s.save()
	}
	if err != nil {
		return nil, err
	}
	if len(data) > 1<<20 {
		return nil, errors.New("speed settings too large")
	}
	if err := json.Unmarshal(data, s); err != nil {
		return nil, err
	}
	s.Proxy, err = s.Proxy.Normalize()
	if err != nil {
		return nil, err
	}
	if s.ManagementMode == "" {
		s.ManagementMode = "automatic"
	}
	if (s.ManagementMode != "automatic" && s.ManagementMode != "manual") || s.TotalRequests < 1 || s.TotalRequests > 64 || s.OriginRequests < 1 || s.OriginRequests > 16 {
		return nil, errors.New("invalid download management settings")
	}
	if s.DisplayUnit == "" {
		s.DisplayUnit = "MB"
	}
	if s.DisplayUnit != "KB" && s.DisplayUnit != "MB" && s.DisplayUnit != "GB" {
		return nil, errors.New("invalid display unit")
	}
	if s.Default < 0 || s.Default > 1<<30 || (s.Enabled && s.Default == 0) {
		return nil, errors.New("invalid default speed limit")
	}
	if s.MaxActive < 1 || s.MaxActive > 64 || s.Connections < 1 || s.Connections > 16 || s.Overall < 0 || s.Overall > 1<<30 || (s.Folder != "" && !filepath.IsAbs(s.Folder)) {
		return nil, errors.New("invalid download settings")
	}
	if s.Overrides == nil {
		s.Overrides = map[string]int64{}
	}
	for _, value := range s.Overrides {
		if value <= 0 || value > 1<<30 {
			return nil, errors.New("invalid download speed limit")
		}
	}
	return s, nil
}

func (s *speedSettings) save() error {
	if _, err := s.Proxy.Normalize(); err != nil {
		return err
	}
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".speed-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return platformfile.Replace(f.Name(), s.path)
}

func (s *speedSettings) saveDownloadLimit(key string, value int64) error {
	old, had := s.Overrides[key]
	if s.Overrides == nil {
		s.Overrides = make(map[string]int64)
	}
	if value == 0 {
		delete(s.Overrides, key)
	} else {
		s.Overrides[key] = value
	}
	if err := s.save(); err != nil {
		if had {
			s.Overrides[key] = old
		} else {
			delete(s.Overrides, key)
		}
		return err
	}
	return nil
}

func (s *speedSettings) inheritedLimitsText() string {
	unit := displayUnit(s.DisplayUnit)
	perDownload := "Unlimited"
	if s.Enabled && s.Default > 0 {
		perDownload = dataNumber(float64(s.Default)*1024, unit) + " " + unit + "/s"
	}
	overall := "Unlimited"
	if s.Overall > 0 {
		overall = dataNumber(float64(s.Overall)*1024, unit) + " " + unit + "/s shared across downloads"
	}
	return "Default per download: " + perDownload + "\nOverall limit: " + overall
}

func (s *speedSettings) editDownload(w fyne.Window, key, filename string, onSaved func()) {
	unit := displayUnit(s.DisplayUnit)
	enabled := widget.NewCheck("Use custom speed limit", nil)
	entry := widget.NewEntry()
	entry.SetPlaceHolder("Enter a speed in " + unit + "/s")
	if value := s.Overrides[key]; value > 0 {
		entry.SetText(limitText(value, unit))
	} else if s.Enabled && s.Default > 0 {
		entry.SetText(limitText(s.Default, unit))
	}
	enabled.OnChanged = func(checked bool) {
		if checked {
			entry.Enable()
		} else {
			entry.Disable()
		}
	}
	enabled.SetChecked(s.Overrides[key] > 0)
	enabled.OnChanged(enabled.Checked)
	form := widget.NewForm(widget.NewFormItem("Speed ("+unit+"/s)", entry))
	form.SubmitText = "Apply"
	form.CancelText = "Cancel"
	var popup *dialog.CustomDialog
	form.OnCancel = func() { popup.Hide() }
	form.OnSubmit = func() {
		var value int64
		var err error
		if enabled.Checked {
			value, err = parseDisplayLimit(entry.Text, unit, false)
			if err != nil {
				dialog.ShowError(err, w)
				return
			}
		}
		if err := s.saveDownloadLimit(key, value); err != nil {
			dialog.ShowError(err, w)
			return
		}
		popup.Hide()
		if onSaved != nil {
			onSaved()
		}
	}
	filenameLabel := widget.NewLabel(filename)
	filenameLabel.Wrapping = fyne.TextWrapWord
	note := widget.NewLabel("Turn off the custom limit to use your Settings limits. Changes apply immediately to running downloads after Apply.")
	note.Wrapping = fyne.TextWrapWord
	inherited := widget.NewLabel(s.inheritedLimitsText())
	inherited.Wrapping = fyne.TextWrapWord
	popup = dialog.NewCustomWithoutButtons("Download speed limit", container.NewVBox(filenameLabel, note, inherited, enabled, form), w)
	popup.Resize(fyne.NewSize(540, 360))
	popup.Show()
}

func bindRemoveLimitButton(button *widget.Button, settings *speedSettings, key string, remove func(string)) {
	button.OnTapped = func() { remove(key) }
	if settings.Overrides[key] > 0 {
		button.Show()
	} else {
		button.Hide()
	}
}

func bindLimitButton(button *widget.Button, settings *speedSettings, key, filename string, edit func(string, string)) {
	label := "Limit: default"
	if limit := settings.Overrides[key]; limit > 0 {
		label = "Limit: " + dataNumber(float64(limit)*1024, settings.DisplayUnit) + " " + displayUnit(settings.DisplayUnit) + "/s"
	}
	button.SetText(label)
	button.OnTapped = func() { edit(key, filename) }
}

func (s *speedSettings) requestPolicy() requestPolicy {
	mode := s.ManagementMode
	if mode == "" {
		mode = "automatic"
	}
	total, origin := s.TotalRequests, s.OriginRequests
	if total == 0 {
		total = 8
	}
	if origin == 0 {
		origin = 4
	}
	return requestPolicy{Mode: mode, Total: total, PerOrigin: origin}
}
func (s *speedSettings) configuration() desktopRequest {
	policy := s.requestPolicy()
	return desktopRequest{Operation: "configure", MaxActive: s.MaxActive, Connections: s.Connections, Management: &policy}
}

func (s *speedSettings) page(w fyne.Window, onSaved func(), canSave func() bool) fyne.CanvasObject {
	policy := s.requestPolicy()
	mode := widget.NewSelect([]string{"Automatic", "Manual"}, nil)
	if policy.Mode == "manual" {
		mode.SetSelected("Manual")
	} else {
		mode.SetSelected("Automatic")
	}
	total, origin := widget.NewEntry(), widget.NewEntry()
	total.SetText(fmt.Sprint(policy.Total))
	origin.SetText(fmt.Sprint(policy.PerOrigin))
	managementNote := widget.NewLabel("Automatic measures your downloads and tries one extra connection or file at a time. It keeps extra connections when they improve total speed, respects your speed limits, and lets active files finish when reducing simultaneous downloads. Manual uses your configured maximums. Saved part boundaries stay fixed. On Linux, sustained CPU or memory pressure holds new work and reduces connections gradually. Destination and checkpoint timings are measured separately. Missing hardware observations stay Unknown; other systems retain conservative request budgets.")
	managementNote.Wrapping = fyne.TextWrapWord
	mode.OnChanged = func(value string) {
		if value == "Automatic" {
			total.Enable()
			origin.Enable()
		} else {
			total.Disable()
			origin.Disable()
		}
	}
	mode.OnChanged(mode.Selected)
	s.hardwareNote = widget.NewLabel("Hardware observations: collecting samples")
	s.hardwareNote.Wrapping = fyne.TextWrapWord
	inputUnit := displayUnit(s.DisplayUnit)
	unit := widget.NewSelect([]string{"KB", "MB", "GB"}, nil)
	unit.SetSelected(inputUnit)
	enabled := widget.NewCheck("Limit the speed of each download", nil)
	enabled.SetChecked(s.Enabled)
	value := widget.NewEntry()
	value.SetText(limitText(s.Default, inputUnit))
	active := widget.NewEntry()
	active.SetText(fmt.Sprint(s.MaxActive))
	connections := widget.NewEntry()
	connections.SetText(fmt.Sprint(s.Connections))
	overall := widget.NewEntry()
	overall.SetText(limitText(s.Overall, inputUnit))
	folder := widget.NewEntry()
	folder.SetText(s.Folder)
	folder.SetPlaceHolder("Your Downloads folder")
	browse := widget.NewButton("Browse…", func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if err != nil {
				dialog.ShowError(err, w)
			} else if uri != nil {
				folder.SetText(uri.Path())
			}
		}, w)
	})
	proxySection, proxyChoice := proxySettingsFields(s.Proxy)
	result := widget.NewLabel("")
	result.Wrapping = fyne.TextWrapWord
	perDownload := widget.NewFormItem("Per download ("+inputUnit+"/s)", value)
	allDownloads := widget.NewFormItem("All downloads ("+inputUnit+"/s)", overall)
	limits := widget.NewForm(perDownload, allDownloads)
	save := widget.NewButton("Save settings", func() {
		if canSave != nil && !canSave() {
			result.SetText("Wait for downloads to finish loading before saving settings.")
			return
		}
		next := *s
		var proxyErr error
		next.Proxy, proxyErr = proxyChoice(s.Proxy)
		if proxyErr != nil {
			result.SetText(proxyErr.Error())
			return
		}
		next.DisplayUnit = unit.Selected
		next.ManagementMode = "automatic"
		if mode.Selected == "Manual" {
			next.ManagementMode = "manual"
		}
		var err error
		next.TotalRequests, err = strconv.Atoi(strings.TrimSpace(total.Text))
		if err != nil || next.TotalRequests < 1 || next.TotalRequests > 64 {
			result.SetText("Choose 1–64 total connections.")
			return
		}
		next.OriginRequests, err = strconv.Atoi(strings.TrimSpace(origin.Text))
		if err != nil || next.OriginRequests < 1 || next.OriginRequests > 16 {
			result.SetText("Choose 1–16 connections to one server.")
			return
		}
		next.MaxActive, err = strconv.Atoi(strings.TrimSpace(active.Text))
		if err != nil || next.MaxActive < 1 || next.MaxActive > 64 {
			result.SetText("Choose 1–64 simultaneous downloads.")
			return
		}
		next.Connections, err = strconv.Atoi(strings.TrimSpace(connections.Text))
		if err != nil || next.Connections < 1 || next.Connections > 16 {
			result.SetText("Choose 1–16 connections per download.")
			return
		}
		next.Default, err = parseDisplayLimit(value.Text, inputUnit, !enabled.Checked)
		if err != nil {
			result.SetText(err.Error())
			return
		}
		next.Overall, err = parseDisplayLimit(overall.Text, inputUnit, true)
		if err != nil {
			result.SetText(err.Error())
			return
		}
		next.Folder = strings.TrimSpace(folder.Text)
		if next.Folder != "" {
			info, e := os.Stat(next.Folder)
			if !filepath.IsAbs(next.Folder) || e != nil || !info.IsDir() {
				result.SetText("Choose an existing download folder.")
				return
			}
		}
		next.Enabled = enabled.Checked
		finish := func(err error) {
			if err != nil {
				result.SetText("Could not apply settings: " + err.Error())
				return
			}
			*s = next
			inputUnit = next.DisplayUnit
			value.SetText(limitText(next.Default, inputUnit))
			overall.SetText(limitText(next.Overall, inputUnit))
			perDownload.Text = "Per download (" + inputUnit + "/s)"
			allDownloads.Text = "All downloads (" + inputUnit + "/s)"
			limits.Refresh()
			result.SetText("Saved. Proxy defaults apply to new downloads; existing downloads keep their route. Worker, request and speed limits apply now.")
			if onSaved != nil {
				onSaved()
			}
		}
		if s.applyDownloads != nil {
			result.SetText("Applying settings…")
			s.applyDownloads(next, finish)
		} else {
			finish(next.save())
		}
	})
	save.Importance = widget.HighImportance
	note := widget.NewLabel("Extra downloads wait automatically. Paused downloads stay paused until you resume them. A lower simultaneous limit lets current transfers finish before starting more.")
	note.Wrapping = fyne.TextWrapWord
	speedNote := widget.NewLabel("Use 0 for unlimited overall speed. The per-download ceiling and each row’s individual limit apply together; the lower ceiling wins.")
	speedNote.Wrapping = fyne.TextWrapWord
	unitNote := widget.NewLabel("Choose KB, MB, or GB for download sizes, speeds, and limits. MB is the default. Units use 1,000 KB per MB and 1,000 MB per GB; save to apply the change.")
	unitNote.Wrapping = fyne.TextWrapWord
	return container.NewVScroll(container.NewPadded(container.NewVBox(
		widget.NewLabelWithStyle("Display", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), unitNote, widget.NewForm(widget.NewFormItem("Data unit", unit)), widget.NewSeparator(),
		widget.NewLabelWithStyle("Downloads", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), note,
		managementNote,
		widget.NewAccordion(widget.NewAccordionItem("Customize download management", container.NewVBox(widget.NewForm(widget.NewFormItem("Mode", mode), widget.NewFormItem("Maximum simultaneous downloads", active), widget.NewFormItem("Maximum connections per download", connections), widget.NewFormItem("Maximum total connections", total), widget.NewFormItem("Maximum connections to one server", origin)), s.hardwareNote))),
		widget.NewForm(widget.NewFormItem("Default folder", container.NewBorder(nil, nil, nil, browse, folder))),
		widget.NewSeparator(), widget.NewLabelWithStyle("Speed limits", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), speedNote, enabled,
		limits, widget.NewSeparator(), proxySection, save, result,
	)))
}
