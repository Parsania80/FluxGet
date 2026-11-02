//go:build !linux

package main

import (
	"errors"
	"os"
)

type jobLock struct{}

func lockJob(path string, partial *os.File) (*jobLock, error) {
	return nil, errors.New("job locking is not supported on this platform yet")
}

func (l *jobLock) Close() error { return nil }
