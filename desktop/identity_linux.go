//go:build linux

package main

import (
	"errors"
	"os"
	"syscall"
)

func replacementFor(path string) (*replacement, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("the existing file is not a regular file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil, errors.New("cannot identify existing file")
	}
	return &replacement{Path: path, Device: uint64(stat.Dev), Inode: stat.Ino, Size: info.Size(), Modified: info.ModTime().UnixNano()}, nil
}

func replacementMatches(expected *replacement) bool {
	actual, err := replacementFor(expected.Path)
	return err == nil && *actual == *expected
}
