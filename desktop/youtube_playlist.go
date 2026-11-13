package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/Parsania80/FluxGet/media"
	"github.com/Parsania80/FluxGet/networkproxy"
)

type playlistChoice struct {
	entry media.PlaylistEntry
	note  string
	lower bool
	job   *media.Job
}

type youtubePlaylist struct {
	owner                                         *youtubeView
	content                                       *fyne.Container
	list                                          *widget.List
	page                                          media.PlaylistPage
	selected                                      map[int]playlistChoice
	heading, count, estimate                      *widget.Label
	previous, next, all, clear, prepare, download *widget.Button
	kind, format, quality, bitrate                *widget.Select
	folder                                        *widget.Entry
	acceptLower                                   *widget.Check
	qualityPanel, bitratePanel                    *fyne.Container
	browse                                        *widget.Button
	jobs                                          []queueEntry
	batch                                         string
}

func loadYouTubePlaylist(ctx context.Context, manifest string, route networkproxy.Config, source string, first int) (media.PlaylistPage, error) {
	tools, err := media.LoadTools(manifest)
	if err != nil {
		return media.PlaylistPage{}, err
	}
	client, err := media.NewWithProxy(tools, route)
	if err != nil {
		return media.PlaylistPage{}, err
	}
	if _, err = client.Probe(ctx); err != nil {
		return media.PlaylistPage{}, err
	}
	return client.Playlist(ctx, source, first)
}

