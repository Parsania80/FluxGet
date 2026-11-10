package main

import "time"

type pressureLatch struct {
	highSince, lowSince time.Time
	active              bool
	needsThrottle       bool
}

func (p *pressureLatch) observe(now time.Time, high, low, urgent bool) {
	if urgent {
		p.active = true
		p.highSince = now
		p.lowSince = time.Time{}
		return
	}
	if high {
		p.lowSince = time.Time{}
		if p.highSince.IsZero() {
			p.highSince = now
		}
		if now.Sub(p.highSince) >= 10*time.Second {
			p.active = true
		}
	} else {
		p.highSince = time.Time{}
		if low {
			if p.lowSince.IsZero() {
				p.lowSince = now
			}
			if now.Sub(p.lowSince) >= 30*time.Second {
				p.active = false
				p.needsThrottle = false
			}
		} else {
			p.lowSince = time.Time{}
		}
	}
}

type storageView struct {
	Destination string                       `json:"destination_filesystem"`
	Checkpoint  string                       `json:"checkpoint_filesystem"`
	P95         [storageMetricCount]*float64 `json:"p95_seconds"`
	State       string                       `json:"state"`
	Reason      string                       `json:"reason"`
}
type storageTrial struct {
	previous, kind int
	baseline       float64
	at             time.Time
	samples        int
	sum            float64
	generation     uint64
}
type storageWatch struct {
	at        time.Time
	previous  measurementSnapshot
	baseline  [storageMetricCount]float64
	high, low int
	blocked   bool
	score     float64
	kind      int
	trial     *storageTrial
	cooldown  time.Time
	view      storageView
}
type resourceGuard struct {
	memoryRequired uint8
	cpu, memory    pressureLatch
	at             time.Time
	epoch          uint64
	hardware       hardwareSnapshot
	storage        map[int]*storageWatch
	blocked        bool
	reason         string
	reducedAt      time.Time
	generation     uint64
}

