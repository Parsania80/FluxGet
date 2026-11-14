//go:build windows

package engine

import (
	"crypto/rand"
	"encoding/hex"
	"github.com/Parsania80/FluxGet/internal/winplatform"
	"io"
	"os"
)

type partialIdentity struct {
	Device uint64 `json:"device"`
	Inode  uint64 `json:"inode"`
}

func identifyPartial(file *os.File) (*partialIdentity, uint64, error) {
	volume, id, links, err := winplatform.Identity(file)
	if err != nil {
		return nil, 0, err
	}
	return &partialIdentity{Device: volume, Inode: id}, links, nil
}

// NTFS alternate streams keep the ownership marker attached to the file. Both
// before and after checks bind the path operation to the open file identity.
func partialMarker(file *os.File, action func(string) bool) bool {
	opened, err := file.Stat()
	if err != nil {
		return false
	}
	named, err := os.Lstat(file.Name())
	if err != nil || !os.SameFile(opened, named) || !named.Mode().IsRegular() {
		return false
	}
	if !action(file.Name() + ":fluxget.partial") {
		return false
	}
	named, err = os.Lstat(file.Name())
	return err == nil && os.SameFile(opened, named)
}
func createPartialToken(file *os.File) string {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return ""
	}
	token := hex.EncodeToString(value[:])
	if !partialMarker(file, func(path string) bool { return os.WriteFile(path, []byte(token), 0600) == nil }) {
		return ""
	}
	return token
}
func checkPartialToken(file *os.File, token string) bool {
	return token != "" && partialMarker(file, func(path string) bool {
		f, err := os.Open(path)
		if err != nil {
			return false
		}
		defer f.Close()
		value, err := io.ReadAll(io.LimitReader(f, 33))
		return err == nil && len(value) == 32 && string(value) == token
	})
}
func clearPartialToken(file *os.File, token string) {
	if checkPartialToken(file, token) {
		partialMarker(file, func(path string) bool { return os.Remove(path) == nil })
	}
}
