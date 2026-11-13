package main

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"github.com/Parsania80/FluxGet/media"
	"github.com/Parsania80/FluxGet/networkproxy"
)

func youtubeFixture() media.Video {
	return media.Video{ID: "AAAAAAAAAAA", Title: "A sample video / with a title", Duration: 597, Formats: []media.Format{
		{ID: "2160", Height: 2160, FPS: 60, VideoCodec: "avc1.640033", AudioCodec: "none", Protocol: "https", Size: 30000000},
		{ID: "137", Height: 1080, FPS: 30, VideoCodec: "avc1.640028", AudioCodec: "none", Protocol: "https", Size: 10000000},
		{ID: "18", Height: 360, VideoCodec: "avc1.42001e", AudioCodec: "mp4a.40.2", Protocol: "https", Size: 2000000},
		{ID: "web", Height: 720, FPS: 60, VideoCodec: "vp9", AudioCodec: "none", Protocol: "https", EstimatedSize: 8000000},
		{ID: "140", VideoCodec: "none", AudioCodec: "mp4a.40.2", Language: "en", AudioBitrate: 128, Protocol: "https", Size: 1000000},
		{ID: "251", VideoCodec: "none", AudioCodec: "opus", Language: "fa", AudioBitrate: 160, Protocol: "https", EstimatedSize: 1500000},
		{ID: "drm", Height: 4320, VideoCodec: "avc1", AudioCodec: "mp4a", Protocol: "https", DRM: true},
		{ID: "storyboard", VideoCodec: "none", AudioCodec: "none", Protocol: "mhtml"},
	}}
}

func youtubeTestView(t *testing.T) (*youtubeView, chan func()) {
	t.Helper()
	setTestConfig(t, t.TempDir())
	a := test.NewApp()
	a.Settings().SetTheme(fluxTheme{theme.DefaultTheme()})
	t.Cleanup(a.Quit)
	w := a.NewWindow("FluxGet — YouTube")
	t.Cleanup(w.Close)
	s := &speedSettings{Folder: t.TempDir(), DisplayUnit: "MB"}
	y := newYouTubeView(w, s, filepath.Join(t.TempDir(), "fluxget"))
	queue := make(chan func(), 4)
	// Deliver worker results on the test's UI thread, like Fyne's real driver.
	y.dispatch = func(fn func()) { queue <- fn }
	t.Cleanup(func() { y.shutdown() })
	w.SetContent(y.content)
	w.Resize(fyne.NewSize(1100, 720))
	w.Show()
	return y, queue
}

func TestYouTubeToolSetupLifecycleAndCancellation(t *testing.T) {
	y, queue := youtubeTestView(t)
	previous := y.tools.Text
	var captured networkproxy.Config
	y.settings.Proxy = networkproxy.Config{Enabled: true, URL: "socks5://127.0.0.1:10808"}
	y.useProxy.SetChecked(true)
	manifest := filepath.Join(t.TempDir(), "tools.json")
	calls := 0
	y.setupTools = func(_ context.Context, route networkproxy.Config) (string, error) {
		calls++
		captured = route
		return manifest, nil
	}
	y.installTools()
	y.installTools()
	finishPlaylist(t, y, queue)
	if calls != 1 || y.tools.Text != manifest || !captured.Enabled || !strings.Contains(y.status.Text, "tools are ready") {
		t.Fatal("setup did not snapshot proxy or publish manifest", calls, y.status.Text)
	}
	y.tools.SetText(previous)
	y.setupTools = func(ctx context.Context, _ networkproxy.Config) (string, error) {
		<-ctx.Done()
		return manifest, ctx.Err()
	}
	y.installTools()
	test.Tap(y.cancel)
	finishPlaylist(t, y, queue)
	if y.tools.Text != previous || !strings.Contains(y.status.Text, "canceled") {
		t.Fatal("canceled setup changed tool selection", y.status.Text)
	}
	y.setupTools = func(context.Context, networkproxy.Config) (string, error) {
		return "", errors.New("https://secret.invalid/?token=PRIVATE")
	}
	y.installTools()
	finishPlaylist(t, y, queue)
	if y.tools.Text != previous || strings.Contains(y.status.Text, "PRIVATE") || !strings.Contains(y.status.Text, "failed") {
		t.Fatal("setup failure leaked details or changed manifest", y.status.Text)
	}
}

