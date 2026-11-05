//go:build !linux

package main

import (
	"errors"
	"os"
)

type partialIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func identifyPartial(file *os.File) (*partialIdentity, uint64, error) {
	return nil, 0, errors.New("partial identity is not supported on this platform")
}

func createPartialToken(file *os.File) string            { return "" }
func checkPartialToken(file *os.File, token string) bool { return false }

func clearPartialToken(file *os.File, token string) {}
