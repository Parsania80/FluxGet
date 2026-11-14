package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/Parsania80/FluxGet/media"
	"github.com/Parsania80/FluxGet/networkproxy"
)

type youtubeAnalyzer func(context.Context, string, networkproxy.Config, string) (media.Video, error)

type youtubeView struct {
	playlist                                            *youtubePlaylist
	scope                                               *widget.Select
	playlistLoader                                      func(context.Context, string, networkproxy.Config, string, int) (media.PlaylistPage, error)
	submitPlaylist                                      func(string, []queueEntry, func([]media.PlaylistResult, error))
	window                                              fyne.Window
	settings                                            *speedSettings
	content                                             fyne.CanvasObject
	link, tools, folder, filename                       *widget.Entry
	kind, format, quality, audio, bitrate               *widget.Select
	useProxy                                            *widget.Check
	sortFiles                                           *widget.Check
	proxyNote, status, title, details, size, formatNote *widget.Label
	analyze, cancel, browseTools, download              *widget.Button
	spinner                                             *widget.ProgressBarInfinite
	options                                             *fyne.Container
	empty                                               *fyne.Container
	bitratePanel                                        *widget.Form
	video                                               media.Video
	videos, audios                                      []media.Format
	route                                               networkproxy.Config
	busy, closed, submitting                            bool
	cancelRun                                           context.CancelFunc
	onIdle                                              func()
	analyzer                                            youtubeAnalyzer
	setupTools                                          func(context.Context, networkproxy.Config) (string, error)
	dispatch                                            func(func())
	submit                                              func(queueEntry, func(error))
	canDownload                                         func() bool
}

func analyzeYouTube(ctx context.Context, manifest string, route networkproxy.Config, source string) (media.Video, error) {
	tools, err := media.LoadTools(manifest)
	if err != nil {
		return media.Video{}, err
	}
	client, err := media.NewWithProxy(tools, route)
	if err != nil {
		return media.Video{}, err
	}
	if _, err := client.Probe(ctx); err != nil {
		return media.Video{}, err
	}
	return client.Analyze(ctx, source)
}

