//go:build !linux

package main

import (
	"errors"
	"os"
)

func lockHistory() (*os.File, error) {
	return nil, errors.New("desktop history is currently supported on Linux only")
}
