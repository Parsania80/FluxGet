// Package buildinfo provides the shared desktop and CLI release version.
package buildinfo

import (
	_ "embed"
	"strings"
)

//go:embed version.txt
var version string

func Version() string { return strings.TrimSpace(version) }
