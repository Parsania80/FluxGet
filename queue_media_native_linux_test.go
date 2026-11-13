//go:build linux

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Parsania80/FluxGet/media"
)

func nativeMediaQueueFixture(t *testing.T) (media.Tools, string, media.Video, string) {
	t.Helper()
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
	files := http.FileServer(http.Dir(root))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("generated media request: %s Range=%q", r.URL.Path, r.Header.Get("Range"))
		files.ServeHTTP(w, r)
	}))
	t.Cleanup(server.Close)
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
	return tools, manifest, video, root
}

func TestMediaQueueNativeMergeAndPublication(t *testing.T) {
	tools, manifest, video, root := nativeMediaQueueFixture(t)
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

func TestMediaQueueNativeInterruptedProcessing(t *testing.T) {
	for _, selection := range []media.Selection{{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, {AudioFormat: "140", Container: "mp3", MP3Kbps: 192}} {
		t.Run(selection.Container, func(t *testing.T) {
			tools, manifest, video, root := nativeMediaQueueFixture(t)
			realFFmpeg := tools.FFmpeg
			toolDir := filepath.Join(root, "processing-tools")
			if err := os.Mkdir(toolDir, 0700); err != nil {
				t.Fatal(err)
			}
			tools.FFmpeg = filepath.Join(toolDir, "ffmpeg")
			if err := os.Symlink(tools.FFprobe, filepath.Join(toolDir, "ffprobe")); err != nil {
				t.Fatal(err)
			}
			tools.FFprobe = filepath.Join(toolDir, "ffprobe")
			// Slow only processing input so cancellation hits a real FFmpeg output.
			script := `#!/usr/bin/env python3
import json,os,pathlib,sys
real=json.loads(pathlib.Path(__file__).with_suffix('.json').read_text())
args=sys.argv[1:]
if '-i' in args:
 args.insert(args.index('-i'),'-re')
 pathlib.Path(__file__).with_suffix('.started').write_text(args[-1].removeprefix('file:'))
os.execv(real,[real]+args)
`
			if err := os.WriteFile(tools.FFmpeg, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			encoded, _ := json.Marshal(realFFmpeg)
			if err := os.WriteFile(tools.FFmpeg+".json", encoded, 0600); err != nil {
				t.Fatal(err)
			}
			encoded, _ = json.Marshal(map[string]any{"tools": tools})
			if err := os.WriteFile(manifest, encoded, 0600); err != nil {
				t.Fatal(err)
			}
			spec, err := media.NewJob(video, selection, manifest)
			if err != nil {
				t.Fatal(err)
			}
			j := queueJob{ID: 1, URL: "https://www.youtube.com/watch?v=AAAAAAAAAAA", Destination: filepath.Join(root, "interrupted."+selection.Container), Media: &spec, State: "paused"}
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { done <- runMediaCompanion(ctx, j, 0, io.Discard) }()
			var interrupted string
			deadline := time.Now().Add(20 * time.Second)
			for time.Now().Before(deadline) {
				data, _ := os.ReadFile(filepath.Join(toolDir, "ffmpeg.started"))
				if info, err := os.Stat(string(data)); err == nil && info.Size() > 0 {
					interrupted = string(data)
					break
				}
				select {
				case err := <-done:
					t.Fatal("ended before processing interruption", err)
				case <-time.After(10 * time.Millisecond):
				}
			}
			cancel()
			if err := <-done; !onlyCancellation(err) || interrupted == "" {
				t.Fatal("did not interrupt native processing", err, interrupted)
			}
			path, _ := mediaRecordPath(j.Destination)
			state, err := loadMediaRecord(path, j)
			if err != nil || !state.Clean || state.Ready != "" {
				t.Fatal("processing pause lost recovery state", err)
			}
			if err := verifyMediaFiles(t.Context(), state, j, false); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(j.Destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("interrupted processing published output", err)
			}
			ctx, stop := context.WithTimeout(t.Context(), 25*time.Second)
			defer stop()
			if err := runMediaCompanion(ctx, j, 0, io.Discard); err != nil {
				t.Fatal("native processing could not resume", err)
			}
			if err := cleanupMediaRecord(j, false); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestMediaQueueNativeDiskFullRecovery(t *testing.T) {
	if os.Getenv("FLUXGET_MEDIA_TOOLS") == "" {
		t.Skip("requires explicit pinned native media tools")
	}
	if os.Getenv("FLUXGET_MEDIA_DISK_CHILD") == "" {
		if err := exec.Command("unshare", "--user", "--map-root-user", "--mount", "true").Run(); err != nil {
			t.Skipf("private user/mount namespaces unavailable: %v", err)
		}
		dir := t.TempDir()
		script := `mount --make-rprivate / && mount -t tmpfs -o size=1m tmpfs "$1" && exec "$2" -test.run=^TestMediaQueueNativeDiskFullRecovery$ -test.v`
		cmd := exec.CommandContext(t.Context(), "unshare", "--user", "--map-root-user", "--mount", "sh", "-c", script, "fluxget-test", dir, os.Args[0])
		cmd.Env = append(os.Environ(), "FLUXGET_MEDIA_DISK_CHILD="+dir)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated media disk-full check: %v\n%s", err, output)
		} else {
			t.Log(string(output))
		}
		return
	}
	_, manifest, video, _ := nativeMediaQueueFixture(t)
	dir := os.Getenv("FLUXGET_MEDIA_DISK_CHILD")
	spec, err := media.NewJob(video, media.Selection{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	j := queueJob{ID: 1, URL: "https://www.youtube.com/watch?v=AAAAAAAAAAA", Destination: filepath.Join(dir, "disk-full.mp4"), Media: &spec, State: "paused"}
	filler := filepath.Join(dir, "filler")
	if err := os.WriteFile(filler, make([]byte, (1<<20)-(16<<10)), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	if err := runMediaCompanion(ctx, j, 0, io.Discard); err == nil || !strings.Contains(err.Error(), "disk space") {
		t.Fatal("media did not report full destination", err)
	}
	if _, err := os.Stat(j.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("full-disk output was published", err)
	}
	path, _ := mediaRecordPath(j.Destination)
	state, err := loadMediaRecord(path, j)
	if err != nil || !state.Clean || state.Ready != "" || len(state.Files) == 0 {
		t.Fatal("full disk lost recoverable scratch", err)
	}
	if err := verifyMediaFiles(ctx, state, j, false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filler); err != nil {
		t.Fatal(err)
	}
	if err := runMediaCompanion(ctx, j, 0, io.Discard); err != nil {
		t.Fatal("media did not recover after restoring space", err)
	}
	// A full metadata filesystem must preserve the last record atomically.
	metadata := filepath.Join(dir, "recovery.json")
	if err := saveMediaRecord(metadata, state); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(metadata)
	if err != nil {
		t.Fatal(err)
	}
	release := fillFilesystem(t, dir)
	defer release()
	state.Clean = false
	if err := saveMediaRecord(metadata, state); !errors.Is(err, syscall.ENOSPC) {
		t.Fatal("media record did not fail with ENOSPC", err)
	}
	after, err := os.ReadFile(metadata)
	if err != nil || string(before) != string(after) {
		t.Fatal("full disk damaged the previous media record", err)
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".fluxget-media-state-*"))
	if err != nil || len(temps) != 0 {
		t.Fatal("failed media save leaked temporary records", err, temps)
	}
}

func TestMediaQueueNativeRefreshesExpiredLinks(t *testing.T) {
	tools, manifest, video, root := nativeMediaQueueFixture(t)
	// Exceed yt-dlp's read buffer so pause leaves a genuinely unfinished stream.
	args := []string{"-nostdin", "-v", "error", "-y", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=60", "-t", "3", "-an", "-c:v", "libx264", "-preset", "ultrafast", "-crf", "0", "-threads", "1", "-pix_fmt", "yuv420p", filepath.Join(root, "video.mp4")}
	if output, err := exec.Command(tools.FFmpeg, args...).CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	spec, err := media.NewJob(video, media.Selection{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	j := queueJob{ID: 1, URL: "https://www.youtube.com/watch?v=AAAAAAAAAAA", Destination: filepath.Join(root, "refreshed.mp4"), Media: &spec, State: "paused"}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	updates := make(chan mediaUpdate, 256)
	done := make(chan error, 1)
	go func() { done <- runMediaCompanion(ctx, j, 16384, mediaTestReporter{updates}) }()
waiting:
	for {
		select {
		case update := <-updates:
			if update.Phase == "download" && update.Downloaded >= 8192 {
				break waiting
			}
		case err := <-done:
			t.Fatal("transfer ended before link refresh", err)
		case <-ctx.Done():
			t.Fatal("transfer never produced progress")
		}
	}
	cancel()
	if err := <-done; !onlyCancellation(err) {
		t.Fatal(err)
	}
	path, _ := mediaRecordPath(j.Destination)
	state, err := loadMediaRecord(path, j)
	if err != nil || !state.Clean || len(state.Files) == 0 {
		t.Fatal("pause did not preserve source data", err)
	}
	if err := verifyMediaFiles(t.Context(), state, j, false); err != nil {
		t.Fatal(err)
	}
	// Old media paths now return 404; only fresh analysis exposes working paths.
	source, err := os.Stat(filepath.Join(root, "video.mp4"))
	partial := state.Files["asset.f137.mp4.part"]
	if err != nil || partial.Size <= 0 || partial.Size >= source.Size() {
		t.Fatal("link-refresh fixture did not pause inside the video stream", err, partial.Size)
	}
	info := filepath.Join(root, "info.json")
	data, err := os.ReadFile(info)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"video.mp4", "audio.m4a"} {
		if err := os.Rename(filepath.Join(root, name), filepath.Join(root, "fresh-"+name)); err != nil {
			t.Fatal(err)
		}
		data = []byte(strings.ReplaceAll(string(data), "/"+name, "/fresh-"+name))
	}
	if err := os.WriteFile(info, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, stop := context.WithTimeout(t.Context(), 25*time.Second)
	defer stop()
	if err := runMediaCompanion(ctx, j, 0, io.Discard); err != nil {
		t.Fatal("fresh fixture links did not resume", err)
	}
	if err := cleanupMediaRecord(j, false); err != nil {
		t.Fatal(err)
	}
}

func TestMediaQueueNativeMixedSharedCeiling(t *testing.T) {
	_, manifest, video, root := nativeMediaQueueFixture(t)
	limits := filepath.Join(root, "limits.json")
	const capBytes = 16384
	saveMediaTestLimits(t, limits, liveSpeedConfig{Overall: capBytes / 1024})
	out := &queueOutput{}
	payload := bytes.Repeat([]byte("direct"), 16384)
	var overlap atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !out.contains("Job 1: completed") {
			overlap.Store(true)
		}
		w.Header().Set("ETag", `"fixture"`)
		http.ServeContent(w, r, "direct.bin", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	spec, err := media.NewJob(video, media.Selection{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	jobs := []queueJob{
		{ID: 1, URL: "https://www.youtube.com/watch?v=AAAAAAAAAAA", Destination: filepath.Join(root, "mixed.mp4"), Media: &spec, State: "queued"},
		{ID: 2, URL: server.URL + "/direct.bin", Destination: filepath.Join(root, "direct.bin"), State: "queued"},
	}
	path, _ := queuePath()
	if err := saveQueue(path, queueData{Version: 5, NextID: 3, Jobs: jobs}); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(t.Context(), liveSpeedKey{}, limits)
	ctx = context.WithValue(ctx, limiterContextKey{}, &speedLimiter{gate: make(chan struct{}, 1), live: &liveSpeedSource{path: limits, key: "overall"}})
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	input, commands := io.Pipe()
	defer commands.Close()
	done := make(chan error, 1)
	var before, after syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &before); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	go func() { done <- runQueue(ctx, input, out, 3, 4) }()
	fmt.Fprintln(commands, "start")
	mediaWaitFor(t, func() bool { return out.contains("Job 2: completed") || out.contains("failed:") })
	fmt.Fprintln(commands, "quit")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(started)
	if !out.contains("Job 1: completed") || !out.contains("Job 2: completed") || overlap.Load() {
		out.mu.Lock()
		text := out.text
		out.mu.Unlock()
		t.Fatal("mixed queue failed or overlapped under a shared cap", text)
	}
	data, err := os.ReadFile(jobs[1].Destination)
	if err != nil || !bytes.Equal(data, payload) {
		t.Fatal("mixed queue direct output differs", err)
	}
	transferred := int64(len(payload))
	for _, name := range []string{"video.mp4", "audio.m4a"} {
		info, err := os.Stat(filepath.Join(root, name))
		if err != nil {
			t.Fatal(err)
		}
		transferred += info.Size()
	}
	if float64(transferred)/elapsed.Seconds() > capBytes*1.25 {
		t.Fatalf("mixed average exceeded shared ceiling: %d bytes / %v", transferred, elapsed)
	}
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &after); err != nil {
		t.Fatal(err)
	}
	cpu := time.Duration(after.Utime.Nano() + after.Stime.Nano() - before.Utime.Nano() - before.Stime.Nano())
	t.Logf("mixed fixture: %d source bytes / %v = %.0f B/s; shared cap %d B/s; reaped child CPU %v; cumulative largest-child max RSS %d KiB (not total tree memory)", transferred, elapsed, float64(transferred)/elapsed.Seconds(), capBytes, cpu, after.Maxrss)
}

func TestMediaQueueNativeCompletedPartialNeedsRestart(t *testing.T) {
	tools, manifest, video, root := nativeMediaQueueFixture(t)
	spec, err := media.NewJob(video, media.Selection{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, manifest)
	if err != nil {
		t.Fatal(err)
	}
	j := queueJob{ID: 1, URL: "https://www.youtube.com/watch?v=AAAAAAAAAAA", Destination: filepath.Join(root, "completed-partial.mp4"), Media: &spec, State: "paused"}
	client, err := media.New(tools)
	if err != nil {
		t.Fatal(err)
	}
	versions, err := client.Probe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state, err := createMediaRecord(j, versions)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "video.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(state.Workspace, "asset.f137.mp4.part"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := snapshotMediaFiles(t.Context(), &state, j); err != nil {
		t.Fatal(err)
	}
	path, _ := mediaRecordPath(j.Destination)
	if err := saveMediaRecord(path, state); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	if err := runMediaCompanion(ctx, j, 0, io.Discard); err == nil || !strings.Contains(err.Error(), "rejected the saved download range") {
		t.Fatal("completed partial did not report a safe restart", err)
	}
	if err := verifyMediaFiles(ctx, state, j, false); err != nil {
		t.Fatal("range rejection changed saved data", err)
	}
	if _, err := os.Stat(j.Destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("range rejection published output", err)
	}
	if err := cleanupMediaRecord(j, true); err != nil {
		t.Fatal("explicit restart failed", err)
	}
	if err := runMediaCompanion(ctx, j, 0, io.Discard); err != nil {
		t.Fatal("explicit restart did not complete", err)
	}
}
