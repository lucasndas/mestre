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

// hookTree is the process group of the hook: the shell leads it from the
// fork on (Setpgid), and the time limit kills the group. A descendant that
// leaves the group (setsid) is reached by the sweep of the orphans the
// dispatcher adopts (orphans_linux.go).
type hookTree struct {
	c     *exec.Cmd
	sweep *orphanSweep
}

func newHookTree(c *exec.Cmd) (*hookTree, error) {
	t := &hookTree{c: c, sweep: newOrphanSweep()}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		t.kill()
		return nil
	}
	return t, nil
}

func (*hookTree) started(*exec.Cmd) error { return nil }

// kill ends every process of the group, and then whatever escaped it that the
// dispatcher adopted; a group already gone (ESRCH) is what the call wants
// anyway. Start sets Process before the context can call Cancel.
func (t *hookTree) kill() {
	if p := t.c.Process; p != nil && p.Pid > 0 {
		_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	}
	t.sweep.kill()
}

func (*hookTree) close() {}
