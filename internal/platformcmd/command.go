// Package platformcmd launches companion processes without opening a Windows console.
package platformcmd

import (
	"context"
	"os/exec"
)

func Command(name string, args ...string) *exec.Cmd {
	c := exec.Command(name, args...)
	configure(c)
	return c
}
func CommandContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	c := exec.CommandContext(ctx, name, args...)
	configure(c)
	return c
}
