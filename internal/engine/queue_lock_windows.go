//go:build windows

package engine

import (
	"fmt"
	"github.com/Parsania80/FluxGet/internal/winplatform"
	"os"
)

func lockQueue(path string) (*os.File, error) {
	f, err := winplatform.OpenLocked(path, true)
	if err != nil {
		return nil, fmt.Errorf("queue is already open or cannot be locked: %w", err)
	}
	return f, nil
}