func newYouTubePlaylist(y *youtubeView) *youtubePlaylist {
	p := &youtubePlaylist{owner: y, selected: map[int]playlistChoice{}}
	p.heading = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	p.heading.Truncation = fyne.TextTruncateEllipsis
	p.count = widget.NewLabel("Select entries, then load their formats.")
	p.estimate = widget.NewLabel("Select videos, then load formats.")
	p.estimate.Wrapping = fyne.TextWrapWord
	p.previous = widget.NewButtonWithIcon("Previous", theme.NavigateBackIcon(), func() { p.load(p.page.First - media.PlaylistPageSize) })
	p.next = widget.NewButtonWithIcon("Next", theme.NavigateNextIcon(), func() { p.load(p.page.First + media.PlaylistPageSize) })
	p.all = widget.NewButton("Select this page", func() {
		if y.busy {
			return
		}
		for _, entry := range p.page.Entries {
			if !entry.Unavailable {
				p.choose(entry, true)
			}
		}
		p.list.Refresh()
	})
	p.clear = widget.NewButton("Clear", func() {
		if !y.busy {
			p.selected = map[int]playlistChoice{}
			p.invalidate()
			p.list.Refresh()
		}
	})
	p.list = widget.NewList(func() int { return len(p.page.Entries) }, func() fyne.CanvasObject {
		check := widget.NewCheck("", nil)

		note := widget.NewLabel("")
		note.Truncation = fyne.TextTruncateEllipsis
		title := widget.NewLabel("")
		title.Truncation = fyne.TextTruncateEllipsis
		return container.NewVBox(container.NewBorder(nil, nil, check, nil, title), note)
	}, func(i widget.ListItemID, object fyne.CanvasObject) {
		entry := p.page.Entries[i]
		row := object.(*fyne.Container)
		header := row.Objects[0].(*fyne.Container)
		check := header.Objects[1].(*widget.Check)
		title := header.Objects[0].(*widget.Label)
		note := row.Objects[1].(*widget.Label)
		choice, selected := p.selected[entry.Index]
		check.OnChanged = nil
		check.SetText(fmt.Sprintf("Select %d", entry.Index))
		title.SetText(entry.Title)
		check.SetChecked(selected)
		check.Enable()
		note.SetText("Select to include in this download.")
		if selected {
			note.SetText("Selected · Formats not loaded")
		}
		if selected && choice.note != "" {
			note.SetText(choice.note)
		}
		if entry.Unavailable {
			check.Disable()
			note.SetText("Unavailable, private, deleted or live — skipped.")
		}
		if y.busy {
			check.Disable()
		}
		check.OnChanged = func(value bool) { p.choose(entry, value) }
		row.Refresh()
	})
	p.kind = widget.NewSelect([]string{"Video", "Audio only"}, nil)
	p.kind.SetSelected("Video")
	p.format = widget.NewSelect([]string{"MP4", "WebM", "MKV"}, nil)
	p.format.SetSelected("MP4")
	p.quality = widget.NewSelect([]string{"360p", "480p", "720p", "1080p", "1440p", "2160p", "4320p"}, nil)
	p.quality.SetSelected("1080p")
	p.bitrate = widget.NewSelect([]string{"128 kbps", "192 kbps", "256 kbps", "320 kbps"}, nil)
	p.bitrate.SetSelected("192 kbps")
	p.bitrate.Disable()
	p.folder = widget.NewEntry()
	p.folder.SetText(y.folder.Text)
	p.folder.OnChanged = func(string) { p.invalidate() }
	p.kind.OnChanged = func(value string) {
		p.format.ClearSelected()
		if value == "Audio only" {
			p.format.SetOptions([]string{"M4A", "Opus", "MP3"})
			p.quality.Disable()
			p.qualityPanel.Hide()
		} else {
			p.format.SetOptions([]string{"MP4", "WebM", "MKV"})
			p.quality.Enable()
			p.qualityPanel.Show()
		}
		p.format.SetSelectedIndex(0)
		p.invalidate()
	}
	p.format.OnChanged = func(string) {
		p.bitrate.Disable()
		p.bitratePanel.Hide()
		if p.format.Selected == "MP3" {
			p.bitrate.Enable()
			p.bitratePanel.Show()
		}
		p.invalidate()
	}
	p.quality.OnChanged = func(string) { p.invalidate() }
	p.bitrate.OnChanged = func(string) { p.invalidate() }
	p.acceptLower = widget.NewCheck("Accept lower qualities", func(bool) { p.updateDownload() })
	p.acceptLower.Hide()
	p.prepare = widget.NewButtonWithIcon("Load selected formats", theme.ViewRefreshIcon(), p.resolve)
	p.download = widget.NewButtonWithIcon("Download", theme.DownloadIcon(), p.submit)
	p.download.Importance = widget.HighImportance
	p.download.Disable()
	p.qualityPanel = container.NewVBox(widget.NewLabel("Maximum quality"), p.quality)
	p.bitratePanel = container.NewVBox(widget.NewLabel("MP3 bitrate"), p.bitrate)
	p.bitratePanel.Hide()
	p.browse = widget.NewButtonWithIcon("Browse", theme.FolderOpenIcon(), func() {
		dialog.ShowFolderOpen(func(uri fyne.ListableURI, err error) {
			if y.closed {
				return
			}
			if err != nil {
				y.status.SetText("Could not open the destination folder.")
			} else if uri != nil {
				p.folder.SetText(uri.Path())
			}
		}, y.window)
	})
	fileNote := widget.NewLabel("Named by playlist order and title. Existing files are kept.")
	fileNote.Wrapping = fyne.TextWrapWord
	settings := container.NewVBox(
		container.NewGridWithColumns(2, container.NewVBox(widget.NewLabel("Save as"), p.kind), container.NewVBox(widget.NewLabel("Format"), p.format)),
		p.qualityPanel, p.bitratePanel, widget.NewLabel("Save to"),
		container.NewBorder(nil, nil, nil, p.browse, p.folder), fileNote)
	sidebar := widget.NewCard("Download settings", "", container.NewVScroll(settings))
	entries := widget.NewCard("", "", container.NewBorder(
		container.NewVBox(p.heading, container.NewBorder(nil, nil, nil, container.NewHBox(p.all, p.clear), p.count)),
		container.NewBorder(nil, nil, p.previous, p.next, nil), nil, nil, p.list))
	workspace := container.NewHSplit(entries, sidebar)
	workspace.Offset = 0.66
	actions := container.NewHBox(p.acceptLower, p.prepare, p.download)
	p.content = container.NewBorder(nil,
		container.NewVBox(widget.NewSeparator(), container.NewBorder(nil, nil, nil, actions, p.estimate)), nil, nil, workspace)
	p.content.Hide()
	return p
}