func newYouTubeView(w fyne.Window, settings *speedSettings, executable string) *youtubeView {
	y := &youtubeView{window: w, settings: settings, analyzer: analyzeYouTube, dispatch: fyne.Do}
	y.link = widget.NewEntry()
	y.link.SetPlaceHolder("YouTube video, Shorts or playlist HTTPS link")
	y.tools = widget.NewEntry()
	manifest := os.Getenv("FLUXGET_MEDIA_TOOLS")
	if manifest == "" {
		manifest = filepath.Join(filepath.Dir(executable), "media-tools", "tools.json")
		if _, err := os.Stat(manifest); err != nil {
			if configured, err := media.DefaultToolsManifest(); err == nil {
				manifest = configured
			}
		}
	}
	y.setupTools = media.SetupTools
	y.tools.SetText(manifest)
	y.browseTools = widget.NewButtonWithIcon("Choose…", theme.FolderOpenIcon(), func() {
		d := dialog.NewFileOpen(func(file fyne.URIReadCloser, err error) {
			if file != nil {
				defer file.Close()
			}
			if y.closed {
				return
			}
			if err != nil {
				y.status.SetText("Could not open the media tools manifest. Choose a local tools.json file.")
				return
			}
			if file != nil {
				y.tools.SetText(file.URI().Path())
			}
		}, w)
		d.SetFilter(storage.NewExtensionFileFilter([]string{".json"}))
		d.Show()
	})
	y.status = widget.NewLabel("Ready. Analyze a link to choose what to download.")
	y.status.Wrapping = fyne.TextWrapWord
	y.proxyNote = widget.NewLabel(proxyDefaultDescription(settings.Proxy))
	y.proxyNote.Wrapping = fyne.TextWrapWord
	y.useProxy = widget.NewCheck("Use proxy", nil)
	y.useProxy.SetChecked(settings.Proxy.Enabled)
	y.title = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	y.title.Wrapping = fyne.TextWrapWord
	y.details = widget.NewLabel("")
	y.details.Wrapping = fyne.TextWrapWord
	y.formatNote = widget.NewLabel("")
	y.formatNote.Wrapping = fyne.TextWrapWord
	y.size = widget.NewLabel("")
	y.size.Wrapping = fyne.TextWrapWord
	y.kind = widget.NewSelect([]string{"Video", "Audio only"}, nil)
	y.kind.SetSelected("Video")
	y.format = widget.NewSelect(nil, nil)
	y.quality = widget.NewSelect(nil, nil)
	y.audio = widget.NewSelect(nil, nil)
	y.bitrate = widget.NewSelect([]string{"128 kbps", "192 kbps", "256 kbps", "320 kbps"}, nil)
	y.bitrate.SetSelected("192 kbps")
	y.bitrate.Disable()
	y.folder = widget.NewEntry()
	y.folder.SetText(settings.Folder)
	if y.folder.Text == "" {
		if home, err := os.UserHomeDir(); err == nil {
			y.folder.SetText(filepath.Join(home, "Downloads"))
		}
	}
	y.filename = widget.NewEntry()
	y.sortFiles = widget.NewCheck("Sort into a file-type folder", nil)
	y.sortFiles.SetChecked(settings.SortFiles)
	browseFolder := widget.NewButtonWithIcon("Browse…", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if y.closed {
				return
			}
			if err != nil {
				y.status.SetText("Could not open the destination folder.")
			} else if uri != nil {
				y.folder.SetText(uri.Path())
			}
		}, w)
	})
	y.download = widget.NewButtonWithIcon("Download", theme.DownloadIcon(), y.downloadSelection)
	y.download.Importance = widget.HighImportance
	y.bitratePanel = widget.NewForm(widget.NewFormItem("MP3 bitrate", y.bitrate))
	y.bitratePanel.Hide()
	output := container.NewVBox(widget.NewForm(widget.NewFormItem("Save as", y.kind), widget.NewFormItem("Format", y.format), widget.NewFormItem("Video quality", y.quality), widget.NewFormItem("Audio track", y.audio)), y.bitratePanel)
	fileNote := widget.NewLabel("The file extension is added automatically. Existing files are kept.")
	fileNote.Wrapping = fyne.TextWrapWord
	destination := container.NewVBox(widget.NewForm(widget.NewFormItem("Folder", container.NewBorder(nil, nil, nil, browseFolder, y.folder)), widget.NewFormItem("Filename", y.filename)), y.sortFiles, fileNote)
	y.options = container.NewBorder(container.NewVBox(y.title, y.details),
		container.NewVBox(y.formatNote, widget.NewSeparator(), container.NewBorder(nil, nil, nil, y.download, y.size)), nil, nil,
		container.NewVScroll(container.NewGridWithColumns(2, widget.NewCard("Output", "Choose the format and quality", output), widget.NewCard("Save to", "Choose where to keep your file", destination))))
	y.options.Hide()
	y.spinner = widget.NewProgressBarInfinite()
	y.spinner.Stop()
	y.spinner.Hide()
	y.analyze = widget.NewButtonWithIcon("Analyze", theme.SearchIcon(), y.startAnalysis)
	y.analyze.Importance = widget.HighImportance
	y.cancel = widget.NewButton("Cancel", y.cancelAnalysis)
	y.cancel.Disable()
	y.link.OnSubmitted = func(string) { y.startAnalysis() }
	y.link.OnChanged = func(string) {
		y.invalidate()
		if _, err := media.NormalizeVideoURL(strings.TrimSpace(y.link.Text)); errors.Is(err, media.ErrPlaylist) {
			y.scope.SetSelected("Choose from playlist")
		} else {
			y.scope.SetSelected("This video")
		}
	}
	y.tools.OnChanged = func(string) { y.invalidate() }
	y.useProxy.OnChanged = func(bool) { y.invalidate() }
	y.kind.OnChanged = func(string) { y.populateContainers() }
	y.format.OnChanged = func(string) { y.populateFormats() }
	y.quality.OnChanged = func(string) { y.populateAudio() }
	y.audio.OnChanged = func(string) { y.selectionChanged() }
	y.bitrate.OnChanged = func(string) { y.selectionChanged() }
	y.folder.OnChanged = func(string) { y.updateDownloadEnabled() }
	y.filename.OnChanged = func(string) { y.updateDownloadEnabled() }
	y.sortFiles.OnChanged = func(bool) { y.updateDownloadEnabled() }
	toolsButton := widget.NewButtonWithIcon("Media tools", theme.SettingsIcon(), func() {
		note := widget.NewLabel("First use: set up the YouTube tools below. Internet access and about 700 MB of free space are required. Downloads come from the original yt-dlp, Deno and FFmpeg releases and are verified with SHA-256. Your selected proxy is used. No automatic updates.")
		note.Wrapping = fyne.TextWrapWord
		var d dialog.Dialog
		setup := widget.NewButtonWithIcon("Set up YouTube tools", theme.DownloadIcon(), func() {
			dialog.ShowConfirm("Download YouTube tools?", "Download the pinned yt-dlp, Deno and FFmpeg tools into your private user cache? Their separate licenses apply. Existing downloads and tools will be kept.", func(yes bool) {
				if yes {
					d.Hide()
					y.installTools()
				}
			}, w)
		})
		d = dialog.NewCustom("Media tools", "Done", container.NewVBox(note, setup,
			widget.NewForm(widget.NewFormItem("Manifest", container.NewBorder(nil, nil, nil, y.browseTools, y.tools)))), w)
		d.Resize(fyne.NewSize(640, 330))
		d.Show()
	})
	toolsButton.Importance = widget.LowImportance
	y.scope = widget.NewSelect([]string{"This video", "Choose from playlist"}, nil)
	y.scope.PlaceHolder = "Choose from playlist"
	y.scope.SetSelected("This video")
	y.scope.OnChanged = func(string) { y.invalidate() }
	y.playlist = newYouTubePlaylist(y)
	y.playlistLoader = loadYouTubePlaylist
	connection := widget.NewButtonWithIcon("Connection", theme.InfoIcon(), func() {
		dialog.ShowInformation("Source connection", y.proxyNote.Text, w)
	})
	connection.Importance = widget.LowImportance
	heading := widget.NewRichTextFromMarkdown("## YouTube")
	subtitle := widget.NewLabel("Save videos, audio or a selection from a playlist.")
	header := container.NewBorder(nil, nil, nil, toolsButton, container.NewHBox(heading, subtitle))
	source := container.NewVBox(
		container.NewBorder(nil, nil, widget.NewLabel("Link"), container.NewHBox(y.analyze, y.cancel), y.link),
		container.NewHBox(widget.NewLabel("Import"), y.scope, y.useProxy, connection))
	emptyTitle := widget.NewLabelWithStyle("Start with a YouTube link", fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	emptyNote := widget.NewLabel("Paste a public video or playlist link above.\nAnalyze first, then choose what to download.")
	emptyNote.Alignment = fyne.TextAlignCenter
	y.empty = container.NewCenter(container.NewVBox(
		container.NewCenter(container.NewGridWrap(fyne.NewSize(40, 40), widget.NewIcon(theme.MediaVideoIcon()))),
		emptyTitle, emptyNote))
	y.content = container.NewPadded(container.NewBorder(
		container.NewVBox(header, source, widget.NewSeparator()),
		container.NewVBox(y.spinner, widget.NewSeparator(), y.status), nil, nil,
		container.NewStack(y.empty, y.options, y.playlist.content)))
	return y
}

