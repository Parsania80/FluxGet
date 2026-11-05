package main

import (
	"encoding/json"
	"errors"
	"fmt"
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
	Enabled   bool             `json:"enabled"`
	Default   int64            `json:"default_kib"`
	Overrides map[string]int64 `json:"overrides"`
	path      string
}

func loadSpeedSettings() (*speedSettings, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	s := &speedSettings{path: filepath.Join(dir, "fluxget", "desktop-speed.json"), Overrides: map[string]int64{}}
	data, err := os.ReadFile(s.path)
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
	if s.Default < 0 || s.Default > 1<<30 || (s.Enabled && s.Default == 0) {
		return nil, errors.New("invalid default speed limit")
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
	return os.Rename(f.Name(), s.path)
}

func parseSpeed(text string, allowZero bool) (int64, error) {
	v, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
	if err != nil || v < 0 || v > 1<<30 || (!allowZero && v == 0) {
		return 0, errors.New("Enter a number between 1 and 1073741824 KiB/s.")
	}
	return v, nil
}

func (s *speedSettings) editDownload(w fyne.Window, key, filename string, onSaved func()) {
	entry := widget.NewEntry()
	entry.SetPlaceHolder("0 = use default only")
	entry.SetText(fmt.Sprint(s.Overrides[key]))
	form := widget.NewForm(widget.NewFormItem("Limit (KiB/s)", entry))
	form.OnSubmit = func() {
		value, err := parseSpeed(entry.Text, true)
		if err != nil {
			dialog.ShowError(err, w)
			return
		}
		old, had := s.Overrides[key]
		if value == 0 {
			delete(s.Overrides, key)
		} else {
			s.Overrides[key] = value
		}
		if err = s.save(); err != nil {
			if had {
				s.Overrides[key] = old
			} else {
				delete(s.Overrides, key)
			}
			dialog.ShowError(err, w)
			return
		}
		if onSaved != nil {
			onSaved()
		}
		dialog.ShowInformation("Speed limit updated", "The limit applies to this download, including while it is running.", w)
	}
	dialog.ShowCustom("Download speed limit", "Close", container.NewVBox(widget.NewLabel(filename), widget.NewLabel("Set 0 to use only the default. The lower enabled limit applies."), form), w)
}

func bindLimitButton(button *widget.Button, settings *speedSettings, key, filename string, edit func(string, string)) {
	label := "Limit: default"
	if limit := settings.Overrides[key]; limit > 0 {
		label = fmt.Sprintf("Limit: %d KiB/s", limit)
	}
	button.SetText(label)
	button.OnTapped = func() { edit(key, filename) }
}

func (s *speedSettings) page(w fyne.Window) fyne.CanvasObject {
	enabled := widget.NewCheck("Enable default speed limit for every download", nil)
	enabled.SetChecked(s.Enabled)
	value := widget.NewEntry()
	value.SetText(fmt.Sprint(s.Default))
	value.SetPlaceHolder("KiB/s")
	result := widget.NewLabel("")
	save := widget.NewButton("Save settings", func() {
		v := s.Default
		var err error
		if enabled.Checked {
			v, err = parseSpeed(value.Text, false)
		} else if strings.TrimSpace(value.Text) != "" {
			v, err = parseSpeed(value.Text, true)
		}
		if err != nil {
			result.SetText(err.Error())
			return
		}
		oldEnabled, oldDefault := s.Enabled, s.Default
		s.Enabled, s.Default = enabled.Checked, v
		if err = s.save(); err != nil {
			s.Enabled, s.Default = oldEnabled, oldDefault
			result.SetText("Could not save: " + err.Error())
			return
		}
		result.SetText("Saved. Running downloads will update shortly.")
	})
	save.Importance = widget.HighImportance
	note := widget.NewLabel("This is a per-download ceiling. Queue-wide limits still apply. Use the limit button beside a download to set a lower limit when needed.")
	note.Wrapping = fyne.TextWrapWord
	return container.NewPadded(container.NewVBox(widget.NewLabelWithStyle("Speed limits", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), note, enabled, widget.NewLabel("Default speed (KiB/s)"), value, save, result))
}
