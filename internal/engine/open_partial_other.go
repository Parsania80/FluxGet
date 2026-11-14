//go:build !windows

package engine

import "os"

func openPartial(path string, flag int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flag, mode)
}
