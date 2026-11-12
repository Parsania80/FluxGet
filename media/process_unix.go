//go:build linux || darwin

package media

import (
	"os/exec"
	"syscall"
)

type processTree struct{ pid int }

func startTree(cmd *exec.Cmd) (*processTree, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &processTree{pid: cmd.Process.Pid}, nil
}

func (t *processTree) interrupt() { _ = syscall.Kill(-t.pid, syscall.SIGINT) }
func (t *processTree) kill()      { _ = syscall.Kill(-t.pid, syscall.SIGKILL) }
func (t *processTree) close()     { t.kill() }
