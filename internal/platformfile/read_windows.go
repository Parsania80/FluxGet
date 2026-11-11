//go:build windows

package platformfile

import (
	"github.com/Parsania80/FluxGet/internal/winplatform"
	"io"
	"os"
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
