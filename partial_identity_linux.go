//go:build linux

package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"syscall"
)

type partialIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func identifyPartial(file *os.File) (*partialIdentity, uint64, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() {
		return nil, 0, errors.New("partial identity unavailable")
	}
	return &partialIdentity{Device: uint64(stat.Dev), Inode: stat.Ino}, uint64(stat.Nlink), nil
}

const partialMarkerName = "user.fluxget.partial"

// The descriptor path binds the marker operation to the already-open file.
func createPartialToken(file *os.File) string {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return ""
	}
	token := hex.EncodeToString(data[:])
	if err := syscall.Setxattr(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), partialMarkerName, []byte(token), 0); err != nil {
		return ""
	}
	return token
}
func checkPartialToken(file *os.File, token string) bool {
	if token == "" {
		return false
	}
	var value [32]byte
	n, err := syscall.Getxattr(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), partialMarkerName, value[:])
	return err == nil && n == len(value) && string(value[:]) == token
}

func clearPartialToken(file *os.File, token string) {
	if checkPartialToken(file, token) {
		_ = syscall.Removexattr(fmt.Sprintf("/proc/self/fd/%d", file.Fd()), partialMarkerName)
	}
}
