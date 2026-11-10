package main

import (
	"context"
	"sync/atomic"
	"time"
)

// Optional measurements use nil for Unknown, never a fabricated healthy zero.
// These describe the engine process and its environment, not total desktop RSS.
type hardwareSnapshot struct {
	At                time.Time `json:"at"`
	Epoch             uint64    `json:"epoch"`
	CPUPercent        *float64  `json:"cpu_percent,omitempty"`
	CPUCapacity       *float64  `json:"cpu_capacity,omitempty"`
	ThrottlePercent   *float64  `json:"cpu_throttle_percent,omitempty"`
	MemorySome        *float64  `json:"memory_some_percent,omitempty"`
	MemoryFull        *float64  `json:"memory_full_percent,omitempty"`
	HostAvailable     *uint64   `json:"host_available_bytes,omitempty"`
	HostTotal         *uint64   `json:"host_total_bytes,omitempty"`
	GroupMemoryRatio  *float64  `json:"group_memory_ratio,omitempty"`
	MemoryEvent       bool      `json:"memory_event"`
	MemoryEventsKnown bool      `json:"memory_events_known"`
	RSS               *uint64   `json:"engine_rss_bytes,omitempty"`
	CPUState          string    `json:"cpu_state"`
	MemoryState       string    `json:"memory_state"`
}
type hardwareReader interface {
	sample(time.Time) hardwareSnapshot
}
type hardwareSourceKey struct{}
type hardwareMonitor struct {
	latest atomic.Pointer[hardwareSnapshot]
	stop   chan struct{}
	done   chan struct{}
}

func startHardwareMonitor() *hardwareMonitor {
	m := &hardwareMonitor{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(m.done)
		reader := newHardwareReader()
		timer := time.NewTicker(time.Second)
		defer timer.Stop()
		for {
			snapshot := reader.sample(time.Now())
			m.latest.Store(&snapshot)
			select {
			case <-m.stop:
				return
			case <-timer.C:
			}
		}
	}()
	return m
}
func (m *hardwareMonitor) close() { close(m.stop); <-m.done }
func (m *hardwareMonitor) snapshot(ctx context.Context, now time.Time) hardwareSnapshot {
	if source, ok := ctx.Value(hardwareSourceKey{}).(func(time.Time) hardwareSnapshot); ok {
		return source(now)
	}
	if s := m.latest.Load(); s != nil && !s.At.After(now) && now.Sub(s.At) <= 3500*time.Millisecond {
		return *s
	}
	return hardwareSnapshot{At: now, CPUState: "Unknown: observation unavailable or stale", MemoryState: "Unknown: observation unavailable or stale"}
}
