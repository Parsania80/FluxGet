package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Parsania80/FluxGet/media"
)

const mediaQueueFixture = `{"id":"AAAAAAAAAAA","title":"Queue media fixture","duration":3,"formats":[{"format_id":"137","ext":"mp4","vcodec":"avc1.640028","acodec":"none","height":1080,"protocol":"https","filesize":65536},{"format_id":"140","ext":"m4a","vcodec":"none","acodec":"mp4a.40.2","protocol":"https","filesize":65536}]}`

// The test binary doubles as explicit tools; these branches never fetch a URL.
func queueMediaTestTool() bool {
	if len(os.Args) < 2 {
		return false
	}
	switch os.Args[1] {
	case "--version", "-version":
		fmt.Println("media fixture 1.0")
		return true
	case "-v":
		if strings.HasSuffix(os.Args[len(os.Args)-1], ".mp3") {
			fmt.Print(`{"format":{"duration":"3","format_name":"mp3"},"streams":[{"codec_type":"audio","codec_name":"mp3"}]}`)
		} else {
			fmt.Print(`{"format":{"duration":"3","format_name":"mov,mp4,m4a,3gp,3g2,mj2"},"streams":[{"codec_type":"video","codec_name":"h264","height":1080},{"codec_type":"audio","codec_name":"aac"}]}`)
		}
		return true
	case "--ignore-config":
		args := os.Args[2:]
		for _, arg := range args {
			if arg == "--dump-single-json" {
				fmt.Println(mediaQueueFixture)
				return true
			}
		}
		output, selector, extension, rate := "", "", "mp4", int64(0)
		for i, arg := range args {
			if i+1 >= len(args) {
				break
			}
			switch arg {
			case "--output":
				output = args[i+1]
			case "--format":
				selector = args[i+1]
			case "--audio-format":
				extension = args[i+1]
			case "--limit-rate":
				rate, _ = strconv.ParseInt(args[i+1], 10, 64)
			}
		}
		output = strings.ReplaceAll(strings.ReplaceAll(output, "%(ext)s", extension), "%%", "%")
		for _, id := range strings.Split(selector, "+") {
			part := filepath.Join(filepath.Dir(output), "asset.f"+id+".part")
			file, err := os.OpenFile(part, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
			if err != nil {
				os.Exit(3)
			}
			info, _ := file.Stat()
			done := info.Size()
			start := time.Now()
			baseline := done
			for done < 65536 {
				if _, err := file.Write(bytes.Repeat([]byte("m"), 4096)); err != nil {
					os.Exit(4)
				}
				done += 4096
				fmt.Printf("FG_PROGRESS:{\"format_id\":%q,\"downloaded_bytes\":%d,\"total_bytes\":65536,\"speed\":16384,\"eta\":0}\n", id, done)
				if rate > 0 {
					delay := time.Duration(float64(done-baseline)/float64(rate)*float64(time.Second)) - time.Since(start)
					if delay > 0 {
						time.Sleep(delay)
					}
				} else {
					time.Sleep(15 * time.Millisecond)
				}
			}
			file.Close()
		}
		fmt.Println(`FG_PHASE:processing`)
		if err := os.WriteFile(output, []byte("validated media output"), 0600); err != nil {
			os.Exit(5)
		}
		encoded, _ := json.Marshal(output)
		fmt.Printf("FG_RESULT:%s\n", encoded)
		return true
	}
	return false
}

func mediaQueueJob(t *testing.T) queueJob {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(t.TempDir(), "tools.json")
	data, _ := json.Marshal(map[string]any{"tools": media.Tools{YTDLP: executable, Deno: executable, FFmpeg: executable, FFprobe: executable}})
	if err := os.WriteFile(manifest, data, 0600); err != nil {
		t.Fatal(err)
	}
	var video media.Video
	if err := json.Unmarshal([]byte(mediaQueueFixture), &video); err != nil {
		t.Fatal(err)
	}
	spec, err := media.NewJob(video, media.Selection{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	return queueJob{ID: 1, URL: "https://www.youtube.com/watch?v=AAAAAAAAAAA", Destination: filepath.Join(t.TempDir(), "video.mp4"), Media: &spec, State: "paused"}
}

func TestMediaQueueSchemaAndDuplicateBoundary(t *testing.T) {
	j := mediaQueueJob(t)
	path, _ := queuePath()
	q := queueData{Version: 4, NextID: 1}
	if err := saveQueue(path, q); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := backupQueueNamed(path, ".before-media"); err != nil {
		t.Fatal(err)
	}
	if _, err := desktopAdd(&q, j, false); err != nil {
		t.Fatal(err)
	}
	if err := saveQueue(path, q); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadQueue(path)
	if err != nil || loaded.Version != 5 || loaded.Jobs[0].Media == nil {
		t.Fatal("media selection did not survive reload", err)
	}
	backup, _ := os.ReadFile(path + ".before-media")
	if !bytes.Equal(before, backup) {
		t.Fatal("migration backup changed")
	}
	j.Destination = filepath.Join(t.TempDir(), "copy.mp4")
	if _, err := desktopAdd(&q, j, false); err == nil {
		t.Fatal("duplicate video/format admitted")
	}
	q.Version = 4
	data, _ := json.Marshal(q)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadQueue(path); err == nil {
		t.Fatal("old schema accepted media job")
	}
}

func TestMediaQueueCompanionPauseResumeAndPublish(t *testing.T) {
	j := mediaQueueJob(t)
	updates := make(chan mediaUpdate, 128)
	writer := &queueProgressWriter{updates: make(chan queueProgress, 256)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runMediaCompanion(ctx, j, 16384, mediaTestReporter{updates}) }()
	deadline := time.NewTimer(20 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case update := <-updates:
			if update.Downloaded >= 16384 {
				cancel()
				goto paused
			}
		case err := <-done:
			t.Fatalf("ended before pause: %v", err)
		case <-deadline.C:
			t.Fatal("media never produced progress")
		}
	}
paused:
	if err := <-done; !onlyCancellation(err) {
		t.Fatal("pause did not save cleanly", err)
	}
	path, _ := mediaRecordPath(j.Destination)
	state, err := loadMediaRecord(path, j)
	if err != nil || !state.Clean || len(state.Files) == 0 {
		t.Fatal("pause lost verified scratch", err)
	}
	if _, err := os.Lstat(j.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("pause published output")
	}
	before := state.Counters["137"].Downloaded
	if before < 16384 {
		t.Fatal("partial counter not saved")
	}
	if err := runMediaCompanion(t.Context(), j, 0, writer); err != nil {
		t.Fatal("resume failed", err)
	}
	data, err := os.ReadFile(j.Destination)
	if err != nil || string(data) != "validated media output" {
		t.Fatal("final output missing", err)
	}
	state, err = loadMediaRecord(path, j)
	if err != nil || state.Ready != "asset.mp4" {
		t.Fatal("publication proof missing", err)
	}
	// Repeated publication reconciles the same inode and never replaces files.
	if err := publishMedia(t.Context(), state, j); err != nil {
		t.Fatal(err)
	}
	if err := cleanupMediaRecord(j, false); err != nil {
		t.Fatal("owned cache cleanup failed", err)
	}
	if _, err := os.Lstat(state.Workspace); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("completed cache was not removed")
	}
	if data, _ := os.ReadFile(j.Destination); string(data) != "validated media output" {
		t.Fatal("cleanup deleted final output")
	}
}

type mediaTestReporter struct{ updates chan mediaUpdate }

func (r mediaTestReporter) Write(p []byte) (int, error) { return len(p), nil }
func (r mediaTestReporter) ReportMedia(update mediaUpdate) {
	select {
	case r.updates <- update:
	default:
	}
}

func TestMediaQueueRejectsEditedScratchAndPreservesDestination(t *testing.T) {
	j := mediaQueueJob(t)
	state, err := createMediaRecord(j, map[string]string{"yt_dlp": "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(state.Workspace, "asset.mp4")
	if err := os.WriteFile(output, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := snapshotMediaFiles(t.Context(), &state, j); err != nil {
		t.Fatal(err)
	}
	state.Ready = "asset.mp4"
	if err := os.WriteFile(output, []byte("modified"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishMedia(t.Context(), state, j); err == nil {
		t.Fatal("modified media was published")
	}
	if err := os.WriteFile(output, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j.Destination, []byte("keep user file"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := publishMedia(t.Context(), state, j); err == nil {
		t.Fatal("overwrote a conflicting destination")
	}
	data, _ := os.ReadFile(j.Destination)
	if string(data) != "keep user file" {
		t.Fatal("user file changed")
	}
}

func TestMediaQueueLimitsAndInterruptedRestart(t *testing.T) {
	j := mediaQueueJob(t)
	config := filepath.Join(t.TempDir(), "speed.json")
	data, _ := json.Marshal(liveSpeedConfig{Enabled: true, Default: 100, Overall: 80, Overrides: map[string]int64{"queue:1": 50}})
	if err := os.WriteFile(config, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), liveSpeedKey{}, config)
	limit, overall, err := mediaLimits(ctx, 1)
	if err != nil || limit != 50*1024 || overall != 80*1024 {
		t.Fatal("media ceilings not combined", limit, overall, err)
	}
	if err := os.WriteFile(config, []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := mediaLimits(ctx, 1); err == nil {
		t.Fatal("invalid settings silently became unlimited")
	}
	state, err := createMediaRecord(j, nil)
	if err != nil {
		t.Fatal(err)
	}
	state.Clean = false
	path, _ := mediaRecordPath(j.Destination)
	if err := saveMediaRecord(path, state); err != nil {
		t.Fatal(err)
	}
	if err := runMediaJob(t.Context(), j, 0, func(mediaUpdate) {}); err == nil || !strings.Contains(err.Error(), "Restart") {
		t.Fatal("unclean scratch reused", err)
	}
	if err := cleanupMediaRecord(j, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(state.Workspace); err != nil {
		t.Fatal("explicit restart deleted unvalidated scratch", err)
	}
	records, _ := filepath.Glob(path + ".kept-*")
	if len(records) != 1 {
		t.Fatal("retained scratch lost its recovery record")
	}
}

var _ io.Writer = mediaTestReporter{}

func TestMediaQueueRefusesSymlinkAndForeignHardlink(t *testing.T) {
	for _, kind := range []string{"symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			j := mediaQueueJob(t)
			state, err := createMediaRecord(j, nil)
			if err != nil {
				t.Fatal(err)
			}
			asset := filepath.Join(state.Workspace, "asset.mp4")
			if err := os.WriteFile(asset, []byte("owned"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := snapshotMediaFiles(t.Context(), &state, j); err != nil {
				t.Fatal(err)
			}
			state.Ready = "asset.mp4"
			path, _ := mediaRecordPath(j.Destination)
			if err := saveMediaRecord(path, state); err != nil {
				t.Fatal(err)
			}
			outside := filepath.Join(t.TempDir(), "unrelated")
			if err := os.WriteFile(outside, []byte("keep unrelated"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(asset); err != nil {
				t.Fatal(err)
			}
			if kind == "symlink" {
				if err := os.Symlink(outside, asset); err != nil {
					t.Skip("symlink creation unavailable")
				}
			} else {
				if err := os.Link(outside, asset); err != nil {
					t.Fatal(err)
				}
			}
			if err := publishMedia(t.Context(), state, j); err == nil {
				t.Fatal("foreign asset published")
			}
			if err := cleanupMediaRecord(j, false); err == nil {
				t.Fatal("foreign asset deleted")
			}
			data, _ := os.ReadFile(outside)
			if string(data) != "keep unrelated" {
				t.Fatal("unrelated data changed")
			}
		})
	}
}
