package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func automaticFixture() (*automaticController, *requestBudget, []automaticJob, time.Time) {
	a := newAutomaticController()
	b := newRequestBudget(defaultRequestPolicy())
	jobs := []automaticJob{{id: 1, origin: "http://a:80", target: 1, maximum: 4, requests: jobBudgetSnapshot{Active: 1}, layout: segmentSnapshot{Mode: "parallel", Target: 1, Parts: []transferSegment{{Start: 0, End: 32 << 20, State: "downloading"}, {Start: 32 << 20, End: 64 << 20, State: "waiting"}, {Start: 64 << 20, End: 96 << 20, State: "waiting"}, {Start: 96 << 20, End: 128 << 20, State: "waiting"}}}}}
	now := time.Unix(100, 0)
	a.tick(now, 1, 4, jobs, nil, b)
	return a, b, jobs, now
}
func advanceAutomatic(a *automaticController, b *requestBudget, jobs []automaticJob, now *time.Time, seconds int, rate int64, queued []queueJob) *automaticUpdate {
	var update *automaticUpdate
	for n := 0; n < seconds; n++ {
		*now = now.Add(time.Second)
		jobs[0].metrics.Accepted += rate
		update = a.tick(*now, a.generation, 4, jobs, queued, b)
		if update != nil {
			jobs[0].target = update.target
			jobs[0].layout.Target = update.target
		}
	}
	return update
}

func TestAutomaticMeasuredWorkerTrials(t *testing.T) {
	for _, gain := range []bool{true, false} {
		t.Run(map[bool]string{true: "gain", false: "no-gain"}[gain], func(t *testing.T) {
			a, b, jobs, now := automaticFixture()
			defer b.close()
			advanceAutomatic(a, b, jobs, &now, 19, 1000, nil)
			if jobs[0].target != 1 {
				t.Fatal("growth without two windows")
			}
			advanceAutomatic(a, b, jobs, &now, 1, 1000, nil)
			if jobs[0].target != 2 || !a.trial {
				t.Fatalf("missing measured trial: %+v %+v", a, jobs)
			}
			jobs[0].metrics.WorkerChanges++ // Expected transition must not erase the trial baseline.
			advanceAutomatic(a, b, jobs, &now, 6, 1000, nil)
			rate := int64(1001)
			if gain {
				rate = 1500
			}
			advanceAutomatic(a, b, jobs, &now, 20, rate, nil)
			want := 1
			if gain {
				want = 2
			}
			if jobs[0].target != want || a.trial {
				t.Fatalf("wrong trial outcome target=%d trial=%v reason=%s", jobs[0].target, a.trial, a.reason)
			}
			advanceAutomatic(a, b, jobs, &now, 20, rate, nil)
			if a.trial {
				t.Fatal("ignored cooldown")
			}
		})
	}
}

func TestAutomaticFileTrialsDrainInsteadOfCancel(t *testing.T) {
	a, b, jobs, now := automaticFixture()
	defer b.close()
	queued := []queueJob{{ID: 2, URL: "http://b/file", State: "queued"}}
	advanceAutomatic(a, b, jobs, &now, 20, 1000, queued)
	if a.files != 2 || !a.trial || a.candidate != 0 || jobs[0].target != 1 {
		t.Fatalf("not a single-variable file trial: %+v", a)
	}
	a.admitted(2)
	second := jobs[0]
	second.id = 2
	second.origin = "http://b:80"
	second.metrics = measurementSnapshot{}
	jobs = append(jobs, second)
	advanceAutomatic(a, b, jobs, &now, 6, 1000, nil)
	advanceAutomatic(a, b, jobs, &now, 20, 1000, nil)
	if a.files != 1 || a.trial || jobs[0].target != 1 || jobs[1].target != 1 {
		t.Fatalf("failed file trial did not drain: %+v", a)
	}
	if len(jobs) != 2 {
		t.Fatal("canceled an admitted download")
	}
}

