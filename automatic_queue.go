package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"
)

// All fields belong to the queue owner; none are persisted in queue/checkpoints.
type automaticController struct {
	policy          adaptivePolicy
	sampler         observationSampler
	generation      uint64
	files           int
	candidate       int // zero means a file admission trial
	trial           bool
	settling        bool
	fileJoined      bool
	settleAt        time.Time
	previous        map[int]measurementSnapshot
	reason          string
	status          string
	limits          liveSpeedConfig
	limitsKnown     bool
	staticOverall   int64
	resourceBlocked bool
	resourceReason  string
}

type automaticJob struct {
	id              int
	origin          string
	layout          segmentSnapshot
	metrics         measurementSnapshot
	target, maximum int
	requests        jobBudgetSnapshot
	stopping        bool
}

type automaticUpdate struct {
	job, target int
}

func newAutomaticController() *automaticController {
	return &automaticController{files: 1, previous: map[int]measurementSnapshot{}, reason: "Collecting download measurements", status: "Learning", limitsKnown: true}
}

func queueOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	origin, _ := requestOrigin(u)
	return origin
}

func (a *automaticController) resetWindow(now time.Time, jobs []automaticJob, generation uint64) {
	a.sampler = observationSampler{}
	a.sampler.sample(now, aggregateMeasurements(jobs), generation)
	a.previous = map[int]measurementSnapshot{}
	for _, j := range jobs {
		a.previous[j.id] = j.metrics
	}
}

func aggregateMeasurements(jobs []automaticJob) measurementSnapshot {
	var m measurementSnapshot
	for _, j := range jobs {
		s := j.metrics
		m.Accepted += s.Accepted
		m.Retrying += s.Retrying
		m.Failures += s.Failures
		m.HTTPErrors += s.HTTPErrors
		m.RetryWait += s.RetryWait
		m.WorkerChanges += s.WorkerChanges
	}
	return m
}

// An admission caused by our own file trial changes the sampling membership,
// not the baseline being tested. Other membership/configuration changes abort it.
func (a *automaticController) admitted(generation uint64) {
	if a.trial && a.candidate == 0 && !a.fileJoined {
		a.fileJoined = true
		a.generation = generation
		a.policy.generation = generation
		a.settling = true
		a.settleAt = time.Time{}
		a.sampler = observationSampler{}
	}
}

func (a *automaticController) discard(generation uint64, now time.Time) *automaticUpdate {
	var update *automaticUpdate
	if a.trial {
		if a.candidate == 0 {
			a.files = max(1, a.policy.previous)
		} else {
			update = &automaticUpdate{a.candidate, max(1, a.policy.previous)}
		}
	}
	a.policy = adaptivePolicy{generation: generation, cooldown: now.Add(30 * time.Second)}
	a.generation, a.trial, a.settling = generation, false, false
	a.fileJoined = false
	a.sampler = observationSampler{}
	a.previous = map[int]measurementSnapshot{}
	a.reason, a.status = "Conditions changed; collecting fresh measurements", "Learning"
	return update
}

func (a *automaticController) readLimits(ctx context.Context) bool {
	if limiter, _ := ctx.Value(limiterContextKey{}).(*speedLimiter); limiter != nil && limiter.live == nil {
		a.staticOverall = limiter.bytesPerSecond
	}
	path, _ := ctx.Value(liveSpeedKey{}).(string)
	if path == "" {
		return false
	}
	file, err := os.Open(path)
	var data []byte
	if err == nil {
		data, err = io.ReadAll(io.LimitReader(file, (1<<20)+1))
		file.Close()
	}
	var c liveSpeedConfig
	known := err == nil && len(data) <= 1<<20 && json.Unmarshal(data, &c) == nil && c.Default >= 0 && c.Default <= 1<<30 && c.Overall >= 0 && c.Overall <= 1<<30
	if known {
		for _, v := range c.Overrides {
			if v < 0 || v > 1<<30 {
				known = false
				break
			}
		}
	}
	old, _ := json.Marshal(a.limits)
	next, _ := json.Marshal(c)
	changed := a.limitsKnown != known || (known && string(old) != string(next))
	a.limitsKnown = known
	if known {
		a.limits = c
	}
	return changed
}

