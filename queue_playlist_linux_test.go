//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestMediaQueuePlaylistPartialCompletionAndRetry(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	batch, jobs := playlistQueueJobs(t)
	jobs[0].State = ""
	jobs[1].State = ""
	// The second source changed; it must fail without restarting the first.
	jobs[1].Media.Video.Duration = 30
	path, _ := queuePath()
	if err := saveQueue(path, queueData{Version: 5, NextID: 1}); err != nil {
		t.Fatal(err)
	}
	input, commands := io.Pipe()
	out := &queueOutput{}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runQueue(ctx, input, out, 3, 4) }()
	t.Cleanup(func() {
		if t.Failed() {
			out.mu.Lock()
			t.Log(out.text)
			out.mu.Unlock()
		}
		commands.Close()
		cancel()
		<-done
	})
	data, _ := json.Marshal(desktopRequest{ID: 1, Operation: "playlist-add", Batch: batch, Jobs: jobs})
	fmt.Fprintln(commands, "desktop-json "+string(data))
	fmt.Fprintln(commands, "start")
	// Each fake tool is a race-instrumented subprocess with an exit delay.
	waitFor := func(ready func() bool) {
		t.Helper()
		deadline := time.NewTimer(20 * time.Second)
		defer deadline.Stop()
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for !ready() {
			select {
			case <-tick.C:
			case <-deadline.C:
				t.Fatal("playlist fixture timed out")
			}
		}
	}
	waitFor(func() bool { return out.contains("Job 1: completed") && out.contains("Job 2 failed:") })
	first, err := os.ReadFile(jobs[0].Destination)
	if err != nil || len(first) == 0 {
		t.Fatal("successful item missing", err)
	}
	fmt.Fprintln(commands, "retry 2")
	waitFor(func() bool {
		out.mu.Lock()
		defer out.mu.Unlock()
		count := 0
		for _, line := range strings.Split(out.text, "\n") {
			if len(line) >= 13 && line[:13] == "Job 2 failed:" {
				count++
			}
		}
		return count >= 2
	})
	q, err := loadQueue(path)
	if err != nil || len(q.Jobs) != 2 || q.Jobs[0].State != "completed" || q.Jobs[1].State != "failed" {
		t.Fatal("retry changed successful item", q, err)
	}
	summary := playlistSummaries(q)
	if summary[0].Completed != 1 || summary[0].Failed != 1 {
		t.Fatal(summary)
	}
}