func TestAutomaticLimitsEligibilityAndDisruption(t *testing.T) {
	for _, kind := range []string{"file-limit", "overall-limit", "near-end", "sequential", "saved-layout", "unknown-limits", "unserved-file"} {
		t.Run(kind, func(t *testing.T) {
			a, b, jobs, now := automaticFixture()
			defer b.close()
			switch kind {
			case "file-limit":
				a.limits = liveSpeedConfig{Enabled: true, Default: 1}
			case "overall-limit":
				a.limits = liveSpeedConfig{Overall: 1}
			case "near-end":
				jobs[0].layout.Parts = []transferSegment{{End: 8 << 20}, {Start: 8 << 20, End: 16 << 20, Downloaded: 1}}
			case "sequential":
				jobs[0].layout.Mode = "sequential"
			case "saved-layout":
				jobs[0].layout.Parts = jobs[0].layout.Parts[:1]
			case "unknown-limits":
				a.limitsKnown = false
			case "unserved-file":
				second := jobs[0]
				second.id = 2
				second.requests.Active = 0
				jobs = append(jobs, second)
			}
			for i := 0; i < 30; i++ {
				jobs[0].metrics.LimiterWait += int64(time.Second)
				advanceAutomatic(a, b, jobs, &now, 1, 1024, nil)
			}
			if jobs[0].target != 1 || a.trial {
				t.Fatalf("grew despite %s", kind)
			}
		})
	}
	a, b, jobs, now := automaticFixture()
	defer b.close()
	advanceAutomatic(a, b, jobs, &now, 20, 1000, nil)
	update := a.tick(now.Add(time.Second), 2, 4, jobs, nil, b)
	if update == nil || update.target != 1 || a.trial {
		t.Fatal("job-set change retained unproven worker", update)
	}
}

func TestAutomaticPerFileLimitStillAllowsAnotherOrigin(t *testing.T) {
	a, b, jobs, now := automaticFixture()
	defer b.close()
	a.limits = liveSpeedConfig{Enabled: true, Default: 1}
	queued := []queueJob{{ID: 2, URL: "http://b/file", State: "queued"}}
	for i := 0; i < 20; i++ {
		jobs[0].metrics.LimiterWait += int64(time.Second)
		advanceAutomatic(a, b, jobs, &now, 1, 1024, queued)
	}
	if a.files != 2 || jobs[0].target != 1 {
		t.Fatal("per-file ceiling incorrectly blocked another file")
	}
}

func TestServerCooldownIsSharedCancelableAndOriginScoped(t *testing.T) {
	b := newRequestBudget(defaultRequestPolicy())
	defer b.close()
	b.serverCooldown("http://a:80", 100*time.Millisecond, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan *requestLease, 1)
	go func() { lease, _ := b.acquire(ctx, 2, "http://a:80"); done <- lease }()
	lease, err := b.acquire(ctx, 3, "http://b:80")
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
	select {
	case <-done:
		t.Fatal("same-origin request bypassed cooldown")
	default:
	}
	select {
	case lease := <-done:
		if lease == nil {
			t.Fatal("wake failed")
		}
		lease.release()
	case <-time.After(time.Second):
		t.Fatal("cooldown expiry did not wake waiters")
	}
	b.serverCooldown("http://a:80", time.Second, true)
	blockedCtx, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if _, err := b.acquire(blockedCtx, 4, "http://a:80"); err == nil {
		t.Fatal("over-one-minute stop was bypassed")
	}
	b.retryOrigin("http://a:80")
	lease, err = b.acquire(ctx, 4, "http://a:80")
	if err != nil {
		t.Fatal(err)
	}
	lease.release()
	if s, _ := b.snapshot(); s.Active != 0 || s.Waiting != 0 {
		t.Fatal("cooldown/cancellation leaked capacity", s)
	}
}

func TestAdaptiveRejectsInterruptedAndRegressingTrials(t *testing.T) {
	for _, kind := range []string{"invalid", "affected", "latency", "generation"} {
		t.Run(kind, func(t *testing.T) {
			var p adaptivePolicy
			in := adaptiveInput{At: time.Unix(100, 0), Workers: 1, Maximum: 4, Eligible: true, AffectedRate: 100, HeaderDelay: 10, Window: transferObservation{Valid: true, Seconds: 10, BytesPerSecond: 100, Generation: 1}}
			p.observe(in)
			in.At = in.At.Add(10 * time.Second)
			p.observe(in)
			in.Workers = 2
			in.At = in.At.Add(10 * time.Second)
			in.Window.BytesPerSecond = 150
			switch kind {
			case "invalid":
				in.Window.Valid = false
			case "generation":
				in.Window.Generation++
			case "affected":
				in.AffectedRate = 50
			case "latency":
				in.HeaderDelay = 30
			}
			d := p.observe(in)
			if kind == "affected" || kind == "latency" {
				in.At = in.At.Add(10 * time.Second)
				d = p.observe(in)
			}
			if d.Target != 1 {
				t.Fatalf("retained bad trial: %+v", d)
			}
		})
	}
}

