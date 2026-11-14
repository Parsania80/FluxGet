//go:build windows

package engine

import (
	"github.com/Parsania80/FluxGet/internal/winplatform"
	"os"
)

func openPartial(path string, flag int, mode os.FileMode) (*os.File, error) {
	return winplatform.OpenFile(path, flag, mode)
}