func (a *automaticController) fileLimit(id int) int64 {
	rate := int64(0)
	if a.limits.Enabled {
		rate = a.limits.Default
	}
	if v := a.limits.Overrides[fmt.Sprintf("queue:%d", id)]; v > 0 && (rate == 0 || v < rate) {
		rate = v
	}
	return rate * 1024
}

func limitReached(rate float64, ceiling int64, waits int64) bool {
	return ceiling > 0 && waits > int64(100*time.Millisecond) && rate >= float64(ceiling)*0.95
}

func workerEligible(j automaticJob) bool {
	if j.stopping || j.layout.Mode != "parallel" || j.target >= j.maximum || j.target >= len(j.layout.Parts) {
		return false
	}
	var remaining int64
	available := 0
	for _, p := range j.layout.Parts {
		if p.End < p.Start || p.State == "retiring" || p.State == "verifying" {
			return false
		}
		if p.Downloaded < p.End-p.Start {
			available++
			remaining += p.End - p.Start - p.Downloaded
		}
	}
	return remaining >= 16<<20 && available > j.target
}

// tick only returns a target; safe checkpoint retirement remains in the transfer
// owner. Sampling is once per second, with non-overlapping ten-second windows.
func (a *automaticController) tick(now time.Time, generation uint64, maximum int, jobs []automaticJob, queued []queueJob, budget *requestBudget) *automaticUpdate {
	a.files = min(maximum, max(1, a.files))
	if a.generation != generation {
		initial := a.generation == 0
		update := a.discard(generation, now)
		// The first session needs observations, not a cooldown.
		if initial {
			a.policy.cooldown = time.Time{}
		}
		a.resetWindow(now, jobs, generation)
		return update
	}
	if a.resourceBlocked {
		a.status = "Backing off"
		a.reason = a.resourceReason
		a.resetWindow(now, jobs, generation)
		return nil
	}
	if len(jobs) == 0 {
		a.files = 1
		a.reason = "Waiting for an eligible download"
		a.status = "Learning"
		return nil
	}
	// Expected worker/admission transitions cannot be compared as throughput.
	if a.settling {
		ready := true
		for _, j := range jobs {
			if j.stopping || j.metrics.Retrying > 0 || j.requests.Active == 0 {
				ready = false
			}
			if a.candidate == j.id && j.layout.Target != j.target {
				ready = false
			}
			for _, p := range j.layout.Parts {
				if p.State == "retiring" || p.State == "connecting" || p.State == "verifying" {
					ready = false
				}
			}
		}
		if !ready {
			a.settleAt = time.Time{}
		} else if a.settleAt.IsZero() {
			a.settleAt = now
		}
		a.reason = "Letting the trial settle"
		a.status = "Learning"
		a.resetWindow(now, jobs, generation)
		if ready && !now.Before(a.settleAt.Add(5*time.Second)) {
			a.settling = false
		}
		if now.Sub(a.policy.trialAt) > 45*time.Second && !ready {
			return a.discard(generation, now)
		}
		return nil
	}
	observation, ok := a.sampler.sample(now, aggregateMeasurements(jobs), generation)
	if !ok {
		return nil
	}
	rates := map[int]float64{}
	limited := map[int]bool{}
	var limiterWait int64
	for _, j := range jobs {
		before := a.previous[j.id]
		rates[j.id] = float64(j.metrics.Accepted-before.Accepted) / observation.Seconds
		wait := j.metrics.LimiterWait - before.LimiterWait
		limited[j.id] = limitReached(rates[j.id], a.fileLimit(j.id), wait)
		limiterWait += wait
	}
	defer func() {
		for _, j := range jobs {
			a.previous[j.id] = j.metrics
		}
	}()
	overall := a.limits.Overall * 1024
	if a.staticOverall > 0 && (overall == 0 || a.staticOverall < overall) {
		overall = a.staticOverall
	}
	aggregateLimited := limitReached(observation.BytesPerSecond, overall, limiterWait)
	if !a.limitsKnown {
		observation.Valid = false
		observation.Reason = "speed settings unavailable"
	}
	current, ceiling, eligible := a.files, maximum, false
	affected, header := float64(0), float64(0)
	if !a.trial {
		previousCandidate := a.candidate
		a.candidate = 0
		if len(jobs) < maximum && a.files == len(jobs) && !aggregateLimited {
			for _, q := range queued {
				if q.State == "queued" && budget.canStart(queueOrigin(q.URL), now) {
					eligible = true
					break
				}
			}
		}
		if !eligible && !aggregateLimited {
			bestTarget := 17
			for _, j := range jobs {
				if workerEligible(j) && !limited[j.id] && budget.canStart(j.origin, now) {
					// Existing files get a useful request opportunity before extra workers.
					served := true
					for _, other := range jobs {
						if other.metrics.Accepted == 0 || other.requests.Active == 0 {
							served = false
						}
					}
					if served && j.target < bestTarget {
						a.candidate = j.id
						bestTarget = j.target
						eligible = true
					}
				}
			}
		}
		if previousCandidate != a.candidate {
			a.policy.samples = 0
			a.policy.baseline = 0
			a.policy.affected = 0
			a.policy.header = 0
		}
	}
	for _, j := range jobs {
		if j.id == a.candidate {
			current, ceiling = j.target, min(j.maximum, len(j.layout.Parts))
			affected = rates[j.id]
			if n := j.metrics.Requests - a.previous[j.id].Requests; n > 0 {
				header = float64(j.metrics.HeaderWait-a.previous[j.id].HeaderWait) / float64(n)
			}
			if limited[j.id] {
				aggregateLimited = true
			}
		}
	}
	decision := a.policy.observe(adaptiveInput{At: now, Window: observation, Workers: current, Maximum: ceiling, Eligible: eligible, SpeedLimited: aggregateLimited, AffectedRate: affected, HeaderDelay: header})
	a.reason = decision.Reason
	a.status = "Learning"
	if !observation.Valid {
		a.reason = "Measurements interrupted; collecting a fresh baseline"
	}
	if aggregateLimited {
		a.status = "Limited by your settings"
		a.reason = "Your speed limit is reached"
	}
	if decision.Reason == "connection maximum reached" {
		a.status = "Limited by your settings"
	}
	if decision.Reason == "extra workers improved throughput" || decision.Reason == "waiting before another trial" || decision.Reason == "no additional eligible parts" {
		a.status = "Stable"
	}
	if a.candidate == 0 {
		switch decision.Reason {
		case "try one additional worker":
			a.reason = "Trying one additional download"
		case "extra workers improved throughput":
			a.reason = "Additional download improved total speed"
		case "extra workers did not improve throughput":
			a.reason = "Additional download did not improve total speed; letting active files finish"
		case "no additional eligible parts":
			a.reason = "No useful additional work within the current limits"
		}
	}
	a.trial = a.policy.trial
	if decision.Target == current {
		return nil
	}
	a.settling = a.trial
	if a.trial && a.candidate == 0 {
		a.fileJoined = false
	}
	a.settleAt = time.Time{}
	a.resetWindow(now, jobs, generation)
	if a.candidate == 0 {
		a.files = min(maximum, max(1, decision.Target))
		return nil
	}
	return &automaticUpdate{a.candidate, decision.Target}
}

// A server's retry sleep must not occupy every useful file opportunity. Borrow
// bounded session slots for eligible work while those sessions are dormant;
// already-started files drain when the origin recovers, without preemption.
func (a *automaticController) admissionMaximum(maximum int, jobs []automaticJob, budget *requestBudget, now time.Time) int {
	dormant := 0
	for _, j := range jobs {
		if !j.stopping && (j.metrics.Retrying > 0 || budget.originCooling(j.origin, now)) {
			dormant++
		}
	}
	return min(maximum, max(1, a.files)+dormant)
}
