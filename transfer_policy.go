package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const maxRetries = 3
const maxRetryAfter = time.Minute

type httpStatusError struct {
	code       int
	retryAfter time.Duration
	tooLong    bool
}

func (e *httpStatusError) Error() string {
	if e.tooLong {
		return fmt.Sprintf("server returned HTTP %d; Retry-After exceeds 1 minute", e.code)
	}
	return fmt.Sprintf("server returned HTTP %d", e.code)
}
func transientStatus(response *http.Response) error {
	switch response.StatusCode {
	case 408, 429, 500, 502, 503, 504:
	default:
		return nil
	}
	e := &httpStatusError{code: response.StatusCode}
	if value := strings.TrimSpace(response.Header.Get("Retry-After")); value != "" {
		if seconds, err := strconv.ParseUint(value, 10, 64); err == nil {
			if seconds > uint64(maxRetryAfter/time.Second) {
				e.tooLong = true
			} else {
				e.retryAfter = time.Duration(seconds) * time.Second
			}
		} else if len(value) > 0 && strings.Trim(value, "0123456789") == "" {
			e.tooLong = true
		} else if date, err := http.ParseTime(value); err == nil {
			delay := time.Until(date)
			if delay > maxRetryAfter {
				e.tooLong = true
			} else if delay > 0 {
				e.retryAfter = delay
			}
		}
	}
	return e
}

func retryable(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, child := range joined.Unwrap() {
			if !retryable(child) {
				return false
			}
		}
		return true
	}
	var status *httpStatusError
	if errors.As(err, &status) {
		return !status.tooLong
	}
	var network net.Error
	return (errors.As(err, &network) && network.Timeout()) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.EPIPE)
}

func withRetries(ctx context.Context, initialDelay time.Duration, attempt func() error) error {
	for n := 0; ; n++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := attempt()
		if err == nil || n >= maxRetries || !retryable(err) {
			return err
		}
		delay := initialDelay << n
		var status *httpStatusError
		if errors.As(err, &status) && status.retryAfter > delay {
			delay = status.retryAfter
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
}

type inactivityTimeout struct{}

func (inactivityTimeout) Error() string   { return "response body read inactivity timeout" }
func (inactivityTimeout) Timeout() bool   { return true }
func (inactivityTimeout) Temporary() bool { return true }

type timeoutTransport struct {
	base *http.Transport
	idle time.Duration
}

func (t *timeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(req.Context())
	response, err := t.base.RoundTrip(req.Clone(ctx))
	if err != nil {
		cancel()
		return nil, err
	}
	response.Body = &timeoutBody{ReadCloser: response.Body, cancel: cancel, parent: req.Context(), idle: t.idle}
	return response, nil
}
func (t *timeoutTransport) CloseIdleConnections() { t.base.CloseIdleConnections() }

type timeoutBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	parent context.Context
	idle   time.Duration
}

func (b *timeoutBody) Read(p []byte) (int, error) {
	fired := make(chan struct{})
	timer := time.AfterFunc(b.idle, func() { b.cancel(); close(fired) })
	n, err := b.ReadCloser.Read(p)
	if !timer.Stop() {
		<-fired
		if err := b.parent.Err(); err != nil {
			return n, err
		}
		return n, inactivityTimeout{}
	}
	return n, err
}
func (b *timeoutBody) Close() error { b.cancel(); return b.ReadCloser.Close() }

func newDownloadClient(connect, tlsHandshake, header, idle time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = (&net.Dialer{Timeout: connect, KeepAlive: 30 * time.Second}).DialContext
	transport.TLSHandshakeTimeout = tlsHandshake
	transport.ResponseHeaderTimeout = header
	return &http.Client{Transport: &timeoutTransport{base: transport, idle: idle}}
}
func defaultDownloadClient() *http.Client {
	return newDownloadClient(10*time.Second, 10*time.Second, 30*time.Second, 60*time.Second)
}
