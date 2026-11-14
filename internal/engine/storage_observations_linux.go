//go:build linux

package engine

import (
	"fmt"
	"os"
	"syscall"
)

func filesystemKey(file *os.File) string {
	info, err := file.Stat()
	if err != nil {
		return ""
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok {
		return fmt.Sprintf("device:%d", stat.Dev)
	}
	return ""
}
