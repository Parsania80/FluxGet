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
	remaining := float64(n)
	last := time.Now()
	for remaining > 0 {
		rate := l.liveRate()
		if rate == 0 {
			return nil
		}
		delay := time.Duration(remaining / float64(rate) * float64(time.Second))
		if delay > 100*time.Millisecond {
			delay = 100 * time.Millisecond
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
		now := time.Now()
		remaining -= now.Sub(last).Seconds() * float64(rate)
		last = now
	}
	return nil
}