func TestAutomaticBudgetHeadroomAndDrainingAdmission(t *testing.T) {
	for _, origin := range []string{"http://a:80", "http://b:80"} {
		a, b, jobs, now := automaticFixture()
		b.configure(requestPolicy{Mode: "automatic", Total: 1, PerOrigin: 1})
		lease, err := b.acquire(context.Background(), 1, origin)
		if err != nil {
			t.Fatal(err)
		}
		advanceAutomatic(a, b, jobs, &now, 30, 1000, []queueJob{{ID: 2, URL: "http://a/file", State: "queued"}})
		if a.trial || a.files != 1 || jobs[0].target != 1 {
			t.Fatal("growth without request headroom")
		}
		lease.release()
		b.close()
	}
	a, b, jobs, now := automaticFixture()
	defer b.close()
	jobs[0].layout.Mode = "sequential"
	second := jobs[0]
	second.id = 2
	jobs = append(jobs, second)
	advanceAutomatic(a, b, jobs, &now, 30, 1000, []queueJob{{ID: 3, URL: "http://b/file", State: "queued"}})
	if a.trial || a.files != 1 {
		t.Fatal("trial counted an already-admitted draining file as new work")
	}
}

func TestAutomaticLiveLimitsChangesInvalidateLearning(t *testing.T) {
	a := newAutomaticController()
	path := filepath.Join(t.TempDir(), "speed.json")
	ctx := context.WithValue(context.Background(), liveSpeedKey{}, path)
	if !a.readLimits(ctx) || a.limitsKnown {
		t.Fatal("missing speed settings treated as unlimited")
	}
	if err := os.WriteFile(path, []byte(`{"enabled":true,"default_kib":100,"overall_kib":200,"overrides":{"queue:1":50}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !a.readLimits(ctx) || !a.limitsKnown || a.fileLimit(1) != 50*1024 || a.limits.Overall != 200 {
		t.Fatal("valid settings not observed")
	}
	if a.readLimits(ctx) {
		t.Fatal("unchanged settings invalidated every window")
	}
	if err := os.WriteFile(path, []byte(`{"overall_kib":-1}`), 0600); err != nil {
		t.Fatal(err)
	}
	if !a.readLimits(ctx) || a.limitsKnown {
		t.Fatal("invalid settings did not freeze growth")
	}
	b := newRequestBudget(defaultRequestPolicy())
	defer b.close()
	b.serverCooldown("http://removed:80", time.Second, true)
	b.pruneOrigins(map[string]bool{})
	if b.originCooling("http://removed:80", time.Now()) {
		t.Fatal("removed job left blocked origin history")
	}
}

func TestAutomaticUnexpectedAdmissionInvalidatesFileTrial(t *testing.T) {
	a, b, jobs, now := automaticFixture()
	defer b.close()
	queued := []queueJob{{ID: 2, URL: "http://b/file", State: "queued"}}
	advanceAutomatic(a, b, jobs, &now, 20, 1000, queued)
	a.admitted(2)
	a.admitted(3) // A borrowed cooldown slot is not the single-variable experiment.
	a.tick(now.Add(time.Second), 3, 4, jobs, nil, b)
	if a.trial || a.files != 1 {
		t.Fatal("unexpected membership was attributed to the trial")
	}
}

func TestOriginBackoffLimitsAggregateOneWorkerJobs(t *testing.T) {
	b := newRequestBudget(defaultRequestPolicy())
	defer b.close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first, _ := b.acquire(ctx, 1, "http://a:80")
	second, _ := b.acquire(ctx, 2, "http://a:80")
	now := time.Unix(100, 0)
	b.backOffOrigin("http://a:80", now)
	waiting := make(chan *requestLease, 1)
	go func() { lease, _ := b.acquire(ctx, 3, "http://a:80"); waiting <- lease }()
	first.release()
	if b.canStart("http://a:80", now) {
		t.Fatal("aggregate origin backoff bypassed by a new file")
	}
	other, err := b.acquire(ctx, 4, "http://b:80")
	if err != nil {
		t.Fatal(err)
	}
	other.release()
	select {
	case <-waiting:
		t.Fatal("over-limit origin did not drain")
	default:
	}
	second.release()
	var lease *requestLease
	select {
	case lease = <-waiting:
	case <-time.After(time.Second):
		t.Fatal("origin failed to admit after draining")
	}
	if b.recoverOrigin(now.Add(59 * time.Second)) {
		t.Fatal("premature origin recovery")
	}
	if !b.recoverOrigin(now.Add(time.Minute)) || !b.canStart("http://a:80", now.Add(time.Minute)) {
		t.Fatal("quiet origin never recovered")
	}
	lease.release()
}
