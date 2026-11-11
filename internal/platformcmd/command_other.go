//go:build !windows

package platformcmd

import "os/exec"

func configure(c *exec.Cmd) {}
