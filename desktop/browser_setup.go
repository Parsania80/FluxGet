package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"
	"github.com/Parsania80/FluxGet/internal/platformcmd"
)

//go:embed scripts/open-extension-folder.ps1
var openBrowserFolderWindows string

//go:embed scripts/open-extension-folder.sh
var openBrowserFolderLinux string

func browserExtensionFolder(executable, browser string) (string, error) {
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	folder := filepath.Join(filepath.Dir(executable), "extensions", browser)
	info, err := os.Stat(filepath.Join(folder, "manifest.json"))
	if err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("extension files are missing from %s. Reinstall FluxGet or build the extensions beside the app", folder)
	}
	return folder, nil
}

func openBrowserFolder(folder string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	program, args := "sh", []string{"-c", openBrowserFolderLinux}
	if runtime.GOOS == "windows" {
		program, args = "powershell.exe", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", openBrowserFolderWindows}
	}
	cmd := platformcmd.CommandContext(ctx, program, args...)
	cmd.Env = append(cmd.Environ(), "FLUXGET_EXTENSION_FOLDER="+folder)
	return cmd.Run()
}

func (b *browserIntegration) showSetup() {
	executable, _ := os.Executable()
	b.showBrowserSetup(executable)
}

func (b *browserIntegration) showBrowserSetup(executable string) {
	steps := widget.NewLabel("")
	steps.Wrapping = fyne.TextWrapWord
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	browser := widget.NewSelect([]string{"Chrome", "Firefox", "Opera"}, nil)
	addresses := map[string]string{"Chrome": "chrome://extensions", "Firefox": "about:debugging#/runtime/this-firefox", "Opera": "opera://extensions"}
	withFolder := func(use func(string)) {
		if executable == "" {
			status.SetText("Could not find the FluxGet installation.")
			return
		}
		folder, err := browserExtensionFolder(executable, strings.ToLower(browser.Selected))
		if err != nil {
			status.SetText(err.Error())
			return
		}
		use(folder)
	}
	open := widget.NewButton("Open extension folder", func() {
		withFolder(func(folder string) {
			name := browser.Selected
			status.SetText("Opening " + folder + "…")
			go func() {
				err := openBrowserFolder(folder)
				fyne.Do(func() {
					if browser.Selected != name {
						return
					}
					if err != nil {
						status.SetText("Could not open the file manager. Use Copy folder path to open it manually.")
						return
					}
					status.SetText("Extension folder: " + folder)
				})
			}()
		})
	})
	copyFolder := widget.NewButton("Copy folder path", func() {
		withFolder(func(folder string) {
			b.view.app.Clipboard().SetContent(folder)
			status.SetText("Folder path copied. Paste it into the browser’s file picker.")
		})
	})
	browser.OnChanged = func(name string) {
		status.SetText("")
		load := "Turn on Developer mode, choose Load unpacked, then paste the folder path or browse to it."
		if name == "Firefox" {
			load = "Choose Load Temporary Add-on and select manifest.json in the extension folder. Repeat after restarting Firefox. Some Linux Firefox packages require a native-messaging portal."
		} else if name == "Opera" {
			load += " Opera integration is experimental. The Windows installer registers Chrome and Firefox."
		}
		steps.SetText("Open " + addresses[name] + " in " + name + ". " + load)
	}
	browser.SetSelected("Chrome")
	copyAddress := widget.NewButton("Copy browser address", func() {
		b.view.app.Clipboard().SetContent(addresses[browser.Selected])
		status.SetText("Address copied. Paste it into " + browser.Selected + ".")
	})
	done := widget.NewLabel("Then enable integration and this browser in FluxGet, and save. In the browser toolbar, open FluxGet, turn on Enable this browser profile, and choose Test connection.")
	done.Wrapping = fyne.TextWrapWord
	d := dialog.NewCustom("Set up a browser extension", "Done", container.NewVBox(
		browser, steps, container.NewGridWithColumns(3, copyAddress, copyFolder, open), done, status), b.view.window)
	d.Resize(fyne.NewSize(620, 360))
	d.Show()
}
