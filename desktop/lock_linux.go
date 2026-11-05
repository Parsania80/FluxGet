//go:build linux

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

func lockHistory() (*os.File, error) {
	path, err := historyPath()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("another FluxGet desktop window is already open: %w", err)
	}
	return f, nil
}