func finishYouTubeAnalysis(t *testing.T, queue chan func()) {
	t.Helper()
	select {
	case apply := <-queue:
		apply()
	case <-time.After(5 * time.Second):
		t.Fatal("analysis did not finish")
	}
}

func loadYouTubeFixture(t *testing.T, y *youtubeView, queue chan func()) {
	t.Helper()
	y.analyzer = func(context.Context, string, networkproxy.Config, string) (media.Video, error) {
		return youtubeFixture(), nil
	}
	y.link.SetText("https://youtu.be/AAAAAAAAAAA")
	test.Tap(y.analyze)
	finishYouTubeAnalysis(t, queue)
}

func TestYouTubeSelectionsAndSingleDownloadSubmission(t *testing.T) {
	y, queue := youtubeTestView(t)
	loadYouTubeFixture(t, y, queue)
	if !y.options.Visible() || y.format.Selected != "MP4" || y.kind.Selected != "Video" {
		t.Fatal("selection screen did not default to available MP4 video")
	}
	selection, err := y.selection()
	if err != nil || selection.VideoFormat != "137" || selection.AudioFormat != "140" {
		t.Fatalf("expected H.264/AAC up to 1080p, got %+v: %v", selection, err)
	}
	if !strings.Contains(y.size.Text, "11.00 MB") {
		t.Fatal("source size did not combine video/audio in the selected unit", y.size.Text)
	}
	calls := 0
	var submitted queueEntry
	y.submit = func(entry queueEntry, finish func(error)) {
		calls++
		submitted = entry
		finish(errors.New("fixture queue busy"))
	}
	y.updateDownloadEnabled()
	test.Tap(y.download)
	if calls != 1 || submitted.Media == nil || submitted.Media.Selection.VideoFormat != "137" || y.download.Text != "Download" {
		t.Fatal("Download did not submit the selected job exactly once")
	}
	files, err := os.ReadDir(y.folder.Text)
	if err != nil || len(files) != 0 {
		t.Fatal("analyze/submission created transfer files", err)
	}
	y.quality.SetSelectedIndex(2)
	selection, err = y.selection()
	if err != nil || selection.VideoFormat != "18" || selection.AudioFormat != "" || !y.audio.Disabled() {
		t.Fatal("muxed video retained separate audio or stale review", err)
	}
	y.kind.SetSelected("Audio only")
	y.format.SetSelected("MP3")
	y.bitrate.SetSelected("256 kbps")
	selection, err = y.selection()
	if err != nil || selection.VideoFormat != "" || selection.AudioFormat != "251" || selection.MP3Kbps != 256 || y.bitrate.Disabled() || !y.quality.Disabled() {
		t.Fatalf("audio/MP3 choice is invalid: %+v %v", selection, err)
	}
	if !strings.Contains(y.audio.Selected, "fa") || !strings.Contains(y.formatNote.Text, "does not improve") {
		t.Fatal("missing audio-language or conversion information")
	}
	y.format.SetSelected("Opus")
	selection, err = y.selection()
	if err != nil || selection.AudioFormat != "251" || !y.bitrate.Disabled() || !strings.Contains(y.size.Text, "Estimated") {
		t.Fatal("native audio or estimated size incorrect", err)
	}
	y.link.SetText("https://youtu.be/BBBBBBBBBBB")
	if y.video.ID != "" || y.options.Visible() {
		t.Fatal("source edit retained old video choices")
	}
}

