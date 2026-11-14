package main

import (
	"context"
	"errors"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/Parsania80/FluxGet/media"
	"github.com/Parsania80/FluxGet/networkproxy"
)

func finishPlaylist(t *testing.T, y *youtubeView, queue chan func()) {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for y.busy {
		select {
		case fn := <-queue:
			fn()
		case <-deadline.C:
			t.Fatal("playlist operation did not finish")
		}
	}
}
func loadPlaylistFixture(t *testing.T, y *youtubeView, queue chan func()) {
	t.Helper()
	y.playlistLoader = func(_ context.Context, _ string, _ networkproxy.Config, _ string, first int) (media.PlaylistPage, error) {
		return media.PlaylistPage{ID: "PLexample", Title: "Example playlist", First: first, HasMore: first == 1, Entries: []media.PlaylistEntry{
			{ID: "AAAAAAAAAAA", Title: "First video", Index: first},
			{ID: "BBBBBBBBBBB", Title: "Lower quality video", Index: first + 1},
			{ID: "CCCCCCCCCCC", Title: "[Private video]", Index: first + 2, Unavailable: true},
			{ID: "DDDDDDDDDDD", Title: "Unavailable format", Index: first + 3},
		}}, nil
	}
	y.link.SetText("https://www.youtube.com/playlist?list=PLexample")
	test.Tap(y.analyze)
	finishPlaylist(t, y, queue)
}
func playlistAnalyzer(calls *[]string) youtubeAnalyzer {
	return func(_ context.Context, _ string, _ networkproxy.Config, source string) (media.Video, error) {
		*calls = append(*calls, source)
		v := youtubeFixture()
		v.ID = strings.TrimPrefix(source, "https://www.youtube.com/watch?v=")
		if v.ID == "BBBBBBBBBBB" {
			v.Formats[1].Height = 720
		}
		if v.ID == "DDDDDDDDDDD" {
			v.Formats = nil
		}
		return v, nil
	}
}
func TestYouTubePlaylistSelectionPagingAndLowerQualityConsent(t *testing.T) {
	y, queue := youtubeTestView(t)
	calls := []string{}
	y.analyzer = playlistAnalyzer(&calls)
	loadPlaylistFixture(t, y, queue)
	p := y.playlist
	if !p.content.Visible() || len(calls) != 0 || y.options.Visible() || y.empty.Visible() || p.acceptLower.Visible() || p.bitratePanel.Visible() {
		t.Fatal("enumeration loaded formats or single-video selection")
	}
	test.Tap(p.all)
	if len(p.selected) != 3 {
		t.Fatal("select page included unavailable entries", len(p.selected))
	}
	test.Tap(p.next)
	finishPlaylist(t, y, queue)
	if len(p.selected) != 3 || p.page.First != 101 || !p.next.Disabled() {
		t.Fatal("paging lost selection or end bound")
	}
	test.Tap(p.previous)
	finishPlaylist(t, y, queue)
	y.submitPlaylist = func(string, []queueEntry, func([]media.PlaylistResult, error)) {}
	test.Tap(p.prepare)
	finishPlaylist(t, y, queue)
	if len(calls) != 3 || len(p.jobs) != 3 || p.selected[2].job == nil || !p.selected[2].lower || p.selected[4].job != nil {
		t.Fatal("per-item resolution or skip state incorrect", calls)
	}
	if !p.download.Disabled() || !p.acceptLower.Visible() {
		t.Fatal("lower quality silently accepted")
	}
	test.Tap(p.acceptLower)
	if p.download.Disabled() {
		t.Fatal("explicit lower quality approval did not enable Download")
	}
	var batch string
	var submitted []queueEntry
	var finish func([]media.PlaylistResult, error)
	y.submitPlaylist = func(id string, jobs []queueEntry, done func([]media.PlaylistResult, error)) {
		batch = id
		submitted = jobs
		finish = done
	}
	test.Tap(p.download)
	test.Tap(p.download)
	if len(submitted) != 3 || !y.submitting || !y.busy {
		t.Fatal("batch not single-submit guarded")
	}
	for i, job := range submitted {
		if job.Media.Playlist.Batch != batch || job.Media.Playlist.Index != []int{1, 2, 4}[i] {
			t.Fatal("playlist order/group not preserved")
		}
	}
	finish(nil, errors.New("lost acknowledgment"))
	if p.batch != batch || p.download.Disabled() {
		t.Fatal("lost acknowledgment discarded replay token")
	}
	test.Tap(p.download)
	finish([]media.PlaylistResult{{Index: 1, JobID: 1}, {Index: 2, JobID: 2}, {Index: 4, Error: "skip"}}, nil)
	if len(p.selected) != 0 || !p.download.Disabled() || !strings.Contains(y.status.Text, "2 added, 1 skipped") {
		t.Fatal("submission summary or duplicate guard missing", y.status.Text)
	}
}
func TestYouTubePlaylistBatchLimitRowReuseAndPreferenceInvalidation(t *testing.T) {
	y, queue := youtubeTestView(t)
	loadPlaylistFixture(t, y, queue)
	p := y.playlist
	for i := 1; i <= 1000; i++ {
		p.selected[i] = playlistChoice{entry: media.PlaylistEntry{ID: "AAAAAAAAAAA", Index: i}}
	}
	p.choose(media.PlaylistEntry{ID: "BBBBBBBBBBB", Index: 1001}, true)
	if len(p.selected) != 1000 || !strings.Contains(y.status.Text, "limited to 1,000") {
		t.Fatal("batch cap bypassed")
	}
	test.Tap(p.clear)
	row := p.list.CreateItem()
	p.list.UpdateItem(0, row)
	header := row.(*fyne.Container).Objects[0].(*fyne.Container)
	check := header.Objects[1].(*widget.Check)
	p.list.UpdateItem(1, row)
	test.Tap(check)
	if _, ok := p.selected[2]; !ok || len(p.selected) != 1 {
		t.Fatal("recycled checkbox changed the wrong entry")
	}
	p.jobs = []queueEntry{{}}
	p.batch = strings.Repeat("a", 32)
	choice := p.selected[2]
	choice.note, choice.lower, choice.job = "Ready · old format", true, &media.Job{}
	p.selected[2] = choice
	p.acceptLower.Show()
	p.format.SetSelected("MKV")
	if len(p.jobs) != 0 || p.batch != "" || p.selected[2].job != nil || p.selected[2].note != "" || p.acceptLower.Visible() || p.download.Text != "Download" {
		t.Fatal("output edit kept stale prepared jobs")
	}
	y.scope.SetSelected("This video")
	if p.content.Visible() || len(p.selected) != 0 {
		t.Fatal("scope change retained playlist draft")
	}
}
func TestYouTubePlaylistResolutionNeverChangesVideoToAudioOrUpscales(t *testing.T) {
	v := youtubeFixture()
	v.Formats = []media.Format{v.Formats[0], v.Formats[5]}
	if _, _, err := playlistSelection(v, "MP4", false, 1080, 192); err == nil {
		t.Fatal("upscaled or incompatible video accepted")
	}
	if _, _, err := playlistSelection(v, "WebM", false, 1080, 192); err == nil {
		t.Fatal("video silently fell back to audio")
	}
	s, _, err := playlistSelection(v, "MP3", true, 1080, 320)
	if err != nil || s.VideoFormat != "" || s.AudioFormat != "251" || s.MP3Kbps != 320 {
		t.Fatal(s, err)
	}
}
func TestYouTubePlaylistCanceledLateSuccessIsDiscarded(t *testing.T) {
	y, queue := youtubeTestView(t)
	started := make(chan struct{})
	y.playlistLoader = func(ctx context.Context, _ string, _ networkproxy.Config, _ string, _ int) (media.PlaylistPage, error) {
		close(started)
		<-ctx.Done()
		return media.PlaylistPage{ID: "PLexample", Title: "late", First: 1}, nil
	}
	y.link.SetText("https://www.youtube.com/playlist?list=PLexample")
	test.Tap(y.analyze)
	<-started
	test.Tap(y.cancel)
	finishPlaylist(t, y, queue)
	if y.playlist.content.Visible() || y.playlist.page.ID != "" || !strings.Contains(y.status.Text, "canceled") {
		t.Fatal("canceled late page published")
	}
	loadPlaylistFixture(t, y, queue)
	p := y.playlist
	p.choose(p.page.Entries[0], true)
	y.analyzer = func(ctx context.Context, _ string, _ networkproxy.Config, _ string) (media.Video, error) {
		<-ctx.Done()
		return youtubeFixture(), nil
	}
	test.Tap(p.prepare)
	test.Tap(y.cancel)
	finishPlaylist(t, y, queue)
	if len(p.jobs) != 0 || !p.download.Disabled() {
		t.Fatal("canceled preparation retained jobs")
	}
}
func TestYouTubePlaylistScreenshotAndDownloadsSummary(t *testing.T) {
	y, queue := youtubeTestView(t)
	v := newDownloadsView(fyne.CurrentApp(), y.window, y.settings, nil, filepath.Join(t.TempDir(), "fluxget"))
	y = v.youtube
	y.dispatch = func(fn func()) { queue <- fn }
	loadPlaylistFixture(t, y, queue)
	var calls []string
	y.analyzer = playlistAnalyzer(&calls)
	test.Tap(y.playlist.all)
	test.Tap(y.playlist.prepare)
	finishPlaylist(t, y, queue)
	v.accept(desktopResponse{Revision: 1, Playlists: []media.PlaylistSummary{{Completed: 2, Failed: 1, Pending: 3, Skipped: 4}}})
	if !strings.Contains(v.summary.Text, "2 completed · 1 failed/canceled · 3 pending · 4 skipped") {
		t.Fatal(v.summary.Text)
	}
	v.ready = true
	v.status.SetText("Downloads ready")
	test.Tap(y.playlist.acceptLower)
	if y.playlist.download.Disabled() || y.playlist.download.Text != "Download 2" {
		t.Fatal("ready download count does not match the prepared videos")
	}
	var tabs *container.AppTabs
	var walk func(fyne.CanvasObject)
	walk = func(o fyne.CanvasObject) {
		switch c := o.(type) {
		case *container.AppTabs:
			tabs = c
		case *fyne.Container:
			for _, child := range c.Objects {
				walk(child)
			}
		}
	}
	walk(v.content)
	if tabs == nil {
		t.Fatal("tabs missing")
	}
	y.window.SetContent(v.content)
	tabs.SelectIndex(1)
	for _, screen := range []struct {
		name  string
		size  fyne.Size
		theme fyne.Theme
	}{
		{"dark", fyne.NewSize(1100, 720), theme.DarkTheme()},
		{"small", fyne.NewSize(900, 600), theme.DarkTheme()},
		{"light", fyne.NewSize(1100, 720), theme.LightTheme()},
		{"wide", fyne.NewSize(1440, 900), theme.DarkTheme()},
	} {
		fyne.CurrentApp().Settings().SetTheme(fluxTheme{Theme: screen.theme})
		y.window.Resize(screen.size)
		assertControlsVisible(t, y.window, y.playlist.download, y.playlist.prepare, y.playlist.acceptLower, y.playlist.previous, y.playlist.next)
		if y.playlist.list.Size().Height < 100 {
			t.Fatalf("%s: playlist has no usable scrolling area: %v", screen.name, y.playlist.list.Size())
		}
		if path := os.Getenv("FLUXGET_PLAYLIST_SCREENSHOT"); path != "" {
			f, err := os.Create(strings.TrimSuffix(path, ".png") + "-" + screen.name + ".png")
			if err != nil {
				t.Fatal(err)
			}
			err = png.Encode(f, y.window.Canvas().Capture())
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestYouTubePlaylistShowsOnlyRelevantOutputSettings(t *testing.T) {
	y, queue := youtubeTestView(t)
	loadPlaylistFixture(t, y, queue)
	p := y.playlist
	p.kind.SetSelected("Audio only")
	if p.qualityPanel.Visible() || p.bitratePanel.Visible() {
		t.Fatal("video quality or MP3 bitrate shown for M4A")
	}
	p.format.SetSelected("MP3")
	if !p.bitratePanel.Visible() || p.bitrate.Disabled() {
		t.Fatal("MP3 bitrate is unavailable")
	}
	p.kind.SetSelected("Video")
	if !p.qualityPanel.Visible() || p.bitratePanel.Visible() || p.quality.Disabled() {
		t.Fatal("video preferences did not restore")
	}
	p.acceptLower.Show()
	p.acceptLower.SetChecked(true)
	p.estimate.SetText("Old format review")
	y.link.SetText("https://youtu.be/AAAAAAAAAAA")
	if !y.empty.Visible() || p.content.Visible() || y.options.Visible() || p.acceptLower.Visible() || p.acceptLower.Checked || p.estimate.Text != "Select videos, then load formats." {
		t.Fatal("changing source left stale results visible")
	}
}

func TestYouTubePlaylistSortingAndInvalidation(t *testing.T) {
	y, queue := youtubeTestView(t)
	loadPlaylistFixture(t, y, queue)
	var calls []string
	y.analyzer = playlistAnalyzer(&calls)
	p := y.playlist
	p.choose(p.page.Entries[0], true)
	p.sortFiles.SetChecked(true)
	for _, tc := range []struct{ kind, format, folder string }{
		{"Video", "MP4", "Videos"}, {"Audio only", "M4A", "Music"},
	} {
		p.kind.SetSelected(tc.kind)
		p.format.SetSelected(tc.format)
		test.Tap(p.prepare)
		if !p.sortFiles.Disabled() {
			t.Fatal("sorting can change while formats are being prepared")
		}
		finishPlaylist(t, y, queue)
		if len(p.jobs) != 1 || filepath.Dir(p.jobs[0].Destination) != filepath.Join(p.folder.Text, tc.folder) || !strings.HasPrefix(filepath.Base(p.jobs[0].Destination), "000001 - ") {
			t.Fatalf("wrong sorted playlist admission: %+v", p.jobs)
		}
	}
	p.sortFiles.SetChecked(false)
	if len(p.jobs) != 0 || p.batch != "" || !p.download.Disabled() {
		t.Fatal("sorting change retained stale prepared destinations")
	}
	test.Tap(p.prepare)
	finishPlaylist(t, y, queue)
	if len(p.jobs) != 1 || filepath.Dir(p.jobs[0].Destination) != p.folder.Text {
		t.Fatal("playlist sorting opt-out ignored")
	}
}

func TestYouTubePlaylistMixExplainsSingleVideoAlternative(t *testing.T) {
	y, queue := youtubeTestView(t)
	calls := 0
	y.playlistLoader = func(context.Context, string, networkproxy.Config, string, int) (media.PlaylistPage, error) {
		calls++
		return media.PlaylistPage{}, nil
	}
	y.link.SetText("https://www.youtube.com/watch?v=AAAAAAAAAAA&list=RDBBBBBBBBBBB&index=2")
	y.scope.SetSelected("Choose from playlist")
	test.Tap(y.analyze)
	if y.busy || calls != 0 || !strings.Contains(y.status.Text, "YouTube Mix") || !strings.Contains(y.status.Text, "Choose This video") {
		t.Fatal("Mix rejection is unclear or reached network work", y.status.Text)
	}
	y.analyzer = func(_ context.Context, _ string, _ networkproxy.Config, source string) (media.Video, error) {
		if source != "https://www.youtube.com/watch?v=AAAAAAAAAAA" {
			t.Error("Mix parameters reached single-video analysis", source)
		}
		return youtubeFixture(), nil
	}
	y.scope.SetSelected("This video")
	test.Tap(y.analyze)
	finishYouTubeAnalysis(t, queue)
	if y.video.ID != "AAAAAAAAAAA" || !y.options.Visible() {
		t.Fatal("This video alternative did not load")
	}
}
