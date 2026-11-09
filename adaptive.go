package main

import "time"

// Stage 1 evaluates this policy in deterministic simulations only. Applying its
// worker decisions awaits stage 3; stage 2 supplies safe live resizing.
type adaptiveInput struct {
	At               time.Time
	Window           transferObservation
	Workers, Maximum int
	Eligible         bool
	SpeedLimited     bool
	Pressure         bool
}
type adaptiveDecision struct {
	Target int
	Reason string
	Trial  bool
}
type adaptivePolicy struct {
	generation   uint64
	samples      int
	baseline     float64
	trial        bool
	previous     int
	trialAt      time.Time
	trialSamples int
	trialRate    float64
	cooldown     time.Time
}

func (p *adaptivePolicy) observe(in adaptiveInput) adaptiveDecision {
	hold := adaptiveDecision{Target: in.Workers, Reason: "collecting measurements"}
	if in.Maximum < 1 {
		hold.Target = 1
		hold.Reason = "invalid worker ceiling"
		return hold
	}
	if in.Workers > in.Maximum {
		p.trial = false
		p.samples = 0
		p.baseline = 0
		hold.Target = in.Maximum
		hold.Reason = "connection maximum reduced"
		return hold
	}

	if p.generation != in.Window.Generation {
		*p = adaptivePolicy{generation: in.Window.Generation}
	}
	if in.Pressure {
		p.samples = 0
		p.baseline = 0
		p.trial = false
		p.cooldown = in.At.Add(30 * time.Second)
		hold.Target = max(1, in.Workers-1)
		hold.Reason = "resource pressure"
		return hold
	}
	if in.SpeedLimited {
		p.samples = 0
		p.baseline = 0
		p.trial = false
		hold.Reason = "speed limit reached"
		return hold
	}
	if !in.Window.Valid || in.Window.Seconds < 10 || in.Window.BytesPerSecond <= 0 {
		p.samples = 0
		p.baseline = 0
		p.trial = false
		hold.Reason = "measurements not stable"
		return hold
	}
	if !in.Eligible || in.Maximum < 1 {
		p.trial = false
		hold.Reason = "no additional eligible parts"
		return hold
	}
	if p.trial {
		if in.Workers != p.previous+1 {
			hold.Target = min(in.Maximum, p.previous+1)
			hold.Trial = true
			hold.Reason = "trial awaiting worker change"
			return hold
		}
		if in.At.Before(p.trialAt.Add(5 * time.Second)) {
			hold.Reason = "trial settling"
			return hold
		}
		p.trialSamples++
		p.trialRate += in.Window.BytesPerSecond
		if p.trialSamples < 2 {
			hold.Reason = "measuring trial"
			return hold
		}
		p.trial = false
		p.samples = 0
		p.cooldown = in.At.Add(30 * time.Second)
		hold.Reason = "extra workers improved throughput"
		if p.trialRate/2 < p.baseline*1.10 {
			hold.Target = p.previous
			hold.Reason = "extra workers did not improve throughput"
		}
		p.baseline = 0
		return hold
	}
	if in.At.Before(p.cooldown) {
		hold.Reason = "waiting before another trial"
		return hold
	}
	if in.Workers >= in.Maximum {
		hold.Reason = "connection maximum reached"
		return hold
	}
	p.samples++
	p.baseline += in.Window.BytesPerSecond
	if p.samples < 2 {
		return hold
	}
	p.baseline /= 2
	p.previous = in.Workers
	p.trial = true
	p.trialAt = in.At
	p.trialSamples = 0
	p.trialRate = 0
	hold.Target = in.Workers + 1
	hold.Trial = true
	hold.Reason = "try one additional worker"
	return hold
}
