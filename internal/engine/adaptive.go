package engine

import "time"

// The queue owns one policy, so only one concurrency experiment runs at a time.
// Inputs contain complete, non-overlapping windows of real accepted bytes.
type adaptiveInput struct {
	At               time.Time
	Window           transferObservation
	Workers, Maximum int
	Eligible         bool
	SpeedLimited     bool
	Pressure         bool
	AffectedRate     float64
	HeaderDelay      float64
}
type adaptiveDecision struct {
	Target int
	Reason string
	Trial  bool
}
type adaptivePolicy struct {
	generation    uint64
	samples       int
	baseline      float64
	trial         bool
	previous      int
	trialAt       time.Time
	trialSamples  int
	trialRate     float64
	cooldown      time.Time
	affected      float64
	header        float64
	trialAffected float64
	trialHeader   float64
	firstRate     float64
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
		p.affected, p.header = 0, 0
		hold.Target = in.Maximum
		hold.Reason = "connection maximum reduced"
		return hold
	}

	if p.generation != in.Window.Generation {
		previous, trial := p.previous, p.trial
		*p = adaptivePolicy{generation: in.Window.Generation}
		if trial {
			hold.Target = min(in.Maximum, max(1, previous))
			hold.Reason = "conditions changed; trial discarded"
			p.cooldown = in.At.Add(30 * time.Second)
			return hold
		}
	}
	if in.Pressure {
		p.samples = 0
		p.baseline = 0
		p.affected, p.header = 0, 0
		p.trial = false
		p.cooldown = in.At.Add(30 * time.Second)
		hold.Target = max(1, in.Workers-1)
		hold.Reason = "resource pressure"
		return hold
	}
	if in.SpeedLimited {
		p.samples = 0
		p.baseline = 0
		p.affected, p.header = 0, 0
		if p.trial {
			hold.Target = max(1, p.previous)
		}
		p.trial = false
		hold.Reason = "speed limit reached"
		return hold
	}
	if !in.Window.Valid || in.Window.Seconds < 10 || in.Window.BytesPerSecond <= 0 {
		p.samples = 0
		p.baseline = 0
		p.affected, p.header = 0, 0
		if p.trial {
			hold.Target = max(1, p.previous)
			p.cooldown = in.At.Add(30 * time.Second)
		}
		p.trial = false
		hold.Reason = "measurements not stable"
		return hold
	}
	if !in.Eligible && !p.trial {
		p.samples, p.baseline, p.affected, p.header = 0, 0, 0, 0
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
		if p.trialSamples == 1 && (in.Window.BytesPerSecond > p.trialRate*1.5 || in.Window.BytesPerSecond < p.trialRate/1.5) {
			hold.Target = p.previous
			hold.Reason = "conditions changed; trial discarded"
			*p = adaptivePolicy{generation: in.Window.Generation, cooldown: in.At.Add(30 * time.Second)}
			return hold
		}
		p.trialSamples++
		p.trialRate += in.Window.BytesPerSecond
		p.trialAffected += in.AffectedRate
		p.trialHeader += in.HeaderDelay
		if p.trialSamples < 2 {
			hold.Reason = "measuring trial"
			return hold
		}
		p.trial = false
		p.samples = 0
		p.cooldown = in.At.Add(30 * time.Second)
		hold.Reason = "extra workers improved throughput"
		if p.trialRate/2 < p.baseline*1.10 || (p.affected > 0 && p.trialAffected/2 < p.affected*0.90) || (p.header > 0 && p.trialHeader/2 > p.header*2) {
			hold.Target = p.previous
			hold.Reason = "extra workers did not improve throughput"
			p.cooldown = in.At.Add(time.Minute)
		}
		p.baseline = 0
		p.affected, p.header = 0, 0
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
	if p.samples > 0 && (in.Window.BytesPerSecond > p.firstRate*1.5 || in.Window.BytesPerSecond < p.firstRate/1.5) {
		p.samples, p.baseline, p.affected, p.header = 0, 0, 0, 0
		hold.Reason = "measurements not stable"
	}
	p.firstRate = in.Window.BytesPerSecond
	p.samples++
	p.affected += in.AffectedRate
	p.header += in.HeaderDelay
	p.baseline += in.Window.BytesPerSecond
	if p.samples < 2 {
		return hold
	}
	p.baseline /= 2
	p.affected /= 2
	p.header /= 2
	p.previous = in.Workers
	p.trial = true
	p.trialAt = in.At
	p.trialSamples = 0
	p.trialRate = 0
	p.trialAffected, p.trialHeader = 0, 0
	hold.Target = in.Workers + 1
	hold.Trial = true
	hold.Reason = "try one additional worker"
	return hold
}