func newResourceGuard() *resourceGuard {
	return &resourceGuard{storage: map[int]*storageWatch{}, hardware: hardwareSnapshot{CPUState: "Unknown: collecting observations", MemoryState: "Unknown: collecting observations"}}
}
func (g *resourceGuard) observe(now time.Time, s hardwareSnapshot, jobs []automaticJob, generation uint64) bool {
	changed := false
	if s.Epoch != 0 && s.Epoch != g.epoch {
		changed = g.epoch != 0
		g.epoch = s.Epoch
		g.cpu.highSince, g.cpu.lowSince = time.Time{}, time.Time{}
		g.memory.highSince, g.memory.lowSince = time.Time{}, time.Time{}
	}
	if !g.at.IsZero() && now.Sub(g.at) > 3500*time.Millisecond {
		g.cpu.highSince, g.cpu.lowSince = time.Time{}, time.Time{}
		g.memory.highSince, g.memory.lowSince = time.Time{}, time.Time{}
	}
	// A cached sample may be displayed, but cannot advance a sustained streak.
	if s.At.After(g.at) {
		cpuHigh := s.CPUPercent != nil && *s.CPUPercent > 85
		if s.ThrottlePercent != nil && *s.ThrottlePercent >= 10 {
			cpuHigh = true
			g.cpu.needsThrottle = true
		}
		cpuLow := s.CPUPercent != nil && *s.CPUPercent < 70 && (!g.cpu.needsThrottle || s.ThrottlePercent != nil) && (s.ThrottlePercent == nil || *s.ThrottlePercent < 5)
		g.cpu.observe(now, cpuHigh, cpuLow, false)
		if !g.cpu.active && g.cpu.highSince.IsZero() {
			g.cpu.needsThrottle = false
		}
		memoryKnown := false
		var knownMask uint8
		memoryHigh, memoryLow := false, true
		if s.MemorySome != nil {
			knownMask |= 1
			if *s.MemorySome >= 10 {
				g.memoryRequired |= 1
			}
			memoryKnown = true
			memoryHigh = memoryHigh || *s.MemorySome >= 10
			memoryLow = memoryLow && *s.MemorySome < 5
		}
		if s.MemoryFull != nil {
			knownMask |= 2
			if *s.MemoryFull >= 1 {
				g.memoryRequired |= 2
			}
			memoryKnown = true
			memoryHigh = memoryHigh || *s.MemoryFull >= 1
			memoryLow = memoryLow && *s.MemoryFull < 0.5
		}
		if s.GroupMemoryRatio != nil {
			knownMask |= 4
			if *s.GroupMemoryRatio >= .90 {
				g.memoryRequired |= 4
			}
			memoryKnown = true
			memoryHigh = memoryHigh || *s.GroupMemoryRatio >= 0.90
			memoryLow = memoryLow && *s.GroupMemoryRatio < 0.80
		}
		if s.HostAvailable != nil && s.HostTotal != nil {
			knownMask |= 8
			if *s.HostAvailable < max(64<<20, *s.HostTotal/20) {
				g.memoryRequired |= 8
			}
			memoryKnown = true
			memoryHigh = memoryHigh || *s.HostAvailable < max(64<<20, *s.HostTotal/20)
			memoryLow = memoryLow && *s.HostAvailable > max(128<<20, *s.HostTotal/10)
		}
		if s.MemoryEventsKnown {
			knownMask |= 16
		}
		if s.MemoryEvent {
			g.memoryRequired |= 16
		}
		g.memory.observe(now, memoryHigh, memoryKnown && memoryLow && !s.MemoryEvent && (knownMask&g.memoryRequired) == g.memoryRequired, s.MemoryEvent)
		if !g.memory.active && g.memory.highSince.IsZero() {
			g.memoryRequired = 0
		}
		g.at = s.At
	}
	g.hardware = s
	present := map[int]bool{}
	for _, j := range jobs {
		present[j.id] = true
		w := g.storage[j.id]
		if w == nil {
			w = &storageWatch{}
			g.storage[j.id] = w
		}
		if g.generation != generation {
			w.at = time.Time{}
			w.high, w.low = 0, 0
			w.baseline = [storageMetricCount]float64{}
		}
		w.observe(now, j)
	}
	for id := range g.storage {
		if !present[id] {
			delete(g.storage, id)
		}
	}
	g.generation = generation
	blocked := g.cpu.active || g.memory.active
	reason := ""
	if g.cpu.active {
		reason = "CPU pressure is sustained; holding new work"
	}
	if g.memory.active {
		reason = "Memory pressure is sustained; holding new work"
	}
	for _, j := range jobs {
		if w := g.storage[j.id]; w != nil && w.blocked {
			blocked = true
			if reason == "" {
				reason = w.view.Reason
			}
		}
	}
	if blocked != g.blocked {
		changed = true
	}
	g.blocked, g.reason = blocked, reason
	return changed
}
func (w *storageWatch) observe(now time.Time, j automaticJob) {
	if w.at.IsZero() || !now.After(w.at) {
		w.at = now
		w.previous = j.metrics
		w.view = storageView{Destination: j.metrics.Storage.Destination, Checkpoint: j.metrics.Storage.Checkpoint, State: "Unknown", Reason: "Waiting for storage measurements"}
		return
	}
	if now.Sub(w.at) < 10*time.Second {
		return
	}
	before := w.previous
	elapsed := now.Sub(w.at)
	w.at = now
	w.previous = j.metrics
	current := j.metrics.Storage
	w.view = storageView{Destination: current.Destination, Checkpoint: current.Checkpoint, State: "Unknown", Reason: "Waiting for storage measurements"}
	valid := elapsed <= 15*time.Second && j.metrics.Retrying == 0 && j.metrics.RetryWait == before.RetryWait && j.metrics.Failures == before.Failures && j.metrics.HTTPErrors == before.HTTPErrors && j.metrics.WorkerChanges == before.WorkerChanges
	if !valid {
		w.high, w.low = 0, 0
		return
	}
	high := false
	known := false
	w.score = 0
	for kind := 0; kind < storageMetricCount; kind++ {
		fs, oldFS := current.Destination, before.Storage.Destination
		if kind >= storageCheckpointWrite {
			fs, oldFS = current.Checkpoint, before.Storage.Checkpoint
		}
		if fs == "" || fs != oldFS {
			continue
		}
		p95, ok := storageP95(current, before.Storage, kind)
		if !ok {
			continue
		}
		known = true
		w.view.P95[kind] = &p95
		slow := p95 > 0.5 || (w.baseline[kind] > 0 && p95 > w.baseline[kind]*2)
		if slow && p95 >= w.score {
			w.kind, w.score = kind, p95
			high = true
		}
		if !slow && w.trial == nil {
			if w.baseline[kind] == 0 {
				w.baseline[kind] = p95
			} else {
				w.baseline[kind] = w.baseline[kind]*.8 + p95*.2
			}
		}
	}
	if !known {
		w.high, w.low = 0, 0
		return
	}
	w.view.State = "Measured"
	w.view.Reason = "Storage timings measured"
	if high {
		w.high++
		w.low = 0
		if w.high >= 2 {
			w.blocked = true
		}
	} else {
		w.high = 0
		w.low++
		if w.low >= 2 {
			w.blocked = false
		}
	}
	if w.blocked {
		w.view.State = "Observing pressure"
		w.view.Reason = "Destination writes or syncs are taking longer; holding new work"
		if w.kind >= storageCheckpointWrite {
			w.view.Reason = "Checkpoint storage is taking longer; holding new work"
		}
	}
	// Count only full valid windows after the worker change. Never treat a
	// retirement/checkpoint boundary as evidence that a reduction helped.
	if trial := w.trial; trial != nil && now.Sub(trial.at) >= 5*time.Second {
		if p95 := w.view.P95[trial.kind]; p95 != nil {
			trial.samples++
			trial.sum += *p95
		}
	}
}
func (g *resourceGuard) adjust(now time.Time, jobs []automaticJob, generation uint64) []automaticUpdate {
	updates := []automaticUpdate{}
	trialPending := false
	for _, j := range jobs {
		w := g.storage[j.id]
		if w == nil || w.trial == nil {
			continue
		}
		trial := w.trial
		interrupted := trial.generation != generation || j.stopping || g.cpu.active || g.memory.active
		expired := now.Sub(trial.at) > 60*time.Second
		if !interrupted && !expired && trial.samples < 2 {
			trialPending = true
			continue
		}
		improved := !interrupted && trial.samples >= 2 && trial.sum/float64(trial.samples) <= trial.baseline*.8
		if !improved && !j.stopping && !g.cpu.active && !g.memory.active && j.target == max(1, trial.previous-1) {
			updates = append(updates, automaticUpdate{j.id, min(j.maximum, trial.previous)})
		}
		w.trial = nil
		w.cooldown = now.Add(time.Minute)
		if improved {
			w.view.Reason = "Fewer connections improved storage timing"
		} else {
			w.view.Reason = "Fewer connections did not show a storage improvement"
		}
	}
	if len(updates) > 0 {
		return updates
	}
	if trialPending {
		return updates
	}
	if !g.blocked || (!g.reducedAt.IsZero() && now.Sub(g.reducedAt) < 10*time.Second) {
		return updates
	}
	// One reduction across the queue at a time. Sequential responses drain.
	for _, j := range jobs {
		if j.target <= 1 || j.stopping || j.layout.Mode != "parallel" {
			continue
		}
		w := g.storage[j.id]
		if g.cpu.active || g.memory.active {
			updates = append(updates, automaticUpdate{j.id, j.target - 1})
			g.reducedAt = now
			return updates
		}
		if w != nil && w.blocked && w.trial == nil && !now.Before(w.cooldown) && w.score > 0 {
			w.trial = &storageTrial{previous: j.target, kind: w.kind, baseline: w.score, at: now, generation: generation}
			updates = append(updates, automaticUpdate{j.id, j.target - 1})
			g.reducedAt = now
			return updates
		}
	}
	return updates
}
