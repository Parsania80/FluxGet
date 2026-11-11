//go:build !windows

package main

import "os"

func openPartial(path string, flag int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, mode)
}
