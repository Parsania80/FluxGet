//go:build linux

package main

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Parsania80/FluxGet/media"
)

func TestMediaQueueNativeLongDuration(t *testing.T) {
	tools, manifest, video, root := nativeMediaQueueFixture(t)
	const duration = 3600
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	defer logMediaResourceCgroup(t)
	for _, name := range []string{"video.mp4", "audio.m4a"} {
		source := filepath.Join(root, name)
		long := filepath.Join(root, "long-"+name)
		args := []string{"-nostdin", "-v", "error", "-stream_loop", "-1", "-i", source, "-t", strconv.Itoa(duration), "-c", "copy", long}
		if name == "audio.m4a" {
			// Looping short AAC repeats encoder priming and loses decoded duration.
			args = []string{"-nostdin", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:sample_rate=44100", "-t", strconv.Itoa(duration), "-vn", "-c:a", "aac", "-b:a", "128k", "-threads", "1", long}
		}
		if output, err := exec.CommandContext(ctx, tools.FFmpeg, args...).CombinedOutput(); err != nil {
			t.Fatal("generate long fixture", err, string(output))
		}
		if err := os.Rename(long, source); err != nil {
			t.Fatal(err)
		}
	}
	infoPath := filepath.Join(root, "info.json")
	data, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatal(err)
	}
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatal(err)
	}
	info["duration"] = duration
	data, _ = json.Marshal(info)
	if err := os.WriteFile(infoPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	video.Duration = duration
	for _, selection := range []media.Selection{{VideoFormat: "137", AudioFormat: "140", Container: "mp4"}, {AudioFormat: "140", Container: "mp3", MP3Kbps: 128}} {
		spec, err := media.NewJob(video, selection, manifest)
		if err != nil {
			t.Fatal(err)
		}
		j := queueJob{ID: 1, URL: "https://www.youtube.com/watch?v=AAAAAAAAAAA", Destination: filepath.Join(root, "hour."+selection.Container), Media: &spec, State: "paused"}
		started := time.Now()
		if err := runMediaCompanion(ctx, j, 0, io.Discard); err != nil {
			t.Fatal("long media did not complete", err)
		}
		output, err := exec.CommandContext(ctx, tools.FFprobe, "-v", "error", "-show_entries", "format=duration", "-of", "json", j.Destination).Output()
		var inspected media.Inspection
		if err != nil || json.Unmarshal(output, &inspected) != nil {
			t.Fatal("inspect long output", err)
		}
		seconds, err := strconv.ParseFloat(inspected.Format.Duration, 64)
		if err != nil || math.Abs(seconds-duration) > 1 {
			t.Fatal("long output duration differs", inspected.Format.Duration, err)
		}
		if err := cleanupMediaRecord(j, false); err != nil {
			t.Fatal(err)
		}
		stat, err := os.Stat(j.Destination)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("native long %s: %.3f seconds, %d output bytes, processing/transfer %v", selection.Container, seconds, stat.Size(), time.Since(started))
	}
}

func TestMediaQueueNativeResourcePressure(t *testing.T) {
	if os.Getenv("FLUXGET_MEDIA_PRESSURE") != "1" {
		t.Skip("opt-in isolated CPU/memory pressure check")
	}
	cgroup, err := os.ReadFile("/proc/self/cgroup")
	if err != nil || !strings.Contains(string(cgroup), "/fluxget-stage5-pressure.service\n") {
		t.Fatal("pressure check requires its dedicated transient service")
	}
	defer logMediaResourceCgroup(t)
	reader := newHardwareReader()
	first := reader.sample(time.Now())
	if first.CPUCapacity == nil || *first.CPUCapacity != .5 || first.GroupMemoryRatio == nil {
		t.Fatal("expected measured half-CPU and finite memory limits", first)
	}
	var blocks [][]byte
	defer func() { blocks = nil; debug.FreeOSMemory() }()
	for allocated := 0; allocated < 128<<20; allocated += 4 << 20 {
		block := make([]byte, 4<<20)
		for offset := 0; offset < len(block); offset += 4096 {
			block[offset] = 1
		}
		blocks = append(blocks, block)
		first = reader.sample(time.Now())
		if first.GroupMemoryRatio != nil && *first.GroupMemoryRatio >= .92 {
			break
		}
	}
	if first.GroupMemoryRatio == nil || *first.GroupMemoryRatio < .90 {
		t.Fatal("bounded allocation did not reach the pressure threshold", first.GroupMemoryRatio)
	}
	var stop atomic.Bool
	done := make(chan struct{})
	go func() {
		defer close(done)
		for !stop.Load() {
		}
	}()
	defer func() { stop.Store(true); <-done }()
	guard := newResourceGuard()
	started := time.Now()
	var peakCPU, peakRatio float64
	for time.Since(started) < 16*time.Second {
		time.Sleep(time.Second)
		now := time.Now()
		snapshot := reader.sample(now)
		guard.observe(now, snapshot, nil, 1)
		if snapshot.CPUPercent != nil {
			peakCPU = max(peakCPU, *snapshot.CPUPercent)
		}
		if snapshot.GroupMemoryRatio != nil {
			peakRatio = max(peakRatio, *snapshot.GroupMemoryRatio)
		}
		if guard.cpu.active && guard.memory.active {
			break
		}
	}
	runtime.KeepAlive(blocks)
	if !guard.cpu.active || !guard.memory.active || !guard.blocked {
		t.Fatalf("real pressure did not latch: cpu=%v memory=%v peakCPU=%.1f ratio=%.3f", guard.cpu.active, guard.memory.active, peakCPU, peakRatio)
	}
	stop.Store(true)
	<-done
	blocks = nil
	debug.FreeOSMemory()
	deadline := time.Now().Add(38 * time.Second)
	for guard.blocked && time.Now().Before(deadline) {
		time.Sleep(time.Second)
		now := time.Now()
		guard.observe(now, reader.sample(now), nil, 1)
	}
	if guard.blocked {
		t.Fatal("resource guard did not recover after releasing pressure", guard.reason)
	}
	t.Logf("real pressure latched and recovered in %v: CPU peak %.1f%% of half CPU, memory ratio peak %.3f", time.Since(started), peakCPU, peakRatio)
}

func logMediaResourceCgroup(t *testing.T) {
	t.Helper()
	data, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		t.Log("cgroup accounting unavailable", err)
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "0::") {
			continue
		}
		root := filepath.Join("/sys/fs/cgroup", strings.TrimPrefix(line, "0::"))
		for _, name := range []string{"cpu.max", "cpu.stat", "memory.high", "memory.max", "memory.peak", "memory.events", "io.stat"} {
			value, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Logf("cgroup %s unavailable: %v", name, err)
			} else {
				t.Logf("cgroup %s: %s", name, strings.TrimSpace(string(value)))
			}
		}
	}
}
