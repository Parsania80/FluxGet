package engine

import (
	"context"
	"io"
	"net/http"
	"time"
)

type speedLimiter struct {
	gate           chan struct{}
	bytesPerSecond int64
	last           time.Time
	credit         float64
	live           *liveSpeedSource
	liveChecked    time.Time
}

func (l *speedLimiter) wait(ctx context.Context, n int) error {
	if l.live != nil {
		return l.waitLive(ctx, n)
	}
	if n == 0 {
		return nil
	}
	select {
	case l.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-l.gate }()
	now := time.Now()
	if !l.last.IsZero() {
		l.credit += now.Sub(l.last).Seconds() * float64(l.bytesPerSecond)
	}
	capacity := float64(l.bytesPerSecond) / 10
	if capacity < 1 {
		capacity = 1
	}
	if capacity > 32768 {
		capacity = 32768
	}
	if l.credit > capacity {
		l.credit = capacity
	}
	l.last = now
	if l.credit >= float64(n) {
		l.credit -= float64(n)
		return nil
	}
	delay := time.Duration(float64(time.Second) * (float64(n) - l.credit) / float64(l.bytesPerSecond))
	l.credit = 0
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		l.last = time.Now()
		return nil
	}
}

type limitedTransport struct {
	base  http.RoundTripper
	limit *speedLimiter
}

func (t *limitedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(req)
	if err == nil {
		response.Body = &limitedBody{ReadCloser: response.Body, ctx: req.Context(), limit: t.limit}
	}
	return response, err
}
func (t *limitedTransport) CloseIdleConnections() {
	if c, ok := t.base.(interface{ CloseIdleConnections() }); ok {
		c.CloseIdleConnections()
	}
}

type limitedBody struct {
	io.ReadCloser
	ctx   context.Context
	limit *speedLimiter
}

func (b *limitedBody) Read(p []byte) (int, error) {
	max := int64(0)
	if b.limit.live != nil {
		started := time.Now()
		size, err := b.limit.liveReadSize(b.ctx)
		measurementsFrom(b.ctx).addDuration(metricLimiterWait, time.Since(started))
		if err != nil {
			return 0, err
		}
		max = size
	} else {
		max = b.limit.bytesPerSecond / 10
	}
	if max < 1 {
		max = 1
	}
	if max > 32768 {
		max = 32768
	}
	if int64(len(p)) > max {
		p = p[:int(max)]
	}
	n, err := b.ReadCloser.Read(p)
	started := time.Now()
	waitErr := b.limit.wait(b.ctx, n)
	measurementsFrom(b.ctx).addDuration(metricLimiterWait, time.Since(started))
	if waitErr != nil {
		return n, waitErr
	}
	return n, err
}

type limiterContextKey struct{}

func limitClient(client *http.Client, limit *speedLimiter) {
	if limit != nil {
		client.Transport = &limitedTransport{base: client.Transport, limit: limit}
	}
}
