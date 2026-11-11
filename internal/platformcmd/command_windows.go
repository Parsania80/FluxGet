//go:build windows

package platformcmd

import (
	"golang.org/x/sys/windows"
	"os/exec"
	"syscall"
)

func configure(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.CREATE_NO_WINDOW}
}
