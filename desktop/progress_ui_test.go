package main

import "testing"

func TestProgressValue(t *testing.T) {
	for _, tc := range []struct {
		message string
		value   float64
		known   bool
	}{
		{"Downloaded 25/100 bytes (25.0%), 100 bytes/s", 0.25, true},
		{"Job 3: Downloaded 3/4 bytes (75.0%)", 0.75, true},
		{"Downloaded 100/100 bytes (100.0%)", 1, true},
		{"Downloaded 100 bytes, 50 bytes/s", 0, false},
		{"Downloaded 0/0 bytes", 0, false},
		{"Downloaded 11/10 bytes", 0, false},
		{"Connecting…", 0, false},
	} {
		value, known := progressValue(tc.message)
		if value != tc.value || known != tc.known {
			t.Errorf("progressValue(%q) = (%v, %v), want (%v, %v)", tc.message, value, known, tc.value, tc.known)
		}
	}
}

func TestReadableCurrentDownloadSpeed(t *testing.T) {
	for _, tc := range []struct {
		rate float64
		want string
	}{
		{0, "Speed: 0.00 MB/s"}, {512, "Speed: 0.00051 MB/s"}, {64 * 1024, "Speed: 0.07 MB/s"}, {2500000, "Speed: 2.50 MB/s"}, {1000000000, "Speed: 1000.00 MB/s"},
	} {
		if got := downloadSpeed(queueEntry{State: "running", Speed: &tc.rate}, "MB"); got != tc.want {
			t.Errorf("rate %v: %q, want %q", tc.rate, got, tc.want)
		}
	}
	stale := 102400.0
	if got := downloadSpeed(queueEntry{State: "paused", Speed: &stale}, "MB"); got != "Speed: —" {
		t.Fatalf("paused speed: %q", got)
	}
	if got := downloadSpeed(queueEntry{State: "running"}, "MB"); got != "Speed: measuring…" {
		t.Fatalf("missing speed: %q", got)
	}
}

func TestRemainingTimeUsesCurrentSpeedForParallelProgress(t *testing.T) {
	speed := 100.0
	j := queueEntry{State: "running", Progress: "Downloaded 100/1000 bytes (10.0%)", Speed: &speed}
	if got := downloadDetails(j, "MB"); got != "Downloaded 0.0001 / 0.001 MB (10.0%) · about 9s left" {
		t.Fatalf("parallel remaining time: %q", got)
	}
	speed = 0
	if got := downloadDetails(j, "MB"); got != "Downloaded 0.0001 / 0.001 MB (10.0%)" {
		t.Fatalf("zero-speed ETA: %q", got)
	}
	j.Progress = "Downloaded 100/1000 bytes (10.0%), 999999 bytes/s"
	speed = 100
	if got := downloadDetails(j, "MB"); got != "Downloaded 0.0001 / 0.001 MB (10.0%) · about 9s left" {
		t.Fatalf("lifetime average leaked into current display: %q", got)
	}
}
