package main

import (
	"path/filepath"
	"runtime"
	"testing"
)

func setTestConfig(t *testing.T, path string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", path)
	t.Setenv("APPDATA", path)
}
func testBinaryPath(t *testing.T) string {
	t.Helper()
	name := "fluxget"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(t.TempDir(), name)
}
