package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Parsania80/FluxGet/media"
)

func playlistQueueJobs(t *testing.T) (string, []queueJob) {
	t.Helper()
	first := mediaQueueJob(t)
	first.Media.Playlist = &media.PlaylistPosition{Batch: strings.Repeat("a", 32), ID: "PLexample", Title: "Playlist", Index: 1}
	second := first
	spec := *first.Media
	position := *first.Media.Playlist
	second.Media = &spec
	second.Media.Playlist = &position
	second.Media.Playlist.Index = 2
	second.Media.Selection = media.Selection{AudioFormat: "140", Container: "mp3", MP3Kbps: 192}
	second.Media.Video.Formats = second.Media.Video.Formats[1:]
	second.Destination = filepath.Join(filepath.Dir(first.Destination), "audio.mp3")
	return first.Media.Playlist.Batch, []queueJob{first, second}
}

func TestMediaQueuePlaylistReceiptsSurviveReloadRemovalAndPartialSuccess(t *testing.T) {
	batch, jobs := playlistQueueJobs(t)
	q := queueData{Version: 5, NextID: 1}
	// A destination conflict skips only its own item.
	if err := os.WriteFile(jobs[1].Destination, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	results, err := addPlaylist(&q, batch, jobs)
	if err != nil || len(q.Jobs) != 1 || results[0].JobID != 1 || results[1].Error == "" {
		t.Fatal(results, err)
	}
	path := filepath.Join(t.TempDir(), "queue.json")
	if err := saveQueue(path, q); err != nil {
		t.Fatal(err)
	}
	restored, err := loadQueue(path)
	if err != nil || restored.Version != 6 || restored.Jobs[0].Media.Playlist.Index != 1 {
		t.Fatal(restored, err)
	}
	restored.Jobs[0].State = "completed"
	summary := playlistSummaries(restored)
	if len(summary) != 1 || summary[0].Completed != 1 || summary[0].Skipped != 1 {
		t.Fatal(summary)
	}
	restored.Jobs = nil
	if err := saveQueue(path, restored); err != nil {
		t.Fatal(err)
	}
	restored, err = loadQueue(path)
	if err != nil {
		t.Fatal(err)
	}
	again, err := addPlaylist(&restored, batch, jobs)
	if err != nil || len(restored.Jobs) != 0 || again[0].JobID != 1 {
		t.Fatal("removed job recreated by replay", again, err)
	}
	jobs[0].Destination = filepath.Join(t.TempDir(), "changed.mp4")
	if _, err := addPlaylist(&restored, batch, jobs); err == nil {
		t.Fatal("changed request reused replay token")
	}
}

func TestMediaQueuePlaylistOrderBoundsAndDifferentProfiles(t *testing.T) {
	batch, jobs := playlistQueueJobs(t)
	q := queueData{Version: 5, NextID: 1}
	if _, err := addPlaylist(&q, batch, []queueJob{jobs[1], jobs[0]}); err == nil || len(q.Jobs) != 0 {
		t.Fatal("out-of-order batch changed queue")
	}
	if _, err := addPlaylist(&q, batch, make([]queueJob, 1001)); err == nil {
		t.Fatal("unbounded batch accepted")
	}
	results, err := addPlaylist(&q, batch, jobs)
	if err != nil || len(q.Jobs) != 2 || results[1].JobID != 2 {
		t.Fatal("different output profiles blocked", results, err)
	}
	q.Jobs[0].State = "completed"
	q.Jobs[1].State = "failed"
	summary := playlistSummaries(q)
	if summary[0].Completed != 1 || summary[0].Failed != 1 {
		t.Fatal(summary)
	}
	q.Version = 5
	if validatePlaylistReceipts(q) == nil {
		t.Fatal("old schema accepted playlist metadata")
	}
}

func TestMediaQueuePlaylistProtocolBackupAndReplay(t *testing.T) {
	batch, jobs := playlistQueueJobs(t)
	path, _ := queuePath()
	if err := saveQueue(path, queueData{Version: 5, NextID: 1}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	request, _ := json.Marshal(desktopRequest{ID: 1, Operation: "playlist-add", Batch: batch, Jobs: jobs})
	var out bytes.Buffer
	input := "desktop-json " + string(request) + "\ndesktop-json " + string(request) + "\nquit\n"
	if err := runQueue(context.Background(), strings.NewReader(input), &out, 1, 1); err != nil {
		t.Fatal(err)
	}
	q, err := loadQueue(path)
	if err != nil || len(q.Jobs) != 2 || q.Version != 6 {
		t.Fatal("batch was not saved once", err, out.String())
	}
	backup, _ := os.ReadFile(path + ".before-playlist")
	if !bytes.Equal(backup, before) {
		t.Fatal("playlist migration backup changed")
	}
	if strings.Count(out.String(), `"playlist_results":[{"index":1,"job_id":1},{"index":2,"job_id":2}]`) != 2 {
		t.Fatal("replay did not acknowledge original jobs", out.String())
	}
}
