//go:build !linux

package main

import (
	"errors"
	"os"
)

func lockQueue(path string) (*os.File, error) {
	return nil, errors.New("queue locking is not supported on this platform yet")
}