func TestYouTubeAnalysisSnapshotsProxyAndOnlyOneVideo(t *testing.T) {
	y, queue := youtubeTestView(t)
	y.settings.Proxy = networkproxy.Config{URL: "socks5://127.0.0.1:10808"}
	y.useProxy.SetChecked(true)
	type request struct {
		route  networkproxy.Config
		source string
	}
	got := make(chan request, 1)
	y.analyzer = func(_ context.Context, _ string, route networkproxy.Config, source string) (media.Video, error) {
		got <- request{route, source}
		return youtubeFixture(), nil
	}
	y.link.SetText("https://www.youtube.com/watch?v=AAAAAAAAAAA&list=PLexample")
	test.Tap(y.analyze)
	finishYouTubeAnalysis(t, queue)
	r := <-got
	if r.source != "https://www.youtube.com/watch?v=AAAAAAAAAAA" || r.route.URL != "socks5h://127.0.0.1:10808" || !r.route.Enabled || !strings.Contains(y.status.Text, "playlist was not imported") {
		t.Fatal("analysis route or single-video consent incorrect", r)
	}
	// The selection keeps its explicitly analyzed route if defaults change.
	y.settings.Proxy = networkproxy.Config{}
	var submitted queueEntry
	y.submit = func(entry queueEntry, finish func(error)) { submitted = entry; finish(nil) }
	y.updateDownloadEnabled()
	test.Tap(y.download)
	if submitted.Proxy == nil || submitted.Proxy.URL != "socks5h://127.0.0.1:10808" {
		t.Fatal("download silently changed its captured route")
	}

	y.useProxy.SetChecked(false)
	playlistCalls := 0
	y.playlistLoader = func(context.Context, string, networkproxy.Config, string, int) (media.PlaylistPage, error) {
		playlistCalls++
		return media.PlaylistPage{ID: "PLexample", Title: "Playlist", First: 1}, nil
	}
	y.link.SetText("https://www.youtube.com/playlist?list=PLexample")
	test.Tap(y.analyze)
	finishYouTubeAnalysis(t, queue)
	if y.scope.Selected != "Choose from playlist" || playlistCalls != 1 || y.video.ID != "" || !y.playlist.content.Visible() {
		t.Fatal("playlist-only URL did not use explicit playlist intake")
	}

}

func TestYouTubeCancelAndCloseWaitForAnalysisReaping(t *testing.T) {
	for _, closeApp := range []bool{false, true} {
		t.Run(map[bool]string{false: "cancel", true: "close"}[closeApp], func(t *testing.T) {
			y, queue := youtubeTestView(t)
			started, canceled, reaped := make(chan struct{}), make(chan struct{}), make(chan struct{})
			y.analyzer = func(ctx context.Context, _ string, _ networkproxy.Config, _ string) (media.Video, error) {
				close(started)
				<-ctx.Done()
				close(canceled)
				<-reaped
				return youtubeFixture(), nil // A late success must be discarded.
			}
			y.link.SetText("https://youtu.be/AAAAAAAAAAA")
			test.Tap(y.analyze)
			<-started
			closed := false
			if closeApp {
				v := &downloadsView{youtube: y, status: y.status, closing: true, closeDone: func() { closed = true }}
				y.shutdown()
				v.finishClose()
			} else {
				test.Tap(y.cancel)
			}
			<-canceled
			if !y.busy || !y.analyze.Disabled() || closed {
				t.Fatal("restarted or closed before owned analysis returned")
			}
			close(reaped)
			finishYouTubeAnalysis(t, queue)
			if y.busy || y.options.Visible() || y.video.ID != "" || closeApp && !closed || !closeApp && !strings.Contains(y.status.Text, "canceled") {
				t.Fatal("late success published or shutdown did not finish")
			}
		})
	}
}

func TestYouTubeErrorsAreActionableAndRedacted(t *testing.T) {
	y, queue := youtubeTestView(t)
	y.link.SetText("https://youtu.be/AAAAAAAAAAA")
	// The real adapter must reject a missing manifest without starting a tool.
	y.tools.SetText(filepath.Join(t.TempDir(), "missing.json"))
	test.Tap(y.analyze)
	finishYouTubeAnalysis(t, queue)
	if !strings.Contains(y.status.Text, "Media tools are missing") || y.options.Visible() {
		t.Fatal("missing-tool action is unclear", y.status.Text)
	}
	y.analyzer = func(context.Context, string, networkproxy.Config, string) (media.Video, error) {
		return media.Video{}, errors.New("https://cdn.invalid/?token=PRIVATE-TOKEN")
	}
	test.Tap(y.analyze)
	finishYouTubeAnalysis(t, queue)
	if strings.Contains(y.status.Text, "PRIVATE-TOKEN") || !strings.Contains(y.status.Text, "Could not analyze") {
		t.Fatal("tool error was exposed or not explained")
	}
	y.useProxy.SetChecked(true)
	test.Tap(y.analyze)
	if y.busy || !strings.Contains(y.status.Text, "Set a valid proxy") {
		t.Fatal("missing proxy was not rejected before network work")
	}
}

