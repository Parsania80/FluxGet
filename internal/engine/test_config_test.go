package engine

import (
	"fmt"
	"os"
	"testing"
)

func TestMain(m *testing.M) {
	if queueMediaTestTool() {
		return
	}
	if len(os.Args) == 2 && os.Args[1] == "-media-worker" {
		os.Exit(runMediaWorker(os.Stdin, os.Stdout))
	}
	if os.Getenv("FLUXGET_TEST_CONFIG_ROOT") != "" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "fluxget-test-config-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Setenv("XDG_CONFIG_HOME", dir)
	os.Setenv("APPDATA", dir)
	os.Setenv("FLUXGET_TEST_CONFIG_ROOT", dir)
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
