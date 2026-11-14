package engine

import (
	"testing"
	"time"
)

func TestCurrentDownloadSpeedUsesRecentBytesAndExpires(t *testing.T) {
	now := time.Unix(1000, 0)
	speed := queueSpeed{bytes: 900000, sampled: now}
	speed.update("Downloaded 901024/2000000 bytes (45.1%), 999999 bytes/s", now.Add(time.Second))
	if got := speed.current(now.Add(time.Second)); got != 1024 {
		t.Fatalf("resumed speed includes saved bytes or lifetime average: %v", got)
	}
	speed.update("Downloaded 903072/2000000 bytes (45.2%)", now.Add(2*time.Second))
	if got := speed.current(now.Add(2 * time.Second)); got != 2048 {
		t.Fatalf("recent speed did not change: %v", got)
	}
	if got := speed.current(now.Add(5 * time.Second)); got != 0 {
		t.Fatalf("stalled download shows old speed: %v", got)
	}
	speed.update("Downloaded 903072/2000000 bytes (45.2%)", now.Add(6*time.Second))
	if got := speed.current(now.Add(6 * time.Second)); got != 0 {
		t.Fatalf("zero-byte interval shows speed: %v", got)
	}
}

func TestCurrentSpeedIsPerJobAndNeverSavedForPausedJobs(t *testing.T) {
	now := time.Unix(1000, 0)
	speeds := map[int]*queueSpeed{1: {sampled: now}, 2: {sampled: now}}
	speeds[1].update("Downloaded 1024 bytes, 99999 bytes/s", now.Add(time.Second))
	speeds[2].update("Downloaded 8192/100000 bytes", now.Add(time.Second))
	jobs := []queueJob{{ID: 1, State: "running"}, {ID: 2, State: "running"}, {ID: 3, State: "paused"}}
	entries := desktopEntries(jobs, speeds, nil, now.Add(time.Second))
	if entries[0].Speed == nil || *entries[0].Speed != 1024 || entries[1].Speed == nil || *entries[1].Speed != 8192 {
		t.Fatalf("per-job rates mixed: %+v", entries)
	}
	jobs[0].State = "paused"
	entries = desktopEntries(jobs, speeds, nil, now.Add(time.Second))
	if entries[0].Speed != nil || entries[2].Speed != nil {
		t.Fatal("paused job exposes stale running speed")
	}
}
