//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Parsania80/FluxGet/media"
)

func TestMediaQueueNativeMergeAndPublication(t *testing.T) {
	manifest := os.Getenv("FLUXGET_MEDIA_TOOLS")
	if manifest == "" {
		t.Skip("requires explicit pinned native media tools")
	}
	tools, err := media.LoadTools(manifest)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	for _, args := range [][]string{
		{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=10", "-t", "3", "-an", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", filepath.Join(root, "video.mp4")},
		{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", "3", "-vn", "-c:a", "aac", "-b:a", "128k", filepath.Join(root, "audio.m4a")},
	} {
		if output, err := exec.Command(tools.FFmpeg, args...).CombinedOutput(); err != nil {
			t.Fatalf("generate native fixture: %v %s", err, output)
		}
	}
	server := httptest.NewServer(http.FileServer(http.Dir(root)))
	defer server.Close()
	info := map[string]any{"id": "AAAAAAAAAAA", "title": "Native queue fixture", "duration": 3, "extractor": "youtube", "extractor_key": "Youtube", "webpage_url": "https://www.youtube.com/watch?v=AAAAAAAAAAA", "formats": []map[string]any{
		{"format_id": "137", "url": server.URL + "/video.mp4", "ext": "mp4", "protocol": "http", "vcodec": "avc1.640028", "acodec": "none", "height": 90},
		{"format_id": "140", "url": server.URL + "/audio.m4a", "ext": "m4a", "protocol": "http", "vcodec": "none", "acodec": "mp4a.40.2"},
	}}
	data, _ := json.Marshal(info)
	infoPath := filepath.Join(root, "info.json")
	if err := os.WriteFile(infoPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	// Only the source discovery is a fixture. Transfer, FFmpeg, inspection,
	// recovery, publication and the queue companion use the production path.
	shim := filepath.Join(root, "yt-dlp-fixture")
	script := `#!/usr/bin/env python3
import json,os,pathlib,sys
config=json.loads(pathlib.Path(__file__).with_name('shim.json').read_text())
args=sys.argv[1:]
if '--dump-single-json' in args:
 print(pathlib.Path(config['info']).read_text())
 sys.exit(0)
if '--output' in args:
 args=args[:args.index('--')] if '--' in args else args
 args+=['--load-info-json',config['info']]
os.execv(config['real'],[config['real']]+args)
`
	if err := os.WriteFile(shim, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	shimConfig, _ := json.Marshal(map[string]string{"real": tools.YTDLP, "info": infoPath})
	if err := os.WriteFile(filepath.Join(root, "shim.json"), shimConfig, 0600); err != nil {
		t.Fatal(err)
	}
	tools.YTDLP = shim
	toolManifest, _ := json.Marshal(map[string]any{"tools": tools})
	manifest = filepath.Join(root, "tools.json")
	if err := os.WriteFile(manifest, toolManifest, 0600); err != nil {
		t.Fatal(err)
	}
	var video media.Video
	if err := json.Unmarshal(data, &video); err != nil {
		t.Fatal(err)
	}
	for _, selection := range []media.Selection{{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, {AudioFormat: "140", Container: "mp3", MP3Kbps: 192}} {
		spec, err := media.NewJob(video, selection, manifest)
		if err != nil {
			t.Fatal(err)
		}
		j := queueJob{ID: 1, URL: "https://www.youtube.com/watch?v=AAAAAAAAAAA", Destination: filepath.Join(root, "output."+selection.Container), Media: &spec, State: "queued"}
		path, _ := queuePath()
		if err := saveQueue(path, queueData{Version: 5, NextID: 2, Jobs: []queueJob{j}}); err != nil {
			t.Fatal(err)
		}
		input, commands := io.Pipe()
		out := &queueOutput{}
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		done := make(chan error, 1)
		go func() { done <- runQueue(ctx, input, out, 3, 4) }()
		fmt.Fprintln(commands, "start")
		deadline := time.Now().Add(25 * time.Second)
		for !out.contains("Job 1: completed") && !out.contains("Job 1 failed:") && time.Now().Before(deadline) {
			time.Sleep(15 * time.Millisecond)
		}
		fmt.Fprintln(commands, "quit")
		commands.Close()
		if err := <-done; err != nil {
			cancel()
			t.Fatal(err)
		}
		cancel()
		if !out.contains("Job 1: completed") {
			out.mu.Lock()
			text := out.text
			out.mu.Unlock()
			t.Fatal("native queue did not complete", text)
		}
		q, err := loadQueue(path)
		if err != nil || q.Jobs[0].State != "completed" {
			t.Fatal("native completion not persisted", err)
		}
		output, err := exec.Command(tools.FFprobe, "-v", "error", "-show_entries", "stream=codec_type,codec_name:format=duration", "-of", "json", j.Destination).CombinedOutput()
		if err != nil || !strings.Contains(string(output), "audio") || selection.VideoFormat != "" && !strings.Contains(string(output), "video") {
			t.Fatal("native published output is invalid", err, string(output))
		}
		record, _ := mediaRecordPath(j.Destination)
		if _, err := os.Stat(record); !os.IsNotExist(err) {
			t.Fatal("completed media left active recovery record")
		}
		t.Logf("native %s queue output inspected: %s", selection.Container, strings.TrimSpace(string(output)))
	}
}
