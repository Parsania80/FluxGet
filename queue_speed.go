package main

import (
	"fmt"
	"strings"
	"time"
)

// Rates belong to the live session, never to persisted recovery metadata.
type queueSpeed struct {
	bytes   int64
	sampled time.Time
	rate    float64
}

func (s *queueSpeed) update(progress string, now time.Time) {
	if !strings.HasPrefix(progress, "Downloaded ") {
		return
	}
	var bytes int64
	if _, err := fmt.Sscanf(progress, "Downloaded %d", &bytes); err != nil || bytes < 0 {
		return
	}
	elapsed := now.Sub(s.sampled).Seconds()
	if elapsed <= 0 {
		return
	}
	s.rate = 0
	if bytes >= s.bytes {
		s.rate = float64(bytes-s.bytes) / elapsed
	}
	s.bytes, s.sampled = bytes, now
}

func (s *queueSpeed) current(now time.Time) float64 {
	if now.Sub(s.sampled) >= 2500*time.Millisecond {
		return 0
	}
	return s.rate
}

type desktopEntry struct {
	queueJob
	Speed *float64 `json:"speed_bytes_per_second,omitempty"`
	segmentSnapshot
}

func desktopEntries(jobs []queueJob, speeds map[int]*queueSpeed, layouts map[int]segmentSnapshot, now time.Time) []desktopEntry {
	entries := make([]desktopEntry, 0, len(jobs))
	for _, job := range jobs {
		entry := desktopEntry{queueJob: job}
		entry.segmentSnapshot = layouts[job.ID]
		if progress := entry.segmentSnapshot.progress(); progress != "" {
			entry.Progress = progress
		}
		if speed := speeds[job.ID]; job.State == "running" && speed != nil {
			rate := speed.current(now)
			entry.Speed = &rate
		}
		entries = append(entries, entry)
	}
	return entries
}
