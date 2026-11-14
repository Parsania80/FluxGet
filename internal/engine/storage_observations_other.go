//go:build !linux

package engine

import "os"

func filesystemKey(*os.File) string { return "" }
