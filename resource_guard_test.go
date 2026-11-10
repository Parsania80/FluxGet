package main

import (
	"context"
	"testing"
	"time"
)

func knownHardware(at time.Time, cpu float64) hardwareSnapshot {
	some, full, ratio := float64(0), float64(0), float64(.5)
	return hardwareSnapshot{At: at, CPUPercent: &cpu, MemorySome: &some, MemoryFull: &full, GroupMemoryRatio: &ratio, MemoryEventsKnown: true, CPUState: "Measured", MemoryState: "Measured"}
}
func TestResourceCPUHysteresisUnknownAndRecovery(t *testing.T) {
	g := newResourceGuard()
	now := time.Unix(100, 0)
	for second := 0; second < 10; second++ {
		at := now.Add(time.Duration(second) * time.Second)
		g.observe(at, knownHardware(at, 90), nil, 1)
		if g.blocked {
			t.Fatal("single CPU spike blocked growth")
		}
	}
	at := now.Add(10 * time.Second)
	g.observe(at, knownHardware(at, 90), nil, 1)
	if !g.blocked {
		t.Fatal("sustained CPU quota pressure ignored")
	}
	for second := 11; second <= 45; second++ {
		at = now.Add(time.Duration(second) * time.Second)
		g.observe(at, hardwareSnapshot{At: at, CPUState: "Unknown", MemoryState: "Unknown"}, nil, 1)
	}
	if !g.blocked {
		t.Fatal("missing metrics were treated as healthy recovery")
	}
	for second := 46; second < 76; second++ {
		at = now.Add(time.Duration(second) * time.Second)
		g.observe(at, knownHardware(at, 60), nil, 1)
		if !g.blocked {
			t.Fatal("CPU recovery ignored thirty-second hysteresis")
		}
	}
	at = now.Add(76 * time.Second)
	g.observe(at, knownHardware(at, 60), nil, 1)
	if g.blocked {
		t.Fatal("healthy CPU never recovered")
	}
}
func TestResourceMemorySignalsAndMissingScope(t *testing.T) {
	for _, kind := range []string{"psi", "cgroup", "host", "event"} {
		t.Run(kind, func(t *testing.T) {
			g := newResourceGuard()
			now := time.Unix(100, 0)
			for second := 0; second <= 10; second++ {
				at := now.Add(time.Duration(second) * time.Second)
				s := knownHardware(at, 20)
				switch kind {
				case "psi":
					*s.MemoryFull = 2
				case "cgroup":
					*s.GroupMemoryRatio = .95
				case "host":
					available, total := uint64(32<<20), uint64(1<<30)
					s.HostAvailable = &available
					s.HostTotal = &total
				case "event":
					s.MemoryEvent = true
				}
				g.observe(at, s, nil, 1)
			}
			if !g.blocked {
				t.Fatal("memory pressure ignored", kind)
			}
			for second := 11; second < 50; second++ {
				at := now.Add(time.Duration(second) * time.Second)
				s := knownHardware(at, 10)
				s.MemorySome = nil
				s.MemoryFull = nil
				s.GroupMemoryRatio = nil
				s.MemoryEventsKnown = false
				g.observe(at, s, nil, 1)
			}
			if !g.blocked {
				t.Fatal("missing pressure source allowed recovery", kind)
			}
		})
	}
}
func TestResourceUnknownKeepsConservativeThroughputPolicy(t *testing.T) {
	a, b, jobs, now := automaticFixture()
	defer b.close()
	g := newResourceGuard()
	for i := 0; i < 20; i++ {
		at := now.Add(time.Second)
		g.observe(at, hardwareSnapshot{At: at, CPUState: "Unknown", MemoryState: "Unknown"}, jobs, 1)
		advanceAutomatic(a, b, jobs, &now, 1, 1000, nil)
	}
	if g.blocked || jobs[0].target != 2 {
		t.Fatal("missing hardware disabled measured adaptation within conservative budgets")
	}
}
func addStorageWindow(j *automaticJob, kind int, d time.Duration) {
	m := &transferMeasurements{}
	for i := 0; i < 20; i++ {
		m.recordStorage(kind, d)
	}
	added := m.storageSnapshot()
	for k := range j.metrics.Storage.Bins {
		for bin := range j.metrics.Storage.Bins[k] {
			j.metrics.Storage.Bins[k][bin] += added.Bins[k][bin]
		}
	}
}
func TestStorageReductionMustImproveTiming(t *testing.T) {
	for _, improved := range []bool{true, false} {
		t.Run(map[bool]string{true: "improved", false: "unhelpful"}[improved], func(t *testing.T) {
			_, b, jobs, now := automaticFixture()
			defer b.close()
			g := newResourceGuard()
			jobs[0].target = 3
			jobs[0].layout.Target = 3
			jobs[0].metrics.Storage.Destination = "device:data"
			jobs[0].metrics.Storage.Checkpoint = "device:metadata"
			g.observe(now, knownHardware(now, 20), jobs, 1)
			// Actual writes and checkpoint storage occupy independent histograms.
			for i := 1; i <= 20; i++ {
				at := now.Add(time.Duration(i) * time.Second)
				if i%10 == 0 {
					addStorageWindow(&jobs[0], storageCheckpointSync, 600*time.Millisecond)
				}
				g.observe(at, knownHardware(at, 20), jobs, 1)
			}
			if !g.blocked || g.storage[1].kind != storageCheckpointSync {
				t.Fatal("sustained checkpoint latency not distinguished")
			}
			at := now.Add(20 * time.Second)
			updates := g.adjust(at, jobs, 1)
			if len(updates) != 1 || updates[0].target != 2 {
				t.Fatal("missing storage reduction trial", updates)
			}
			jobs[0].target = 2
			jobs[0].layout.Target = 2
			duration := 600 * time.Millisecond
			if improved {
				duration = 100 * time.Millisecond
			}
			for i := 21; i <= 40; i++ {
				at = now.Add(time.Duration(i) * time.Second)
				if i%10 == 0 {
					addStorageWindow(&jobs[0], storageCheckpointSync, duration)
				}
				g.observe(at, knownHardware(at, 20), jobs, 1)
			}
			updates = g.adjust(at, jobs, 1)
			if improved && len(updates) > 0 {
				t.Fatal("useful storage reduction was reverted", updates)
			}
			if !improved && (len(updates) != 1 || updates[0].target != 3) {
				t.Fatal("unhelpful storage reduction was retained", updates)
			}
		})
	}
}
func TestBufferReservationsBoundAndCancel(t *testing.T) {
	b := newBufferBudget()
	b.maximumHash = transferBufferSize
	b.maximumTransfer = transferBufferSize
	first, _ := b.reserve()
	if first == nil {
		t.Fatal("no initial reservation")
	}
	if next, _ := b.reserve(); next != nil {
		t.Fatal("buffer ceiling exceeded")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := b.acquire(ctx); err == nil {
		t.Fatal("buffer waiter ignored cancellation")
	}
	first.release()
	first.release()
	next, err := b.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	next.release()
	if s := b.snapshot(); s.Transfer != 0 || s.Hash != 0 {
		t.Fatal("buffer accounting leaked", s)
	}
}

func TestStorageTrialsSerializeAndRecoverWithoutGenerationChange(t *testing.T) {
	_, broker, jobs, now := automaticFixture()
	defer broker.close()
	second := jobs[0]
	second.id = 2
	jobs = append(jobs, second)
	g := newResourceGuard()
	for i := range jobs {
		jobs[i].target, jobs[i].layout.Target = 3, 3
		jobs[i].metrics.Storage.Destination = "data"
	}
	g.observe(now, knownHardware(now, 20), jobs, 1)
	for sec := 1; sec <= 40; sec++ {
		at := now.Add(time.Duration(sec) * time.Second)
		if sec%10 == 0 {
			for i := range jobs {
				duration := 600 * time.Millisecond
				if sec > 20 && i == 0 {
					duration = 100 * time.Millisecond
				}
				addStorageWindow(&jobs[i], storageWrite, duration)
			}
		}
		g.observe(at, knownHardware(at, 20), jobs, 1)
		updates := g.adjust(at, jobs, 1)
		for _, update := range updates {
			if sec < 40 && (update.job != 1 || sec != 20 || update.target != 2) {
				t.Fatalf("overlapping trial at %d: %+v", sec, updates)
			}
			if update.job == 1 && update.target != 2 {
				t.Fatal("useful reduction reverted during recovery")
			}
			jobs[update.job-1].target = update.target
			jobs[update.job-1].layout.Target = update.target
		}
	}
	if g.storage[1].trial != nil || jobs[0].target != 2 {
		t.Fatal("successful trial not retained")
	}
}

func TestStorageRelativeBaselineAndMissingSamples(t *testing.T) {
	_, b, jobs, now := automaticFixture()
	defer b.close()
	g := newResourceGuard()
	jobs[0].metrics.Storage.Destination = "data"
	g.observe(now, knownHardware(now, 10), jobs, 1)
	for sec := 1; sec <= 30; sec++ {
		at := now.Add(time.Duration(sec) * time.Second)
		if sec%10 == 0 {
			d := 10 * time.Millisecond
			if sec > 10 {
				d = 100 * time.Millisecond
			}
			addStorageWindow(&jobs[0], storageWrite, d)
		}
		g.observe(at, knownHardware(at, 10), jobs, 1)
	}
	if !g.blocked {
		t.Fatal("relative regression below 500ms ignored")
	}
	for sec := 31; sec <= 60; sec++ {
		at := now.Add(time.Duration(sec) * time.Second)
		g.observe(at, knownHardware(at, 10), jobs, 1)
	}
	if !g.blocked || g.storage[1].view.State != "Unknown" {
		t.Fatal("absent writes were treated as healthy recovery")
	}
}

func TestHardwareMonitorRejectsStaleSamples(t *testing.T) {
	m := &hardwareMonitor{}
	now := time.Unix(100, 0)
	s := knownHardware(now, 0)
	m.latest.Store(&s)
	if m.snapshot(context.Background(), now).CPUPercent == nil {
		t.Fatal("measured zero lost")
	}
	for _, at := range []time.Time{now.Add(4 * time.Second), now.Add(-time.Second)} {
		if m.snapshot(context.Background(), at).CPUPercent != nil {
			t.Fatal("stale/future CPU invented")
		}
	}
}
