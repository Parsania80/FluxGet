package main

import (
	"context"
	"errors"
	"sync/atomic"
)

type workerSettings struct {
	Maximum, Target int
	Reason          string
}

// The queue publishes targets; the transfer owner alone assigns file parts.
type workerControl struct {
	settings atomic.Pointer[workerSettings]
	changed  chan struct{}
}
type workerControlKey struct{}

func newWorkerControl(maximum, target int) *workerControl {
	c := &workerControl{changed: make(chan struct{}, 1)}
	c.settings.Store(&workerSettings{Maximum: maximum, Target: target})
	return c
}

func (c *workerControl) set(maximum, target int) error {
	if maximum < 1 || maximum > 16 || target < 1 || target > maximum {
		return errors.New("worker target must be within the configured 1–16 connection maximum")
	}
	c.settings.Store(&workerSettings{Maximum: maximum, Target: target})
	select {
	case c.changed <- struct{}{}:
	default:
	}
	return nil
}

func workerControlFrom(ctx context.Context) *workerControl {
	c, _ := ctx.Value(workerControlKey{}).(*workerControl)
	return c
}
