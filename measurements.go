package main

import (
	"context"
	"io"
	"os"
	"sync/atomic"
	"time"
)

type measurementKind int

const (
	metricBudgetWait measurementKind = iota
	metricHeaderWait
	metricNetworkRead
	metricLimiterWait
	metricWrite
	metricSync
	metricCheckpoint
	metricVerification
	metricRetryWait
	metricCount
)

// Counters are per transfer session. They contain no URLs and are never saved
// as recovery metadata. Duration sums across workers may exceed wall time.
type transferMeasurements struct {
	retrying     atomic.Int64
	accepted     atomic.Int64
	networkBytes atomic.Int64
	requests     atomic.Int64
	failures     atomic.Int64
	httpErrors   atomic.Int64
	durations    [metricCount]atomic.Int64
}
type measurementSnapshot struct {
	Retrying     int64 `json:"retrying"`
	Accepted     int64 `json:"accepted_bytes"`
	NetworkBytes int64 `json:"network_bytes"`
	Requests     int64 `json:"requests"`
	Failures     int64 `json:"request_failures"`
	HTTPErrors   int64 `json:"http_errors"`
	BudgetWait   int64 `json:"budget_wait_ns"`
	HeaderWait   int64 `json:"header_wait_ns"`
	NetworkRead  int64 `json:"network_read_ns"`
	LimiterWait  int64 `json:"limiter_wait_ns"`
	Write        int64 `json:"write_ns"`
	Sync         int64 `json:"sync_ns"`
	Checkpoint   int64 `json:"checkpoint_ns"`
	Verification int64 `json:"verification_ns"`
	RetryWait    int64 `json:"retry_wait_ns"`
}

func (m *transferMeasurements) addDuration(k measurementKind, d time.Duration) {
	if m != nil {
		m.durations[k].Add(int64(d))
	}
}
func (m *transferMeasurements) snapshot() measurementSnapshot {
	if m == nil {
		return measurementSnapshot{}
	}
	return measurementSnapshot{Retrying: m.retrying.Load(), Accepted: m.accepted.Load(), NetworkBytes: m.networkBytes.Load(), Requests: m.requests.Load(), Failures: m.failures.Load(), HTTPErrors: m.httpErrors.Load(), BudgetWait: m.durations[metricBudgetWait].Load(), HeaderWait: m.durations[metricHeaderWait].Load(), NetworkRead: m.durations[metricNetworkRead].Load(), LimiterWait: m.durations[metricLimiterWait].Load(), Write: m.durations[metricWrite].Load(), Sync: m.durations[metricSync].Load(), Checkpoint: m.durations[metricCheckpoint].Load(), Verification: m.durations[metricVerification].Load(), RetryWait: m.durations[metricRetryWait].Load()}
}

type measurementContextKey struct{}

func measurementsFrom(ctx context.Context) *transferMeasurements {
	m, _ := ctx.Value(measurementContextKey{}).(*transferMeasurements)
	return m
}

type measuredFileWriter struct {
	out     io.Writer
	metrics *transferMeasurements
}

func (w measuredFileWriter) Write(p []byte) (int, error) {
	start := time.Now()
	n, err := w.out.Write(p)
	if w.metrics != nil {
		w.metrics.accepted.Add(int64(n))
		w.metrics.addDuration(metricWrite, time.Since(start))
	}
	return n, err
}
func syncTransferFile(ctx context.Context, file *os.File) error {
	start := time.Now()
	err := file.Sync()
	measurementsFrom(ctx).addDuration(metricSync, time.Since(start))
	return err
}
func saveTransferCheckpoint(ctx context.Context, path string, file *os.File, c checkpoint) error {
	return saveCheckpointMeasured(path, file, c, nil, measurementsFrom(ctx))
}

// Sampling uses monotonic caller times, so tests can advance a fake clock. The
// queue owns this sampler; worker threads only mutate atomic counters above.
type observationSampler struct {
	at         time.Time
	baseline   measurementSnapshot
	generation uint64
}
type transferObservation struct {
	Seconds        float64 `json:"seconds"`
	Accepted       int64   `json:"accepted_bytes"`
	BytesPerSecond float64 `json:"bytes_per_second"`
	Valid          bool    `json:"valid"`
	Reason         string  `json:"reason"`
	Generation     uint64  `json:"generation"`
}

func (s *observationSampler) sample(now time.Time, current measurementSnapshot, generation uint64) (transferObservation, bool) {
	if s.at.IsZero() || s.generation != generation || current.Accepted < s.baseline.Accepted || !now.After(s.at) {
		s.at = now
		s.baseline = current
		s.generation = generation
		return transferObservation{}, false
	}
	elapsed := now.Sub(s.at)
	if elapsed < 10*time.Second {
		return transferObservation{}, false
	}
	o := transferObservation{Seconds: elapsed.Seconds(), Accepted: current.Accepted - s.baseline.Accepted, Generation: generation, Valid: true, Reason: "measured"}
	o.BytesPerSecond = float64(o.Accepted) / o.Seconds
	if elapsed > 15*time.Second {
		o.Valid = false
		o.Reason = "sampling interrupted"
	}
	if current.Retrying > 0 || current.RetryWait > s.baseline.RetryWait || current.Failures > s.baseline.Failures || current.HTTPErrors > s.baseline.HTTPErrors {
		o.Valid = false
		o.Reason = "retry or request error"
	}
	s.at = now
	s.baseline = current
	return o, true
}

// Local prefix and final-file hashing is measured independently of network IO.
func hashTransferRange(ctx context.Context, file *os.File, start, length int64, digest io.Writer) (int64, error) {
	began := time.Now()
	n, err := io.CopyN(digest, io.NewSectionReader(file, start, length), length)
	measurementsFrom(ctx).addDuration(metricVerification, time.Since(began))
	return n, err
}
