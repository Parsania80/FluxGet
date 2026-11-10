package main

import (
	"context"
	"encoding/json"
	"os"
	"time"
)

type liveSpeedConfig struct {
	Enabled   bool             `json:"enabled"`
	Default   int64            `json:"default_kib"`
	Overrides map[string]int64 `json:"overrides"`
	Overall   int64            `json:"overall_kib"`
}

type liveSpeedKey struct{}
type liveSpeedSource struct{ path, key string }

func (l *speedLimiter) liveRate() int64 {
	if time.Since(l.liveChecked) < 250*time.Millisecond {
		return l.bytesPerSecond
	}
	l.liveChecked = time.Now()
	data, err := os.ReadFile(l.live.path)
	if err != nil || len(data) > 1<<20 {
		return l.bytesPerSecond
	}
	var c liveSpeedConfig
	if json.Unmarshal(data, &c) != nil || c.Default < 0 || c.Default > 1<<30 {
		return l.bytesPerSecond
	}
	rate := int64(0)
	if c.Enabled {
		rate = c.Default
	}
	if l.live.key == "overall" {
		if c.Overall >= 0 && c.Overall <= 1<<30 {
			l.bytesPerSecond = c.Overall * 1024
		}
		return l.bytesPerSecond
	}
	if v, ok := c.Overrides[l.live.key]; ok && v > 0 && v <= 1<<30 && (rate == 0 || v < rate) {
		rate = v
	}
	l.bytesPerSecond = rate * 1024
	return l.bytesPerSecond
}

func (l *speedLimiter) waitLive(ctx context.Context, n int) error {
	if n == 0 {
		return nil
	}
	select {
	case l.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-l.gate }()
	// Keep the elapsed-time credit across reads, including timer oversleep and
	// time spent reading the network or another limiter. Otherwise thousands
	// of sub-millisecond sleeps impose an accidental ~1 MB/s ceiling.
	now := time.Now()
	if !l.last.IsZero() {
		l.credit += now.Sub(l.last).Seconds() * float64(l.bytesPerSecond)
	}
	l.last = now
	rate := l.liveRate()
	if rate == 0 {
		l.credit = 0
		return nil
	}
	capacity := min(32768, max(1, float64(rate)/10))
	l.credit = min(l.credit, capacity) - float64(n)
	for l.credit < 0 {
		delay := time.Duration(-l.credit / float64(rate) * float64(time.Second))
		delay = min(100*time.Millisecond, max(time.Nanosecond, delay))
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			// An interrupted read must not leave unpaid pacing debt that
			// stalls other downloads sharing this limiter after a pause.
			l.credit = 0
			l.last = time.Now()
			return ctx.Err()
		case <-timer.C:
		}
		now = time.Now()
		l.credit += now.Sub(l.last).Seconds() * float64(rate)
		l.last = now
		rate = l.liveRate()
		if rate == 0 {
			l.credit = 0
			return nil
		}
		capacity = min(32768, max(1, float64(rate)/10))
		l.credit = min(l.credit, capacity)
	}
	return nil
}

// Read at most a tenth of a second of the current allowance (capped at 32 KiB).
// Low limits still publish progress promptly; high/unlimited rates avoid 1 KiB
// read overhead. Polling uses the same gate as pacing so live rate fields are
// never read or modified concurrently by different range workers.
func (l *speedLimiter) liveReadSize(ctx context.Context) (int64, error) {
	select {
	case l.gate <- struct{}{}:
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	defer func() { <-l.gate }()
	rate := l.liveRate()
	if rate == 0 {
		return 32768, nil
	}
	return min(32768, max(1, rate/10)), nil
}
