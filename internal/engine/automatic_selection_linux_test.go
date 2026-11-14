//go:build linux

package engine

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Exercise actual HTTP bodies and transfer owners while advancing only the
// observation clock. Keep enough data remaining for several useful trials.
func TestAutomaticSelectionGrowsToFourAndRejectsSharedCap(t *testing.T) {
	for _, gain := range []bool{true, false} {
		name := map[bool]string{true: "useful-ranges", false: "shared-server-cap"}[gain]
		t.Run(name, func(t *testing.T) {
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			s := newAutomaticHTTPFixtureSized(t, 8<<20, true)
			h := startAutomaticQueueHarness(t, func(at time.Time) hardwareSnapshot { return knownHardware(at, 20) })
			dest := filepath.Join(t.TempDir(), "image.iso")
			h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: s.server.URL, Destination: dest}})
			waitQueue(t, func() bool { return s.count() == 1 })
			// Let the first real part layout reach the queue before the first window.
			waitQueue(t, func() bool {
				r := h.request(t, desktopRequest{Operation: "snapshot"})
				return r.Jobs[0].Mode == "parallel" && len(r.Jobs[0].Parts) == 4
			})
			h.ticks <- time.Unix(0, h.at.Load())
			seen := map[int]bool{1: true}
			last := 1
			rollbacks := 0
			var result desktopResponse
			for second := 1; second <= 200; second++ {
				result = h.pulse(t, s, gain)
				target := result.Jobs[0].Target
				if target != last {
					t.Logf("observation second %d: %d -> %d (%s)", second, last, target, result.Management.Reason)
					if target < last {
						rollbacks++
					}
					last = target
					waitQueue(t, func() bool { return s.count() == target })
					// The sampler excludes changing worker ownership. Publish a stable
					// layout before the next accelerated observation, as the real 1s tick does.
					time.Sleep(550 * time.Millisecond)
				}
				seen[target] = true
				if second < 20 && target != 1 {
					t.Fatal("grew before two baseline windows")
				}
				if gain && target == 4 && result.Management.Reason == "extra workers improved throughput" {
					break
				}
				if !gain && rollbacks == 2 {
					break
				}
			}
			if gain {
				for n := 1; n <= 4; n++ {
					if !seen[n] {
						t.Fatalf("never selected %d workers; final target=%d reason=%s", n, last, result.Management.Reason)
					}
				}
				if last != 4 || result.Management.Reason != "extra workers improved throughput" {
					t.Fatalf("four-worker gain not retained: target=%d reason=%s", last, result.Management.Reason)
				}
			} else if !seen[2] || seen[3] || rollbacks != 2 || last != 1 {
				t.Fatalf("unhelpful workers stuck or untested: seen=%v rollbacks=%d target=%d reason=%s", seen, rollbacks, last, result.Management.Reason)
			}
			close(s.finish)
			waitQueue(t, func() bool {
				r := h.request(t, desktopRequest{Operation: "snapshot"})
				return r.Jobs[0].State == "completed" && r.Management.Active == 0 && r.Management.Waiting == 0
			})
			requireContents(t, dest, s.payload)
			if s.probes.Load() != 1 || s.full.Load() != 0 {
				t.Fatal("parallel path unexpectedly fell back", s.probes.Load(), s.full.Load())
			}
		})
	}
}

func TestAutomaticSelectionRangeSupportWithoutETagStaysSequential(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newAutomaticHTTPFixtureSized(t, 4<<20, false)
	h := startAutomaticQueueHarness(t, func(at time.Time) hardwareSnapshot { return knownHardware(at, 20) })
	dest := filepath.Join(t.TempDir(), "no-etag.iso")
	h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: s.server.URL, Destination: dest}})
	waitQueue(t, func() bool { return s.count() == 1 })
	for _, mode := range []string{"automatic", "manual"} {
		policy := requestPolicy{Mode: mode, Total: 8, PerOrigin: 4}
		h.request(t, desktopRequest{Operation: "configure", MaxActive: 3, Connections: 4, Management: &policy})
		for second := 0; second < 40; second++ {
			r := h.pulse(t, s, true)
			if r.Jobs[0].Mode != "sequential" || len(r.Jobs[0].Parts) != 1 || s.count() != 1 {
				t.Fatalf("unsafe parallel work without validator: mode=%s layout=%+v active=%d", mode, r.Jobs[0].segmentSnapshot, s.count())
			}
		}
	}
	if s.probes.Load() != 1 || s.ranges.Load() != 0 || s.full.Load() != 1 {
		t.Fatal("single probe must fall back to exactly one full response", s.probes.Load(), s.ranges.Load(), s.full.Load())
	}
	close(s.finish)
	waitQueue(t, func() bool {
		r := h.request(t, desktopRequest{Operation: "snapshot"})
		return r.Jobs[0].State == "completed" && r.Management.Active == 0 && r.Management.Waiting == 0
	})
	requireContents(t, dest, s.payload)
}

