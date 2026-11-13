//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Parsania80/FluxGet/media"
	"github.com/Parsania80/FluxGet/networkproxy"
)

func TestMediaQueuePublicPlaylist(t *testing.T) {
	url := os.Getenv("FLUXGET_MEDIA_PUBLIC_PLAYLIST")
	if url == "" {
		t.Skip("requires an explicit public playlist and pinned tools")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	manifest := os.Getenv("FLUXGET_MEDIA_TOOLS")
	tools, err := media.LoadTools(manifest)
	if err != nil {
		t.Fatal(err)
	}
	route := networkproxy.Config{Enabled: os.Getenv("FLUXGET_MEDIA_PROXY") != "", URL: os.Getenv("FLUXGET_MEDIA_PROXY")}
	client, err := media.NewWithProxy(tools, route)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	page, err := client.Playlist(ctx, url, 1)
	if err != nil || page.HasMore || len(page.Entries) < 2 || len(page.Entries) > 10 {
		t.Fatal("public acceptance requires a finite 2–10 item sample", err)
	}
	batch := strings.Repeat("c", 32)
	root := t.TempDir()
	var jobs []queueJob
	for _, entry := range page.Entries {
		if entry.Unavailable {
			t.Fatalf("sample entry %d is unavailable", entry.Index)
		}
		source := "https://www.youtube.com/watch?v=" + entry.ID
		video, err := client.Analyze(ctx, source)
		if err != nil || video.Duration <= 0 || video.Duration > 600 {
			t.Fatalf("sample entry %d metadata/size boundary: %v", entry.Index, err)
		}
		var selected media.Format
		for _, format := range video.Formats {
			if format.VideoCodec == "none" && (format.AudioCodec == "mp4a.40.2" || format.AudioCodec == "aac") && (selected.ID == "" || format.AudioBitrate > selected.AudioBitrate) {
				selected = format
			}
		}
		spec, err := media.NewJob(video, media.Selection{AudioFormat: selected.ID, Container: "m4a"}, manifest)
		if err != nil {
			t.Fatalf("sample entry %d has no compatible native audio: %v", entry.Index, err)
		}
		spec.Playlist = &media.PlaylistPosition{Batch: batch, ID: page.ID, Title: page.Title, Index: entry.Index}
		jobs = append(jobs, queueJob{URL: source, Destination: filepath.Join(root, fmt.Sprintf("%03d-%s.m4a", entry.Index, entry.ID)), Media: &spec, Proxy: &route})
	}
	path, err := queuePath()
	if err != nil {
		t.Fatal(err)
	}
	if err := saveQueue(path, queueData{Version: 5, NextID: 1}); err != nil {
		t.Fatal(err)
	}
	input, commands := io.Pipe()
	out := &queueOutput{}
	done := make(chan error, 1)
	go func() { done <- runQueue(ctx, input, out, 2, 4) }()
	t.Cleanup(func() {
		commands.Close()
		cancel()
		if err := <-done; err != nil && !onlyCancellation(err) {
			t.Error(err)
		}
	})
	request, _ := json.Marshal(desktopRequest{ID: 1, Operation: "playlist-add", Batch: batch, Jobs: jobs})
	fmt.Fprintln(commands, "desktop-json "+string(request))
	fmt.Fprintln(commands, "start")
	for _, job := range jobs {
		for !out.contains(fmt.Sprintf("Job %d: completed", job.Media.Playlist.Index)) {
			if out.contains("failed:") || ctx.Err() != nil {
				out.mu.Lock()
				text := out.text
				out.mu.Unlock()
				t.Fatal("public playlist did not complete", text)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	fmt.Fprintln(commands, "quit")
	queue, err := loadQueue(path)
	if err != nil || queue.Version != 6 || len(queue.Jobs) != len(jobs) {
		t.Fatal("public playlist completion was not durable", err)
	}
	summary := playlistSummaries(queue)
	if len(summary) != 1 || summary[0].Completed != len(jobs) || summary[0].Failed != 0 || summary[0].Pending != 0 || summary[0].Skipped != 0 {
		t.Fatal("public playlist summary differs", summary)
	}
	for _, job := range queue.Jobs {
		stat, err := os.Stat(job.Destination)
		if err != nil || stat.Size() == 0 || job.State != "completed" {
			t.Fatal("public playlist output missing", err)
		}
		t.Logf("public playlist entry %d: %s, %.3f seconds, %d bytes, completed and ffprobe-validated", job.Media.Playlist.Index, job.Media.Video.ID, job.Media.Video.Duration, stat.Size())
	}
}
