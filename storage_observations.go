package main

import (
	"context"
	"os"
	"sync/atomic"
	"time"
)

const storageMetricCount = 5
const (
	storageWrite = iota
	storageDataSync
	storageCheckpointWrite
	storageCheckpointSync
	storageCheckpointPublish
)

var storageBounds = [...]time.Duration{time.Millisecond, 5 * time.Millisecond, 10 * time.Millisecond, 25 * time.Millisecond, 50 * time.Millisecond, 100 * time.Millisecond, 250 * time.Millisecond, 500 * time.Millisecond, 750 * time.Millisecond, time.Second, 2 * time.Second, 5 * time.Second, 10 * time.Second, time.Duration(1<<63 - 1)}

type latencyHistogram struct {
	counts [len(storageBounds)]atomic.Uint64
}
type storageCounters struct {
	Bins                    [storageMetricCount][len(storageBounds)]uint64
	Destination, Checkpoint string
}

func (m *transferMeasurements) recordStorage(kind int, d time.Duration) {
	if m == nil {
		return
	}
	for i, bound := range storageBounds {
		if d <= bound {
			m.storage[kind].counts[i].Add(1)
			return
		}
	}
}
func (m *transferMeasurements) recordFilesystem(file *os.File, checkpoint bool) {
	if m == nil || file == nil {
		return
	}
	target := &m.destinationFS
	if checkpoint {
		target = &m.checkpointFS
	}
	if target.Load() != nil {
		return
	}
	if key := filesystemKey(file); key != "" {
		target.CompareAndSwap(nil, &key)
	}
}
func (m *transferMeasurements) storageSnapshot() storageCounters {
	var s storageCounters
	for kind := range s.Bins {
		for bin := range s.Bins[kind] {
			s.Bins[kind][bin] = m.storage[kind].counts[bin].Load()
		}
	}
	if key := m.destinationFS.Load(); key != nil {
		s.Destination = *key
	}
	if key := m.checkpointFS.Load(); key != nil {
		s.Checkpoint = *key
	}
	return s
}

// A caller that accelerates observation time can supply storage observations
// from the same clock domain. Normal queues always use the measured counters.
type storageSourceKey struct{}

func storageObservations(ctx context.Context, measured storageCounters) storageCounters {
	if source, ok := ctx.Value(storageSourceKey{}).(func(storageCounters) storageCounters); ok {
		return source(measured)
	}
	return measured
}

func storageP95(current, previous storageCounters, kind int) (float64, bool) {
	count := uint64(0)
	for i := range storageBounds {
		if current.Bins[kind][i] < previous.Bins[kind][i] {
			return 0, false
		}
		count += current.Bins[kind][i] - previous.Bins[kind][i]
	}
	if count == 0 {
		return 0, false
	}
	rank := count - count/20
	seen := uint64(0)
	for i, bound := range storageBounds {
		seen += current.Bins[kind][i] - previous.Bins[kind][i]
		if seen >= rank {
			return min(bound, 60*time.Second).Seconds(), true
		}
	}
	return 0, false
}
