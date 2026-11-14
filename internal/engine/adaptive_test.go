package engine

import (
	"testing"
	"time"
)

func TestAdaptivePolicyTrialsWithFakeClock(t *testing.T) {
	for _, gain := range []bool{true, false} {
		t.Run(map[bool]string{true: "gain", false: "no gain"}[gain], func(t *testing.T) {
			var p adaptivePolicy
			now := time.Unix(100, 0)
			in := adaptiveInput{At: now, Workers: 1, Maximum: 4, Eligible: true, Window: transferObservation{Valid: true, Seconds: 10, BytesPerSecond: 100, Generation: 1}}
			if d := p.observe(in); d.Target != 1 || d.Trial {
				t.Fatal("grew before baseline")
			}
			in.At = in.At.Add(10 * time.Second)
			if d := p.observe(in); d.Target != 2 || !d.Trial {
				t.Fatalf("no trial: %+v", d)
			}
			in.Workers = 2
			in.At = in.At.Add(10 * time.Second)
			if gain {
				in.Window.BytesPerSecond = 150
			} else {
				in.Window.BytesPerSecond = 101
			}
			p.observe(in)
			in.At = in.At.Add(10 * time.Second)
			d := p.observe(in)
			if gain && d.Target != 2 || !gain && d.Target != 1 {
				t.Fatalf("wrong trial result: %+v", d)
			}
			in.Workers = d.Target
			in.At = in.At.Add(time.Second)
			if d := p.observe(in); d.Trial {
				t.Fatal("ignored cooldown")
			}
		})
	}
}
func TestAdaptivePolicyHonorsLimitsAndDisruption(t *testing.T) {
	var p adaptivePolicy
	in := adaptiveInput{At: time.Unix(100, 0), Workers: 2, Maximum: 4, Eligible: true, SpeedLimited: true, Window: transferObservation{Valid: true, Seconds: 10, BytesPerSecond: 100, Generation: 1}}
	if d := p.observe(in); d.Target != 2 || d.Reason != "speed limit reached" {
		t.Fatal("tried to defeat user speed limit")
	}
	in.SpeedLimited = false
	in.Pressure = true
	if d := p.observe(in); d.Target != 1 {
		t.Fatal("ignored pressure")
	}
	in.Pressure = false
	in.Window.Valid = false
	if d := p.observe(in); d.Trial {
		t.Fatal("experimented with invalid data")
	}
	in.Window.Valid = true
	in.Window.Generation++
	in.Workers = 4
	if d := p.observe(in); d.Target != 4 || d.Trial {
		t.Fatal("exceeded user ceiling")
	}
}
