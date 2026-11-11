//go:build windows

package main

import (
	"github.com/Parsania80/FluxGet/internal/winplatform"
	"os"
	"path/filepath"
)

func lockHistory() (*os.File, error) {
	path, err := historyPath()
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, err
	}
	return winplatform.OpenLocked(path+".lock", true)
}
