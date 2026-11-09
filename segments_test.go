package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestLiveSegmentCountersAndFinalSnapshot(t *testing.T) {
	writer := &queueProgressWriter{updates: make(chan queueProgress, 1)}
	session := newSegmentSession(writer, "parallel", 2, []transferSegment{{Start: 0, End: 100, State: "waiting"}, {Start: 100, End: 200, State: "completed", Downloaded: 100}})
	session.part(0).setState(segmentDownloading)
	session.part(0).add(35)
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot := writer.segments.Load()
		if snapshot != nil && snapshot.Parts[0].Downloaded == 35 {
			break
		}
		if time.Now().After(deadline) {
			session.finish(context.Background(), false)
			t.Fatal("live part counter was not published while otherwise idle")
		}
		time.Sleep(10 * time.Millisecond)
	}
	session.finish(context.Background(), false)
	snapshot := writer.segments.Load()
	if snapshot.Parts[0].State != "failed" || snapshot.Parts[1].State != "completed" || snapshot.Parts[0].Downloaded != 35 {
		t.Fatalf("incorrect final parts: %+v", snapshot)
	}
	data, err := json.Marshal(desktopEntries([]queueJob{{ID: 1, State: "failed"}}, nil, map[int]segmentSnapshot{1: *snapshot}, time.Now()))
	if err != nil || !strings.Contains(string(data), `"segments"`) {
		t.Fatalf("parts missing from desktop protocol: %s %v", data, err)
	}
	persisted, _ := json.Marshal(queueData{Version: 2, NextID: 2, Jobs: []queueJob{{ID: 1, State: "failed"}}})
	if strings.Contains(string(persisted), `"segments"`) {
		t.Fatal("runtime counters became recovery metadata")
	}
}

func TestSavedSegmentsRetainRealChunkBoundaries(t *testing.T) {
	total := int64(303)
	c := checkpoint{Version: 3, Total: &total, Chunks: []savedChunk{{Start: 0, End: 100, SHA256: "saved"}, {Start: 100, End: 200}, {Start: 200, End: 303}}}
	snapshot := segmentsFromCheckpoint(c, 1)
	if snapshot.Limit != 1 || snapshot.Mode != "parallel" || len(snapshot.Parts) != 3 || snapshot.Parts[0].Downloaded != 100 || snapshot.Parts[0].State != "saved" || snapshot.Parts[2].Start != 200 || snapshot.Parts[2].End != 303 {
		t.Fatalf("saved layout changed with connection limit: %+v", snapshot)
	}
}

func TestSavedPrefixesDriveOverallDesktopProgress(t *testing.T) {
	total := int64(300)
	c := checkpoint{Version: 4, Total: &total, Chunks: []savedChunk{{Start: 0, End: 100, Downloaded: 25}, {Start: 100, End: 200, Downloaded: 50}, {Start: 200, End: 300, SHA256: "saved", Downloaded: 100}}}
	snapshot := segmentsFromCheckpoint(c, 1)
	entries := desktopEntries([]queueJob{{ID: 1, State: "paused", Progress: "Downloaded 0/300 bytes (0.0%)"}}, nil, map[int]segmentSnapshot{1: snapshot}, time.Now())
	if len(snapshot.Parts) != 3 || snapshot.Parts[0].Downloaded != 25 || snapshot.Parts[1].Downloaded != 50 || entries[0].Progress != "Downloaded 175/300 bytes (58.3%)" {
		t.Fatalf("saved prefixes lost in desktop snapshot: %+v", entries)
	}
}