// Opt-in only: the ordinary suite stays local and deterministic. This probes
// one public range byte with the production Go client, never a full ISO.
func TestAutomaticSelectionPublicRangeProbe(t *testing.T) {
	raw := os.Getenv("FLUXGET_PUBLIC_RANGE_PROBE_URL")
	if raw == "" {
		t.Skip("requires an explicitly selected public URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		t.Fatal("probe requires a public HTTPS URL without credentials/query/fragment")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client := defaultDownloadClient()
	base := client.Transport
	var status int
	var etag, contentRange string
	client.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		response, err := base.RoundTrip(req)
		if err == nil {
			status = response.StatusCode
			etag = response.Header.Get("ETag")
			contentRange = response.Header.Get("Content-Range")
		}
		return response, err
	})
	rep, parallel, err := probeParallel(ctx, client, raw)
	if err != nil {
		t.Fatal("public range probe failed (URL omitted)")
	}
	t.Logf("production probe: HTTP=%d strong_ETag=%t range=%s parallel_eligible=%t size=%d", status, strongETag(etag), contentRange, parallel, rep.size)
	if !strongETag(etag) && parallel {
		t.Fatal("unsafe parallel selection without strong ETag")
	}
}

// The opt-in smoke uses real one-second scheduler ticks and paced HTTP bytes.
// It catches timing integration defects hidden by accelerated observation tests.
func TestAutomaticSelectionWallClockSmoke(t *testing.T) {
	if os.Getenv("FLUXGET_AUTOMATIC_WALL_CLOCK_SMOKE") != "1" {
		t.Skip("requires explicit ~50-second wall-clock run")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s := newAutomaticHTTPFixtureSized(t, 16<<20, true)
	h := &automaticQueueHarness{output: &queueOutput{}, done: make(chan error, 1)}
	input, commands := io.Pipe()
	h.commands = commands
	ctx, cancel := context.WithCancel(context.Background())
	ctx = context.WithValue(ctx, hardwareSourceKey{}, func(at time.Time) hardwareSnapshot { return knownHardware(at, 20) })
	ticks := time.NewTicker(time.Second)
	go func() { h.done <- runQueueClock(ctx, input, h.output, 3, 4, time.Now, ticks.C) }()
	t.Cleanup(func() {
		ticks.Stop()
		cancel()
		commands.Close()
		select {
		case err := <-h.done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("real-clock queue did not stop")
		}
	})
	policy := defaultRequestPolicy()
	h.request(t, desktopRequest{Operation: "initialize", Management: &policy})
	dest := filepath.Join(t.TempDir(), "real-clock.iso")
	h.request(t, desktopRequest{Operation: "add", Job: queueJob{URL: s.server.URL, Destination: dest}})
	waitQueue(t, func() bool { return s.count() == 1 })
	pumpCtx, stopPump := context.WithCancel(context.Background())
	pumpDone := make(chan struct{})
	go func() {
		defer close(pumpDone)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-pumpCtx.Done():
				return
			case <-ticker.C:
				s.pulse(true)
			}
		}
	}()
	t.Cleanup(func() { stopPump(); <-pumpDone })
	started := time.Now()
	deadline := started.Add(65 * time.Second)
	var result desktopResponse
	for time.Now().Before(deadline) {
		result = h.request(t, desktopRequest{Operation: "snapshot"})
		if time.Since(started) < 19*time.Second && result.Jobs[0].Target > 1 {
			t.Fatal("grew before real baseline elapsed")
		}
		if result.Jobs[0].Target == 2 && s.count() == 2 && result.Management.Reason == "extra workers improved throughput" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if result.Jobs[0].Target != 2 || result.Management.Reason != "extra workers improved throughput" {
		t.Fatalf("real-clock trial stuck: target=%d reason=%s", result.Jobs[0].Target, result.Management.Reason)
	}
	t.Logf("real-clock useful worker retained after %.1fs; active connections=%d", time.Since(started).Seconds(), s.count())
	stopPump()
	<-pumpDone
	close(s.finish)
	waitQueue(t, func() bool {
		r := h.request(t, desktopRequest{Operation: "snapshot"})
		return r.Jobs[0].State == "completed" && r.Management.Active == 0 && r.Management.Waiting == 0
	})
	requireContents(t, dest, s.payload)
}
