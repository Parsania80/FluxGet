//go:build linux

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Parsania80/FluxGet/media"
)

func TestYouTubePlaylistActualQueueReplayAfterReopen(t *testing.T) {
	setTestConfig(t, t.TempDir())
	binary := unifiedCLI(t)
	processContext, stopProcess := context.WithTimeout(t.Context(), 20*time.Second)
	defer stopProcess()
	process := exec.CommandContext(processContext, binary, "-queue")
	input, err := process.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := process.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		input.Close()
		if process.ProcessState == nil {
			process.Process.Kill()
			process.Wait()
		}
	})

	batch := strings.Repeat("b", 32)
	v := youtubeFixture()
	spec, err := media.NewJob(v, media.Selection{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, filepath.Join(t.TempDir(), "tools.json"))
	if err != nil {
		t.Fatal(err)
	}
	spec.Playlist = &media.PlaylistPosition{Batch: batch, ID: "PLexample", Title: "Fixture playlist", Index: 1}
	job := queueEntry{Media: &spec, URL: "https://www.youtube.com/watch?v=" + v.ID, Destination: filepath.Join(t.TempDir(), "video.mp4"), State: "paused"}
	request := desktopRequest{Operation: "playlist-add", Batch: batch, Jobs: []queueEntry{job}}

	request.ID = 1
	encoded, _ := json.Marshal(request)
	if _, err := fmt.Fprintln(input, "desktop-json "+string(encoded)); err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(output)
	scanner.Buffer(make([]byte, 4096), 2<<20)
	var first desktopResponse
	for scanner.Scan() {
		if strings.HasPrefix(scanner.Text(), "DESKTOP_JSON ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(scanner.Text(), "DESKTOP_JSON ")), &first); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	if len(first.PlaylistResults) != 1 || first.PlaylistResults[0].JobID != 1 || len(first.Playlists) != 1 {
		t.Fatal(first)
	}
	// Simulate death after commit while the caller still needs to reconcile.
	if err := process.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = process.Wait()
	input.Close()
	request.ID = 0
	reopened := unifiedQueue(t, binary, 1)
	response := unifiedRequest(t, reopened, request)
	if len(response.Jobs) != 1 || response.Jobs[0].State != "paused" || response.Jobs[0].Media.Playlist.Batch != batch || response.PlaylistResults[0].JobID != 1 {
		t.Fatal("reopen duplicated or started batch", response)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := reopened.request(ctx, desktopRequest{Operation: "action", Action: "remove", Job: response.Jobs[0]}); err != nil {
		t.Fatal(err)
	}
	response = unifiedRequest(t, reopened, request)
	if len(response.Jobs) != 0 || response.PlaylistResults[0].JobID != 1 || response.Playlists[0].Removed != 1 {
		t.Fatal("receipt did not survive removal", response)
	}
}
