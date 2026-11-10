//go:build linux

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestQueueHardwarePressureRetiresSafelyAndManualOverrides(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var healthy atomic.Bool
	h := startAutomaticQueueHarness(t, func(at time.Time) hardwareSnapshot {
		cpu := float64(95)
		if healthy.Load() {
			cpu = 20
		}
		return knownHardware(at, cpu)
	})
	s := newResizeServer(t)
	dir := t.TempDir()
	h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: s.URL, Destination: filepath.Join(dir, "first")}})
	h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: s.URL, Destination: filepath.Join(dir, "second")}})
	h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: s.URL, Destination: filepath.Join(dir, "later"), State: "paused"}})
	h.request(t, desktopRequest{Operation: "workers", Job: queueJob{ID: 1}, Connections: 4})
	waitQueue(t, func() bool { return s.activeCount() == 4 })
	tick := func() {
		at := time.Unix(0, h.at.Add(int64(time.Second)))
		h.ticks <- at
		h.request(t, desktopRequest{Operation: "snapshot"})
	}
	for i := 0; i < 11; i++ {
		tick()
	}
	waitQueue(t, func() bool { return s.activeCount() == 3 })
	r := h.request(t, desktopRequest{Operation: "snapshot"})
	if r.Management.Status != "Backing off" || !strings.Contains(r.Management.Reason, "CPU") || r.Jobs[1].State != "queued" || r.Jobs[2].State != "paused" {
		t.Fatalf("pressure status/admission incorrect: %+v", r)
	}
	for i := 0; i < 10; i++ {
		tick()
	}
	waitQueue(t, func() bool { return s.activeCount() == 2 })
	manual := requestPolicy{Mode: "manual", Total: 8, PerOrigin: 4}
	h.request(t, desktopRequest{Operation: "configure", MaxActive: 1, Connections: 4, Management: &manual})
	waitQueue(t, func() bool { return s.activeCount() == 4 })
	for i := 0; i < 12; i++ {
		tick()
	}
	if s.activeCount() != 4 {
		t.Fatal("hardware tuner overrode Manual")
	}
	auto := defaultRequestPolicy()
	h.request(t, desktopRequest{Operation: "configure", MaxActive: 1, Connections: 4, Management: &auto})
	waitQueue(t, func() bool { return s.activeCount() == 1 })
	healthy.Store(true)
	for i := 0; i < 31; i++ {
		tick()
	}
	r = h.request(t, desktopRequest{Operation: "snapshot"})
	if r.Management.Status == "Backing off" {
		t.Fatal("sustained healthy observations did not recover", r.Management.Reason)
	}
	close(s.release)
	waitQueue(t, func() bool {
		r = h.request(t, desktopRequest{Operation: "snapshot"})
		return r.Jobs[0].State == "completed" && r.Jobs[1].State == "completed"
	})
	requireContents(t, filepath.Join(dir, "first"), s.payload)
	requireContents(t, filepath.Join(dir, "second"), s.payload)
	if r.Management.Buffers == nil || r.Management.Buffers.Transfer != 0 || r.Management.Buffers.Hash != 0 {
		t.Fatal("completed queue leaked buffer reservations", r.Management.Buffers)
	}
	path, _ := queuePath()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"cpu_percent", "engine_rss_bytes", "hardware", "p95_seconds", "transfer_bytes"} {
		if strings.Contains(string(data), name) {
			t.Fatal("hardware observations persisted", name)
		}
	}
}
func TestBufferPressureNeverPublishesIncompleteFile(t *testing.T) {
	for _, cancelWaiting := range []bool{false, true} {
		t.Run(map[bool]string{false: "drain", true: "cancel"}[cancelWaiting], func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			s := newResizeServer(t)
			budget := newBufferBudget()
			slots := 2
			if cancelWaiting {
				slots = 1
			}
			budget.maximumHash = slots * transferBufferSize
			budget.maximumTransfer = slots * transferBufferSize
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx = context.WithValue(ctx, bufferBudgetKey{}, budget)
			writer := &queueProgressWriter{updates: make(chan queueProgress, 1)}
			dest := filepath.Join(t.TempDir(), "file")
			done := make(chan error, 1)
			go func() {
				done <- runQueueDownloadWithProgress(ctx, queueJob{ID: 1, URL: s.URL, Destination: dest}, 4, writer)
			}()
			waitQueue(t, func() bool {
				view := writer.segments.Load()
				return view != nil && strings.Contains(view.Reason, "buffer capacity")
			})
			requireMissing(t, dest)
			if cancelWaiting {
				cancel()
			} else {
				if s.activeCount() != 1 {
					t.Fatal("worker started without a reservation")
				}
				close(s.release)
			}
			select {
			case err := <-done:
				if cancelWaiting {
					if err == nil || !onlyCancellation(err) {
						t.Fatal("buffer wait did not cancel safely", err)
					}
				} else if err != nil {
					t.Fatal(err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("buffer pressure deadlocked transfer")
			}
			if cancelWaiting {
				requireMissing(t, dest)
			} else {
				requireContents(t, dest, s.payload)
			}
			if snap := budget.snapshot(); snap.Transfer != 0 || snap.Hash != 0 {
				t.Fatal("buffer capacity leaked", snap)
			}
		})
	}
}
func TestReservedCopiesAvoidUnaccountedFastPaths(t *testing.T) {
	b := newBufferBudget()
	buffers, err := b.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer buffers.release()
	ctx := context.WithValue(context.Background(), transferBufferKey{}, buffers)
	if n, err := copyTransfer(ctx, io.Discard, strings.NewReader("verified"), false); err != nil || n != 8 || len(buffers.read) != transferBufferSize {
		t.Fatal("read bypassed reserved buffer", n, err)
	}
	if n, err := copyTransferN(ctx, io.Discard, strings.NewReader("hash"), 8, true); err != io.EOF || n != 4 || len(buffers.hash) != transferBufferSize {
		t.Fatal("hash copy bounds/EOF changed", n, err)
	}
}
