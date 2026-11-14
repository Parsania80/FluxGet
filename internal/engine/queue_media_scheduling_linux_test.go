//go:build linux

package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Parsania80/FluxGet/internal/platformfile"
)

func mediaWaitFor(t *testing.T, match func() bool) {
	t.Helper()
	deadline := time.Now().Add(25 * time.Second)
	for !match() {
		if time.Now().After(deadline) {
			t.Fatal("media state did not converge")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func saveMediaTestLimits(t *testing.T, path string, c liveSpeedConfig) {
	t.Helper()
	data, _ := json.Marshal(c)
	temp := path + ".new"
	if err := os.WriteFile(temp, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := platformfile.Replace(temp, path); err != nil {
		t.Fatal(err)
	}
}

func TestMediaQueueLiveCeilingRestartsSavedWorker(t *testing.T) {
	j := mediaQueueJob(t)
	path := filepath.Join(t.TempDir(), "limits.json")
	saveMediaTestLimits(t, path, liveSpeedConfig{Enabled: true, Default: 16})
	ctx := context.WithValue(t.Context(), liveSpeedKey{}, path)
	updates := make(chan mediaUpdate, 256)
	done := make(chan error, 1)
	go func() { done <- runQueueMedia(ctx, j, mediaTestReporter{updates}) }()
	changed, sawApply := false, false
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case update := <-updates:
			if !changed && update.Downloaded >= 16384 {
				saveMediaTestLimits(t, path, liveSpeedConfig{Enabled: true, Default: 256})
				changed = true
			}
			sawApply = sawApply || update.Phase == "limit"
		case err := <-done:
			if err != nil || !changed || !sawApply {
				t.Fatal("live cap did not safely restart media", err, changed, sawApply)
			}
			if _, err := os.Stat(j.Destination); err != nil {
				t.Fatal(err)
			}
			return
		case <-deadline.C:
			t.Fatal("media cap reconfiguration stalled")
		}
	}
}

func TestMediaQueueEnablingSharedLimitDrainsOtherDownload(t *testing.T) {
	j := mediaQueueJob(t)
	j.State = "queued"
	limits := filepath.Join(t.TempDir(), "limits.json")
	saveMediaTestLimits(t, limits, liveSpeedConfig{})
	payload := bytes.Repeat([]byte("d"), 128*1024)
	entered, canceled := make(chan struct{}), make(chan struct{})
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests.Add(1) == 1 {
			w.Header().Set("ETag", `"unchanged"`)
			w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
			w.Write(payload[:32768])
			w.(http.Flusher).Flush()
			close(entered)
			<-r.Context().Done()
			close(canceled)
			return
		}
		w.Header().Set("ETag", `"unchanged"`)
		http.ServeContent(w, r, "direct.bin", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	direct := queueJob{ID: 2, URL: server.URL + "/file", Destination: filepath.Join(t.TempDir(), "direct.bin"), State: "queued"}
	path, _ := queuePath()
	if err := saveQueue(path, queueData{Version: 5, NextID: 3, Jobs: []queueJob{j, direct}}); err != nil {
		t.Fatal(err)
	}
	input, commands := io.Pipe()
	out := &queueOutput{}
	ctx, cancel := context.WithTimeout(context.WithValue(t.Context(), liveSpeedKey{}, limits), 35*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runQueue(ctx, input, out, 2, 1) }()
	defer commands.Close()
	fmt.Fprintln(commands, "start")
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("ordinary download did not start alongside unlimited media")
	}
	saveMediaTestLimits(t, limits, liveSpeedConfig{Overall: 256})
	select {
	case <-canceled:
	case <-time.After(8 * time.Second):
		t.Fatal("ordinary download did not drain for the shared media ceiling")
	}
	mediaWaitFor(t, func() bool { return out.contains("Job 1: completed") || out.contains("Job 1 failed:") })
	if !out.contains("Job 1: completed") {
		out.mu.Lock()
		text := out.text
		out.mu.Unlock()
		t.Fatal("media failed", text)
	}
	mediaWaitFor(t, func() bool { return out.contains("Job 2: completed") || out.contains("Job 2 failed:") })
	fmt.Fprintln(commands, "quit")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	result, err := os.ReadFile(direct.Destination)
	if err != nil || !bytes.Equal(result, payload) {
		t.Fatal("drained ordinary download did not resume correctly", err)
	}
}

func TestMediaQueueProcessDeathStopsToolsAndSavesScratch(t *testing.T) {
	j := mediaQueueJob(t)
	j.State = "queued"
	path, _ := queuePath()
	if err := saveQueue(path, queueData{Version: 5, NextID: 2, Jobs: []queueJob{j}}); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "fluxget")
	if output, err := exec.Command("go", "build", "-o", binary, "../..").CombinedOutput(); err != nil {
		t.Fatal(err, string(output))
	}
	limits := filepath.Join(t.TempDir(), "limits.json")
	saveMediaTestLimits(t, limits, liveSpeedConfig{Enabled: true, Default: 16})
	cmd := exec.Command(binary, "-queue", "-live-speed-file", limits)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	out := &queueOutput{}
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { input.Close(); cmd.Process.Kill() }()
	fmt.Fprintln(input, "start")
	mediaWaitFor(t, func() bool { return out.contains("Downloaded 16384/") || out.contains("failed:") })
	if out.contains("failed:") {
		out.mu.Lock()
		text := out.text
		out.mu.Unlock()
		t.Fatal(text)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	cmd.Wait()
	input.Close()
	record, _ := mediaRecordPath(j.Destination)
	var state mediaRecovery
	mediaWaitFor(t, func() bool { var err error; state, err = loadMediaRecord(record, j); return err == nil && state.Clean })
	if err := verifyMediaFiles(t.Context(), state, j, false); err != nil {
		t.Fatal("queue death did not leave validated/reaped scratch", err)
	}
	time.Sleep(100 * time.Millisecond)
	if err := verifyMediaFiles(t.Context(), state, j, false); err != nil {
		t.Fatal("orphan tool continued writing", err)
	}
	saved, err := loadQueue(path)
	if err != nil || saved.Jobs[0].State != "running" {
		t.Fatal("expected interrupted persisted queue", err)
	}
	// Opening the queue restores the interrupted job as paused and never starts it.
	reader, commands := io.Pipe()
	output := &queueOutput{}
	done := make(chan error, 1)
	go func() { done <- runQueue(t.Context(), reader, output, 2, 1) }()
	mediaWaitFor(t, func() bool { return output.contains("Queue ready") })
	fmt.Fprintln(commands, "quit")
	commands.Close()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	saved, err = loadQueue(path)
	if err != nil || saved.Jobs[0].State != "paused" {
		t.Fatal("media did not restore paused", err)
	}
	if err := runMediaCompanion(t.Context(), j, 0, io.Discard); err != nil {
		t.Fatal("saved media could not continue", err)
	}
}
