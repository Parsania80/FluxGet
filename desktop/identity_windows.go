//go:build windows

package main

import (
	"github.com/Parsania80/FluxGet/internal/winplatform"
	"os"
)

func replacementFor(path string) (*replacement, error) {
	f, err := winplatform.OpenFile(path, os.O_RDONLY, 0600)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	volume, id, _, err := winplatform.Identity(f)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	return &replacement{Path: path, Device: volume, Inode: id, Size: info.Size(), Modified: info.ModTime().UnixNano()}, nil
}
func replacementMatches(expected *replacement) bool {
	actual, err := replacementFor(expected.Path)
	return err == nil && *actual == *expected
}