func (y *youtubeView) installTools() {
	y.installToolsThen(nil)
}

func (y *youtubeView) installToolsThen(afterInstall func()) {
	if y.busy || y.closed || y.submitting {
		return
	}
	route, err := proxyForDownload(y.settings.Proxy, y.useProxy.Checked)
	if err != nil {
		y.status.SetText(err.Error())
		return
	}
	config := networkproxy.Config{}
	if route != nil {
		config = *route
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	y.cancelRun = cancel
	y.setBusy(true)
	y.status.SetText("Downloading and verifying YouTube tools… This can take several minutes. Cancel keeps existing tools and downloads.")
	setup, dispatch := y.setupTools, y.dispatch
	go func() {
		manifest, err := setup(ctx, config)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		cancel()
		dispatch(func() {
			y.cancelRun = nil
			y.setBusy(false)
			if y.closed {
				if y.onIdle != nil {
					done := y.onIdle
					y.onIdle = nil
					done()
				}
				return
			}
			if err != nil {
				if errors.Is(err, context.Canceled) {
					y.status.SetText("YouTube tool setup canceled. Existing tools and downloads are kept.")
				} else {
					y.status.SetText("YouTube tool setup failed. Check the connection, proxy, free space and permissions, then retry.")
				}
				return
			}
			y.tools.SetText(manifest)
			y.status.SetText("YouTube tools are ready. Paste a video or playlist link and choose Analyze.")
			if afterInstall != nil {
				afterInstall()
			}
		})
	}()
}

func (y *youtubeView) promptToInstallTools() {
	y.status.SetText("Media tools are missing or invalid. Install them to download from YouTube.")
	note := widget.NewLabel("FluxGet needs yt-dlp and its companion tools to read YouTube links and save video or audio. Installation needs internet access and about 700 MB of free space.")
	note.Wrapping = fyne.TextWrapWord
	prompt := dialog.NewCustomConfirm("Install YouTube tools?", "Install", "Later", note, func(install bool) {
		if install {
			y.installToolsThen(y.startAnalysis)
		}
	}, y.window)
	prompt.Resize(fyne.NewSize(500, 220))
	prompt.Show()
}

func (y *youtubeView) showAnalysisError(err error) {
	if errors.Is(err, media.ErrTools) {
		y.promptToInstallTools()
		return
	}
	y.status.SetText(youtubeAnalysisError(err))
}

func (y *youtubeView) invalidate() {
	if y.busy || y.closed || y.submitting {
		return
	}
	if y.video.ID != "" {
		y.status.SetText("The source or connection changed. Analyze again to refresh formats.")
	}
	y.video = media.Video{}
	if y.playlist != nil {
		y.playlist.reset()
	}
	y.options.Hide()
	y.empty.Show()
	y.updateDownloadEnabled()
}

func (y *youtubeView) setBusy(busy bool) {
	y.busy = busy
	if y.playlist != nil {
		y.playlist.setBusy(busy)
	}
	y.updateDownloadEnabled()
	for _, object := range []fyne.Disableable{y.link, y.tools, y.useProxy, y.browseTools, y.analyze, y.scope} {
		if busy {
			object.Disable()
		} else {
			object.Enable()
		}
	}
	if busy {
		y.cancel.Enable()
		y.spinner.Show()
		y.spinner.Start()
	} else {
		y.cancel.Disable()
		y.spinner.Stop()
		y.spinner.Hide()
	}
}

func (y *youtubeView) startAnalysis() {
	if y.busy || y.closed || y.submitting {
		return
	}
	if y.scope.Selected == "Choose from playlist" {
		y.playlist.reset()
		y.playlist.load(1)
		return
	}
	y.invalidate()
	source, err := media.NormalizeVideoURL(strings.TrimSpace(y.link.Text))
	if err != nil {
		if errors.Is(err, media.ErrPlaylist) {
			y.status.SetText("Choose from playlist to select entries from this link.")
		} else {
			y.status.SetText("Enter a public YouTube watch, youtu.be or Shorts HTTPS link.")
		}
		return
	}
	route, err := proxyForDownload(y.settings.Proxy, y.useProxy.Checked)
	if err != nil {
		y.status.SetText("Set a valid proxy address in Settings, or turn off Use proxy for this source.")
		return
	}
	y.route = networkproxy.Config{}
	if route != nil {
		y.route = *route
	}
	manifest := strings.TrimSpace(y.tools.Text)
	if !filepath.IsAbs(manifest) {
		y.promptToInstallTools()
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	y.cancelRun = cancel
	y.setBusy(true)
	message := "Analyzing video information…"
	if u, _ := url.Parse(y.link.Text); u != nil && u.Query().Get("list") != "" {
		message = "Analyzing this video only. The playlist in this link will not be imported."
	}
	y.status.SetText(message)
	analyze, dispatch, capturedRoute := y.analyzer, y.dispatch, y.route
	go func() {
		video, err := analyze(ctx, manifest, capturedRoute, source)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		cancel()
		dispatch(func() {
			y.setBusy(false)
			y.cancelRun = nil
			if y.closed {
				if y.onIdle != nil {
					done := y.onIdle
					y.onIdle = nil
					done()
				}
				return
			}
			if err != nil {
				y.showAnalysisError(err)
				return
			}
			y.video = video
			y.title.SetText(video.Title)
			y.details.SetText(fmt.Sprintf("Duration: %s · Video ID: %s", (time.Duration(video.Duration) * time.Second).String(), video.ID))
			y.filename.SetText(youtubeFilename(video.Title, video.ID))
			y.populateContainers()
			y.empty.Hide()
			y.options.Show()
			y.status.SetText("Video information loaded. Choose your output, then Download.")
			if u, _ := url.Parse(y.link.Text); u != nil && u.Query().Get("list") != "" {
				y.status.SetText("This video only. Its playlist was not imported. Choose your output, then Download.")
			}
		})
	}()
}

func youtubeAnalysisError(err error) string {
	switch {
	case errors.Is(err, context.Canceled):
		return "Analysis canceled. No media was downloaded."
	case errors.Is(err, context.DeadlineExceeded):
		return "Analysis timed out. Check your connection or proxy and try again."
	case errors.Is(err, media.ErrMetadata):
		return "This video has no supported public formats. Live, private and login-required sources are not supported."
	default:
		// Never display arbitrary subprocess/network errors or signed URLs.
		return "Could not analyze this video. Check the link, connection or proxy and media-tool compatibility. The video may be unavailable."
	}
}

func (y *youtubeView) cancelAnalysis() {
	if y.busy && y.cancelRun != nil {
		y.cancelRun()
		y.cancel.Disable()
		y.status.SetText("Canceling…")
	}
}

func (y *youtubeView) shutdown() {
	y.closed = true
	y.cancelAnalysis()
}

func (y *youtubeView) populateContainers() {
	if y.video.ID == "" {
		return
	}
	audioOnly := y.kind.Selected == "Audio only"
	formats := []string{"MP4", "WebM", "MKV"}
	if audioOnly {
		formats = []string{"M4A", "Opus", "MP3"}
	}
	available := make([]string, 0, len(formats))
	for _, format := range formats {
		videos, audios := youtubeFormats(y.video, format, audioOnly)
		if !audioOnly && len(videos) > 0 || audioOnly && len(audios) > 0 {
			available = append(available, format)
		}
	}
	y.format.ClearSelected()
	y.format.SetOptions(available)
	if len(available) == 0 {
		y.populateFormats()
		return
	}
	y.format.SetSelectedIndex(0)
}

func (y *youtubeView) populateFormats() {
	y.videos, y.audios = youtubeFormats(y.video, y.format.Selected, y.kind.Selected == "Audio only")
	y.quality.ClearSelected()
	labels := make([]string, len(y.videos))
	chosen := -1
	for i, f := range y.videos {
		labels[i] = youtubeQuality(f)
		if chosen < 0 && f.Height > 0 && f.Height <= 1080 {
			chosen = i
		}
	}
	y.quality.SetOptions(labels)
	y.quality.Disable()
	if len(labels) > 0 {
		y.quality.Enable()
		if chosen < 0 {
			chosen = len(labels) - 1
		}
		y.quality.SetSelectedIndex(chosen)
	} else {
		y.populateAudio()
	}
}

func (y *youtubeView) populateAudio() {
	y.audio.ClearSelected()
	y.audio.Disable()
	if i := y.quality.SelectedIndex(); i >= 0 && i < len(y.videos) && y.videos[i].AudioCodec != "none" {
		y.audio.SetOptions([]string{"Included in video · " + y.videos[i].AudioCodec})
		y.audio.SetSelectedIndex(0)
	} else {
		labels := make([]string, len(y.audios))
		for i, f := range y.audios {
			labels[i] = youtubeAudio(f)
		}
		y.audio.SetOptions(labels)
		if len(labels) > 0 {
			y.audio.Enable()
			y.audio.SetSelectedIndex(0)
		}
	}
	y.selectionChanged()
}

func (y *youtubeView) selection() (media.Selection, error) {
	s := media.Selection{Container: strings.ToLower(y.format.Selected)}
	if y.kind.Selected == "Video" {
		i := y.quality.SelectedIndex()
		if i < 0 || i >= len(y.videos) {
			return s, media.ErrSelection
		}
		f := y.videos[i]
		s.VideoFormat = f.ID
		if f.AudioCodec != "none" {
			return s, s.Validate(y.video)
		}
	}
	i := y.audio.SelectedIndex()
	if i < 0 || i >= len(y.audios) {
		return s, media.ErrSelection
	}
	s.AudioFormat = y.audios[i].ID
	if s.Container == "mp3" {
		s.MP3Kbps, _ = strconv.Atoi(strings.TrimSuffix(y.bitrate.Selected, " kbps"))
	}
	return s, s.Validate(y.video)
}

func (y *youtubeView) selectionChanged() {
	y.bitrate.Disable()
	y.bitratePanel.Hide()
	if y.format.Selected == "MP3" {
		y.bitrate.Enable()
		y.bitratePanel.Show()
	}
	note := "Video and audio are combined without increasing source quality. Only compatible formats are shown."
	if y.kind.Selected == "Video" && y.format.Selected != "" && y.format.Selected != "MP4" {
		note = "MP4-compatible output is not selected. Review the selected container and codecs for your player."
		if !slices.Contains(y.format.Options, "MP4") {
			note = "MP4 is unavailable for this video. Review the selected alternative container and codecs for your player."
		}
	} else if y.kind.Selected == "Audio only" {
		note = "Audio is saved in its available source format."
		if y.format.Selected == "MP3" {
			note = "MP3 conversion takes extra processing and does not improve source quality."
		}
	}
	s, err := y.selection()
	y.download.Disable()
	if err != nil {
		y.size.SetText("No compatible formats are available for this choice.")
	} else {
		y.updateDownloadEnabled()
		y.size.SetText(youtubeSize(y.video, s, y.settings.DisplayUnit))
		if i := y.quality.SelectedIndex(); s.VideoFormat != "" && i >= 0 && y.videos[i].Height > 1080 {
			note += " Selected quality is above the default 1080p maximum. No upscaling is performed."
		}
	}
	y.formatNote.SetText(note)
}

func (y *youtubeView) updateDownloadEnabled() {
	y.download.Disable()
	if y.playlist != nil {
		y.playlist.updateDownload()
	}
	if y.closed || y.busy || y.submitting || y.video.ID == "" || y.submit == nil || y.canDownload != nil && !y.canDownload() {
		return
	}
	if _, err := y.selection(); err == nil {
		y.download.Enable()
	}
}

func (y *youtubeView) downloadSelection() {
	if y.closed || y.busy || y.submitting || y.submit == nil {
		return
	}
	selection, err := y.selection()
	if err != nil {
		y.status.SetText("Choose an available video and audio combination.")
		return
	}
	path, err := youtubeDestination(strings.TrimSpace(y.folder.Text), strings.TrimSpace(y.filename.Text), selection.Container, y.sortFiles.Checked)
	if err != nil {
		y.status.SetText(err.Error())
		return
	}
	job, err := media.NewJob(y.video, selection, strings.TrimSpace(y.tools.Text))
	if err != nil {
		y.status.SetText("This selection is unavailable. Analyze the link again.")
		return
	}
	entry := queueEntry{Media: &job, URL: "https://www.youtube.com/watch?v=" + y.video.ID, Destination: path}
	if y.route.Enabled {
		route := y.route
		entry.Proxy = &route
	}
	y.submitting = true
	y.download.Disable()
	y.analyze.Disable()
	y.status.SetText("Adding to Downloads…")
	y.submit(entry, func(err error) {
		y.submitting = false
		if y.closed {
			return
		}
		y.analyze.Enable()
		if err != nil {
			y.status.SetText(err.Error())
			y.updateDownloadEnabled()
			return
		}
		y.invalidate()
		y.status.SetText("Added to Downloads. Pause, resume or retry it there.")
	})
}
