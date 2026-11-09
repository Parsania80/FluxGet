//go:build linux

package main

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTransferMeasurementsSeparateSavedAndFreshBytes(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	payload := bytes.Repeat([]byte("measure"), 200000)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"v1"`)
		http.ServeContent(w, r, "file", time.Time{}, bytes.NewReader(payload))
	}))
	defer server.Close()
	dest, _, saved := prefixFixture(t, server.URL, payload)
	m := &transferMeasurements{}
	ctx := context.WithValue(context.Background(), measurementContextKey{}, m)
	budget := newRequestBudget(defaultRequestPolicy())
	client := server.Client()
	client.Transport = &requestTransport{base: client.Transport, budget: budget, job: 1, metrics: m}
	if _, err := resumeDownload(ctx, client, dest, "", io.Discard); err != nil {
		t.Fatal(err)
	}
	got := m.snapshot()
	if got.Accepted != int64(len(payload))-saved.Completed {
		t.Fatalf("counted saved bytes as fresh: %+v", got)
	}
	if got.Requests != 3 || got.NetworkBytes != got.Accepted+1 || got.NetworkRead <= 0 || got.Write <= 0 || got.Sync <= 0 || got.Checkpoint <= 0 || got.Verification <= 0 {
		t.Fatalf("missing separated measurements: %+v", got)
	}
	snapshot, _ := budget.snapshot()
	if snapshot.Active != 0 {
		t.Fatal("completed transfer leaked requests")
	}
	requireContents(t, dest, payload)
}
func TestObservationWindowsUseFakeClockAndInvalidate(t *testing.T) {
	var sampler observationSampler
	now := time.Unix(100, 0)
	sampler.sample(now, measurementSnapshot{Accepted: 1000}, 1)
	if _, ok := sampler.sample(now.Add(9*time.Second), measurementSnapshot{Accepted: 2000}, 1); ok {
		t.Fatal("short window emitted")
	}
	o, ok := sampler.sample(now.Add(10*time.Second), measurementSnapshot{Accepted: 3000}, 1)
	if !ok || !o.Valid || o.Accepted != 2000 || o.BytesPerSecond != 200 {
		t.Fatalf("wrong window: %+v", o)
	}
	if _, ok := sampler.sample(now.Add(20*time.Second), measurementSnapshot{Accepted: 4000}, 2); ok {
		t.Fatal("configuration change did not reset baseline")
	}
	o, ok = sampler.sample(now.Add(30*time.Second), measurementSnapshot{Accepted: 5000, RetryWait: 1}, 2)
	if !ok || o.Valid {
		t.Fatal("retry window treated as a stable experiment")
	}
}
func TestLimiterAndRetryWaitAreMeasuredSeparately(t *testing.T) {
	m := &transferMeasurements{}
	ctx := context.WithValue(context.Background(), measurementContextKey{}, m)
	body := &limitedBody{ReadCloser: io.NopCloser(bytes.NewReader([]byte("x"))), ctx: ctx, limit: &speedLimiter{gate: make(chan struct{}, 1), bytesPerSecond: 100}}
	if _, err := body.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	tries := 0
	err := withRetries(ctx, time.Millisecond, func() error {
		tries++
		if tries == 1 {
			return io.ErrUnexpectedEOF
		}
		return nil
	})
	if err != nil || tries != 2 || m.snapshot().LimiterWait <= 0 || m.snapshot().RetryWait <= 0 || m.snapshot().NetworkRead != 0 {
		t.Fatalf("incorrect time attribution: %+v %v", m.snapshot(), err)
	}
}
