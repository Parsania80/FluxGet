//go:build !linux && !darwin && !windows

package media

import (
	"errors"
	"os/exec"
)

type processTree struct{}

func startTree(*exec.Cmd) (*processTree, error) { return nil, errors.New("unsupported media platform") }
func (*processTree) interrupt()                 {}
func (*processTree) kill()                      {}
func (*processTree) close()                     {}
