//go:build !windows

// Package platformfile reads local metadata while allowing atomic replacement on Windows.
package platformfile

import "os"

func Open(path string) (*os.File, error)   { return os.Open(path) }
func ReadFile(path string) ([]byte, error) { return os.ReadFile(path) }
