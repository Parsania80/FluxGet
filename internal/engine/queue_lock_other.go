//go:build !linux && !windows

package engine

import (
	"errors"
	"os"
)

func lockQueue(path string) (*os.File, error) {
	return nil, errors.New("queue locking is not supported on this platform yet")
}
