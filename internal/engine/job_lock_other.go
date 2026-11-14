//go:build !linux && !windows

package engine

import (
	"errors"
	"os"
)

type jobLock struct{}

func lockJob(path string, partial *os.File) (*jobLock, error) {
	return nil, errors.New("job locking is not supported on this platform yet")
}

func (l *jobLock) Close() error { return nil }

func jobActive(path string) (bool, error) {
	return false, errors.New("job locking is not supported on this platform yet")
}

func lockJobRecord(path string) (*os.File, error) {
	return nil, errors.New("job locking is not supported on this platform yet")
}