func TestYouTubeUnavailableFormatsAndExplicitAlternative(t *testing.T) {
	y, queue := youtubeTestView(t)
	y.analyzer = func(context.Context, string, networkproxy.Config, string) (media.Video, error) {
		v := youtubeFixture()
		v.Formats = []media.Format{v.Formats[3], v.Formats[5]}
		return v, nil
	}
	y.link.SetText("https://youtu.be/AAAAAAAAAAA")
	test.Tap(y.analyze)
	finishYouTubeAnalysis(t, queue)
	if y.format.Selected != "WebM" || slices.Contains(y.format.Options, "MP4") || !strings.Contains(y.formatNote.Text, "MP4 is unavailable") {
		t.Fatal("unavailable MP4 was offered or alternative hidden")
	}
	y.video.Formats = nil
	y.populateContainers()
	if !y.download.Disabled() || len(y.format.Options) != 0 {
		t.Fatal("unavailable formats remain reviewable")
	}
}

func TestYouTubeDestinationReviewPreservesFiles(t *testing.T) {
	folder := t.TempDir()
	for _, name := range []string{"../escape", "a/b", "a\\b", "name:stream", "CON", "con .txt", "COM¹", "NUL.txt", "trailing.", "bad\x00name"} {
		if _, err := youtubeDestination(folder, name, "mp4", false); err == nil {
			t.Fatalf("accepted unsafe filename %q", name)
		}
	}
	name := youtubeFilename(strings.Repeat("🎬", 100)+" /\\bad", "AAAAAAAAAAA")
	path, err := youtubeDestination(folder, name, "mp4", false)
	if err != nil || filepath.Dir(path) != folder {
		t.Fatal("sanitized Unicode filename invalid", err)
	}
	if err := os.WriteFile(path, []byte("keep existing"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := youtubeDestination(folder, name, "mp4", false); err == nil {
		t.Fatal("existing file could be overwritten")
	}
	data, _ := os.ReadFile(path)
	if string(data) != "keep existing" {
		t.Fatal("review modified existing file")
	}
	if _, err := youtubeDestination("relative", "video", "mp4", false); err == nil {
		t.Fatal("accepted relative folder")
	}
	if _, err := youtubeDestination(folder, "video", "../escape", false); err == nil {
		t.Fatal("accepted arbitrary extension")
	}
}

func TestYouTubeSortsSelectedVideoAndAudioOutputs(t *testing.T) {
	y, queue := youtubeTestView(t)
	loadYouTubeFixture(t, y, queue)
	var submitted queueEntry
	y.submit = func(entry queueEntry, finish func(error)) {
		submitted = entry
		finish(errors.New("fixture admission"))
	}
	y.sortFiles.SetChecked(true)
	for _, tc := range []struct{ kind, format, folder string }{
		{"Video", "MP4", "Videos"}, {"Audio only", "MP3", "Music"},
	} {
		y.kind.SetSelected(tc.kind)
		y.format.SetSelected(tc.format)
		test.Tap(y.download)
		want := filepath.Join(y.folder.Text, tc.folder, y.filename.Text+"."+strings.ToLower(tc.format))
		if submitted.Destination != want || submitted.Media == nil || submitted.SortFiles || submitted.AutomaticName {
			t.Fatalf("wrong sorted media admission: %+v, want %s", submitted, want)
		}
	}
	y.sortFiles.SetChecked(false)
	test.Tap(y.download)
	if filepath.Dir(submitted.Destination) != y.folder.Text {
		t.Fatal("media sorting opt-out ignored")
	}
}

func TestYouTubeTabAndSelectionScreenshot(t *testing.T) {
	y, queue := youtubeTestView(t)
	v := newDownloadsView(fyne.CurrentApp(), y.window, y.settings, nil, filepath.Join(t.TempDir(), "fluxget"))
	y = v.youtube
	t.Cleanup(y.shutdown)
	y.dispatch = func(fn func()) { queue <- fn }
	loadYouTubeFixture(t, y, queue)
	v.ready = true
	v.status.SetText("Downloads ready")
	y.updateDownloadEnabled()
	var tabs *container.AppTabs
	var walk func(fyne.CanvasObject)
	walk = func(object fyne.CanvasObject) {
		switch o := object.(type) {
		case *container.AppTabs:
			tabs = o
		case *fyne.Container:
			for _, child := range o.Objects {
				walk(child)
			}
		}
	}
	walk(v.content)
	if tabs == nil || len(tabs.Items) != 3 || tabs.Items[1].Text != "YouTube" || tabs.Items[1].Content != y.content {
		t.Fatal("Downloads, YouTube and Settings tabs are not integrated")
	}
	y.window.SetContent(v.content)
	tabs.SelectIndex(1)
	if tabs.Selected().Text != "YouTube" {
		t.Fatal("YouTube page unavailable")
	}
	for _, size := range []fyne.Size{fyne.NewSize(900, 600), fyne.NewSize(1100, 720)} {
		y.window.Resize(size)
		assertControlsVisible(t, y.window, y.download, y.analyze)
	}
	if path := os.Getenv("FLUXGET_YOUTUBE_SCREENSHOT"); path != "" {
		file, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, y.window.Canvas().Capture())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestYouTubeEmptyWorkspaceScreenshot(t *testing.T) {
	y, _ := youtubeTestView(t)
	if !y.empty.Visible() || y.options.Visible() || y.playlist.content.Visible() {
		t.Fatal("new workspace shows stale results")
	}
	if path := os.Getenv("FLUXGET_YOUTUBE_SCREENSHOT"); path != "" {
		file, err := os.Create(strings.TrimSuffix(path, ".png") + "-empty.png")
		if err != nil {
			t.Fatal(err)
		}
		err = png.Encode(file, y.window.Canvas().Capture())
		file.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
}

func assertControlsVisible(t *testing.T, window fyne.Window, objects ...fyne.CanvasObject) {
	t.Helper()
	viewport := window.Canvas().Size()
	for _, object := range objects {
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(object)
		size := object.Size()
		if !object.Visible() || size.Width <= 0 || size.Height <= 0 || pos.X < 0 || pos.Y < 0 || pos.X+size.Width > viewport.Width || pos.Y+size.Height > viewport.Height {
			t.Fatalf("control %T at %v size %v is outside viewport %v", object, pos, size, viewport)
		}
	}
}

func TestYouTubeDownloadButtonGuardsPendingAndExistingDestination(t *testing.T) {
	y, queue := youtubeTestView(t)
	loadYouTubeFixture(t, y, queue)
	calls := 0
	var finish func(error)
	var submitted queueEntry
	y.submit = func(entry queueEntry, done func(error)) { calls++; submitted = entry; finish = done }
	y.updateDownloadEnabled()
	test.Tap(y.download)
	test.Tap(y.download)
	if calls != 1 || !y.submitting || !y.download.Disabled() || submitted.Media == nil {
		t.Fatal("duplicate pending download submitted")
	}
	if len(submitted.Media.Video.Formats) != 2 {
		t.Fatal("saved unrelated formats")
	}
	finish(nil)
	if y.submitting || y.options.Visible() || !y.download.Disabled() {
		t.Fatal("successful submit left a duplicate download action active")
	}
	loadYouTubeFixture(t, y, queue)
	path, err := youtubeDestination(y.folder.Text, y.filename.Text, "mp4", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("keep existing"), 0600); err != nil {
		t.Fatal(err)
	}
	test.Tap(y.download)
	if calls != 1 || !strings.Contains(y.status.Text, "unavailable") {
		t.Fatal("conflicting destination reached the queue")
	}
}

func TestYouTubeDownloadRowsShowProcessingWithoutSpeedOrParts(t *testing.T) {
	j := queueEntry{ID: 1, State: "running", Media: &media.Job{}, MediaPhase: "processing", Activity: "Merging / converting", Progress: "Downloaded 100/100 bytes"}
	speed := float64(99999)
	j.Speed = &speed
	if j.displayState() != "Processing" || downloadSpeed(j, "MB") != "Speed: —" {
		t.Fatal("processing claims a network speed")
	}
	rows := downloadRows([]queueEntry{j}, nil, "Downloading")
	if len(rows) != 1 {
		t.Fatal("processing media disappeared from active downloads")
	}
	a := test.NewApp()
	defer a.Quit()
	panel := newSegmentPanel()
	updateSegmentPanel(panel, j, func() {})
	if panel.Visible() {
		t.Fatal("media fragments shown as engine range connections")
	}
}
