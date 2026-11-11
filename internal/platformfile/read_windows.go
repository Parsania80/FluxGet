//go:build windows

package platformfile

import (
	"errors"
	"github.com/Parsania80/FluxGet/internal/winplatform"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Metadata readers must share delete access so a writer can atomically replace
// settings/checkpoints without a transient sharing violation.
func Open(path string) (*os.File, error) { return winplatform.OpenFile(path, os.O_RDONLY, 0) }
func ReadFile(path string) ([]byte, error) {
	f, err := Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// Replace uses Go's directory-scoped rename, whose Windows adapter requests
// POSIX replacement semantics. Ordinary os.Rename uses MoveFileEx and can fail
// while a delete-sharing metadata reader still holds the previous snapshot.
func Replace(staged, destination string) error {
	source, err := filepath.Abs(staged)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(destination)
	if err != nil {
		return err
	}
	if !strings.EqualFold(filepath.Dir(source), filepath.Dir(target)) {
		return errors.New("metadata replacement must stay in the same directory")
	}
	root, err := os.OpenRoot(filepath.Dir(source))
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Rename(filepath.Base(source), filepath.Base(target))
}
