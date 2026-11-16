package main

import (
	"testing"

	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
)

func TestBrowserDownloadPromptRequiresOneDecision(t *testing.T) {
	for _, action := range []string{"approve", "browser", "close"} {
		t.Run(action, func(t *testing.T) {
			a := test.NewApp()
			defer a.Quit()
			folder, name := widget.NewEntry(), widget.NewEntry()
			name.SetText("file.zip")
			var decisions []browserDownloadChoice
			w := newBrowserDownloadPrompt(a, folder, name, widget.NewCheck("Sort files", nil), widget.NewCheck("Later", nil), func(choice browserDownloadChoice) {
				decisions = append(decisions, choice)
			})
			w.Show()
			if len(decisions) != 0 {
				t.Fatal("download was approved before a user decision")
			}
			switch action {
			case "approve":
				test.Tap(findFluxGetButton(t, w.Content(), "Use FluxGet"))
			case "browser":
				test.Tap(findFluxGetButton(t, w.Content(), "Use browser"))
			case "close":
				w.Close()
			}
			want := map[string]browserDownloadChoice{"approve": useFluxGet, "browser": useBrowser, "close": cancelBrowserDownload}[action]
			if len(decisions) != 1 || decisions[0] != want {
				t.Fatalf("incorrect decision: %v", decisions)
			}
		})
	}
}