func (p *youtubePlaylist) reset() {
	p.page = media.PlaylistPage{}
	p.selected = map[int]playlistChoice{}
	p.invalidate()
	p.content.Hide()
	p.download.Disable()
}
func (p *youtubePlaylist) invalidate() {
	if len(p.jobs) > 0 {
		for index, choice := range p.selected {
			choice.note, choice.lower, choice.job = "", false, nil
			p.selected[index] = choice
		}
		p.list.Refresh()
	}
	p.jobs = nil
	p.batch = ""
	if p.acceptLower != nil {
		p.acceptLower.SetChecked(false)
		p.acceptLower.Hide()
	}
	p.count.SetText(fmt.Sprintf("%d selected / 1,000", len(p.selected)))
	p.estimate.SetText("Select videos, then load formats.")
	p.prepare.Enable()
	if len(p.selected) == 0 {
		p.prepare.Disable()
	}
	p.updateDownload()
}
func (p *youtubePlaylist) choose(entry media.PlaylistEntry, selected bool) {
	if p.owner.busy || p.owner.submitting || entry.Unavailable {
		return
	}
	if selected {
		if len(p.selected) >= media.PlaylistBatchLimit {
			if _, exists := p.selected[entry.Index]; !exists {
				p.owner.status.SetText("This batch is limited to 1,000 entries. Download it before choosing another batch.")
				return
			}
		}
		p.selected[entry.Index] = playlistChoice{entry: entry}
	} else {
		delete(p.selected, entry.Index)
	}
	p.invalidate()
}
func (p *youtubePlaylist) setBusy(busy bool) {
	for _, object := range []fyne.Disableable{p.previous, p.next, p.all, p.clear, p.prepare, p.kind, p.format, p.quality, p.bitrate, p.folder, p.browse, p.acceptLower} {
		if busy {
			object.Disable()
		} else {
			object.Enable()
		}
	}
	if !busy {
		if p.page.First <= 1 {
			p.previous.Disable()
		}
		if !p.page.HasMore {
			p.next.Disable()
		}
		if p.kind.Selected == "Audio only" {
			p.quality.Disable()
		}
		if p.format.Selected != "MP3" {
			p.bitrate.Disable()
		}
		if len(p.selected) == 0 {
			p.prepare.Disable()
		}
	}
	p.list.Refresh()
	p.updateDownload()
}
func (p *youtubePlaylist) finish() bool {
	y := p.owner
	y.setBusy(false)
	y.cancelRun = nil
	if y.closed {
		if y.onIdle != nil {
			done := y.onIdle
			y.onIdle = nil
			done()
		}
		return false
	}
	return true
}
func (p *youtubePlaylist) load(first int) {
	y := p.owner
	if y.busy || y.closed || y.submitting || first < 1 {
		return
	}
	source, err := media.NormalizePlaylistURL(strings.TrimSpace(y.link.Text))
	if err != nil {
		y.status.SetText("Enter a finite public YouTube playlist link. Channels, radio mixes and login-required lists are not supported.")
		if errors.Is(err, media.ErrMix) {
			y.status.SetText("This link is a YouTube Mix. Mix import is not supported in this preview. Choose This video to download the current item, or use a regular public playlist.")
		}
		return
	}
	route, err := proxyForDownload(y.settings.Proxy, y.useProxy.Checked)
	if err != nil {
		y.status.SetText("Set a valid proxy in Settings or turn off the proxy choice.")
		return
	}
	manifest := strings.TrimSpace(y.tools.Text)
	if !filepath.IsAbs(manifest) {
		y.status.SetText("Choose an absolute tools.json path in Media tools.")
		return
	}
	capturedRoute := networkproxy.Config{}
	if route != nil {
		capturedRoute = *route
	}
	if p.page.ID == "" {
		p.reset()
		y.video = media.Video{}
		y.options.Hide()
		y.route = capturedRoute
	} else {
		capturedRoute = y.route
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	y.cancelRun = cancel
	y.setBusy(true)
	y.status.SetText(fmt.Sprintf("Loading playlist entries %d–%d…", first, first+media.PlaylistPageSize-1))
	loader, dispatch := y.playlistLoader, y.dispatch
	go func() {
		page, err := loader(ctx, manifest, capturedRoute, source, first)
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		cancel()
		dispatch(func() {
			if !p.finish() {
				return
			}
			if err != nil {
				y.status.SetText(youtubeAnalysisError(err))
				return
			}
			if p.page.ID != "" && p.page.ID != page.ID {
				p.reset()
			}
			for _, entry := range page.Entries {
				if old, ok := p.selected[entry.Index]; ok && old.entry.ID != entry.ID {
					p.selected = map[int]playlistChoice{}
					p.invalidate()
					break
				}
			}
			p.page = page
			p.heading.SetText(fmt.Sprintf("%s · entries %d–%d", page.Title, first, first+len(page.Entries)-1))
			if len(page.Entries) == 0 {
				p.heading.SetText(page.Title + " · No entries on this page")
			}
			p.content.Show()
			y.empty.Hide()
			p.list.Refresh()
			p.setBusy(false)
			p.count.SetText(fmt.Sprintf("%d selected / 1,000", len(p.selected)))
			y.status.SetText("Choose videos and output settings, then Load selected formats. Nothing has been downloaded.")
		})
	}()
}

// Resolve each preference independently. No video-to-audio fallback or upscale.
func playlistSelection(v media.Video, format string, audioOnly bool, maximum, bitrate int) (media.Selection, bool, error) {
	videos, audios := youtubeFormats(v, format, audioOnly)
	s := media.Selection{Container: strings.ToLower(format)}
	lower := false
	if !audioOnly {
		found := false
		for _, f := range videos {
			if f.Height > 0 && f.Height <= maximum {
				s.VideoFormat = f.ID
				lower = f.Height < maximum
				found = true
				if f.AudioCodec != "none" {
					return s, lower, s.Validate(v)
				}
				break
			}
		}
		if !found {
			return s, false, media.ErrSelection
		}
	}
	if len(audios) == 0 {
		return s, false, media.ErrSelection
	}
	s.AudioFormat = audios[0].ID
	if s.Container == "mp3" {
		s.MP3Kbps = bitrate
	}
	return s, lower, s.Validate(v)
}
func (p *youtubePlaylist) resolve() {
	y := p.owner
	if y.busy || y.closed || y.submitting || len(p.selected) == 0 {
		return
	}
	p.invalidate()
	choices := make([]playlistChoice, 0, len(p.selected))
	for _, choice := range p.selected {
		choices = append(choices, playlistChoice{entry: choice.entry})
	}
	sort.Slice(choices, func(i, j int) bool { return choices[i].entry.Index < choices[j].entry.Index })
	token := make([]byte, 16)
	if _, err := rand.Read(token); err != nil {
		y.status.SetText("Cannot prepare a unique playlist batch. Try again.")
		return
	}
	batch := hex.EncodeToString(token)
	format, audioOnly := p.format.Selected, p.kind.Selected == "Audio only"
	maximum, _ := strconv.Atoi(strings.TrimSuffix(p.quality.Selected, "p"))
	bitrate, _ := strconv.Atoi(strings.TrimSuffix(p.bitrate.Selected, " kbps"))
	folder, manifest, route := strings.TrimSpace(p.folder.Text), strings.TrimSpace(y.tools.Text), y.route
	analyze, dispatch := y.analyzer, y.dispatch
	ctx, cancel := context.WithCancel(context.Background())
	y.cancelRun = cancel
	y.setBusy(true)
	y.status.SetText("Loading formats for selected entries…")
	go func() {
		jobs := make([]queueEntry, 0, len(choices))
		known, total := true, int64(0)
		for i := range choices {
			if ctx.Err() != nil {
				break
			}
			choice := &choices[i]
			current, totalCount := i+1, len(choices)
			dispatch(func() {
				if !y.closed {
					y.status.SetText(fmt.Sprintf("Loading selected formats: %d / %d…", current, totalCount))
				}
			})
			video, err := analyze(ctx, manifest, route, "https://www.youtube.com/watch?v="+choice.entry.ID)
			selection := media.Selection{Container: strings.ToLower(format)}
			var lower bool
			if err == nil && video.ID != choice.entry.ID {
				err = media.ErrMetadata
			}
			if err == nil {
				selection, lower, err = playlistSelection(video, format, audioOnly, maximum, bitrate)
			}
			position := &media.PlaylistPosition{Batch: batch, ID: p.page.ID, Title: p.page.Title, Index: choice.entry.Index}
			spec := media.Job{Playlist: position, Video: media.Video{ID: choice.entry.ID}, Selection: selection}
			destination := ""
			if err == nil {
				spec, err = media.NewJob(video, selection, manifest)
				spec.Playlist = position
			}
			if err == nil {
				profile := ""
				for _, f := range spec.Video.Formats {
					if f.ID == selection.VideoFormat {
						profile = fmt.Sprintf(" - %dp", f.Height)
					}
				}
				if selection.Container == "mp3" {
					profile = fmt.Sprintf(" - %dkbps", selection.MP3Kbps)
				}
				destination, err = youtubeDestination(folder, fmt.Sprintf("%06d - %s%s", choice.entry.Index, youtubeFilename(video.Title, video.ID), profile), selection.Container)
			}
			if err != nil {
				choice.note = "Skipped: unavailable source, incompatible preferred format or destination conflict."
				known = false
			} else {
				choice.job = &spec
				choice.lower = lower
				choice.note = "Ready · " + format
				for _, f := range spec.Video.Formats {
					if f.ID == selection.VideoFormat {
						choice.note += fmt.Sprintf(" · %dp", f.Height)
					}
					if f.ID == selection.AudioFormat || selection.AudioFormat == "" {
						if f.AudioCodec != "none" {
							choice.note += " · Audio"
							if f.Language != "" {
								choice.note += " (" + f.Language + ")"
							}
						}
					}
					size := f.Size
					if size == 0 {
						size = f.EstimatedSize
					}
					if size <= 0 || size > 1<<50 || total > 1<<60-size {
						known = false
					} else {
						total += size
					}
				}
				if lower {
					choice.note += " · Lower quality"
				}
			}
			job := queueEntry{URL: "https://www.youtube.com/watch?v=" + choice.entry.ID, Destination: destination, Media: &spec}
			if route.Enabled {
				r := route
				job.Proxy = &r
			}
			jobs = append(jobs, job)
		}
		err := ctx.Err()
		cancel()
		dispatch(func() {
			if !p.finish() {
				return
			}
			if err != nil {
				p.invalidate()
				y.status.SetText(youtubeAnalysisError(err))
				return
			}
			for _, choice := range choices {
				p.selected[choice.entry.Index] = choice
			}
			p.jobs, p.batch = jobs, batch
			for _, choice := range choices {
				if choice.lower {
					p.acceptLower.Show()
					break
				}
			}
			if known {
				p.estimate.SetText(fmt.Sprintf("Transfer: %s %s · Allow extra processing space.", dataNumber(float64(total), y.settings.DisplayUnit), displayUnit(y.settings.DisplayUnit)))
			} else {
				p.estimate.SetText("Size unknown · Allow extra processing space.")
			}
			p.list.Refresh()
			p.updateDownload()
			y.status.SetText("Formats ready. Review skipped videos and approve any lower qualities, then Download.")
		})
	}()
}
func (p *youtubePlaylist) updateDownload() {
	if p.download == nil {
		return
	}
	p.download.Disable()
	y := p.owner
	ready, needsApproval := 0, false
	for _, choice := range p.selected {
		if choice.lower && !p.acceptLower.Checked {
			needsApproval = true
		}
		if choice.job != nil {
			ready++
		}
	}
	p.download.SetText("Download")
	if ready > 0 && len(p.jobs) > 0 {
		p.download.SetText(fmt.Sprintf("Download %d", ready))
	}
	if y.busy || y.closed || y.submitting || y.submitPlaylist == nil || len(p.jobs) == 0 || needsApproval || ready == 0 || y.canDownload != nil && !y.canDownload() {
		return
	}
	p.download.Enable()
}
func (p *youtubePlaylist) submit() {
	y := p.owner
	p.updateDownload()
	if p.download.Disabled() {
		return
	}
	y.submitting = true
	y.setBusy(true)
	y.cancel.Disable()
	y.status.SetText("Adding selected entries to Downloads…")
	y.submitPlaylist(p.batch, p.jobs, func(results []media.PlaylistResult, err error) {
		y.submitting = false
		if !p.finish() {
			return
		}
		if err != nil {
			y.status.SetText("Could not confirm playlist admission. Retry Download to reconcile this same batch.")
			p.updateDownload()
			return
		}
		admitted, skipped := 0, 0
		for _, result := range results {
			if result.JobID > 0 {
				admitted++
			} else {
				skipped++
			}
		}
		p.selected = map[int]playlistChoice{}
		p.invalidate()
		p.list.Refresh()
		y.status.SetText(fmt.Sprintf("Playlist submitted: %d added, %d skipped. Use Downloads to pause, resume or retry individual failed entries.", admitted, skipped))
	})
}
