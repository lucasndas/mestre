//go:build !windows

package main

import (
	"context"
	"os/exec"
	"syscall"
)

func shellCommand(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", line)
}

// hookTree is the process group of the hook: the shell leads it, and the
// time limit kills the group.
type hookTree struct{ pgid int }

func newHookTree(c *exec.Cmd) (*hookTree, error) {
	t := &hookTree{}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		t.kill()
		return nil
	}
	return t, nil
}

func (t *hookTree) started(c *exec.Cmd) error {
	t.pgid = c.Process.Pid
	return nil
}

// kill ends every process of the group; a group already gone (ESRCH) is
// what the call wants anyway.
func (t *hookTree) kill() {
	if t.pgid > 0 {
		_ = syscall.Kill(-t.pgid, syscall.SIGKILL)
	}
}

func (*hookTree) close() {}
