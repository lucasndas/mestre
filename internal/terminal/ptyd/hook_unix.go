//go:build !windows

package ptyd

import (
	"context"
	"os/exec"
	"syscall"
)

// errWaitDelay is what Wait reports when a descendant holds the output.
var errWaitDelay = exec.ErrWaitDelay

func shellCommand(ctx context.Context, line string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", line)
}

// hookTree is the process group of the hook: the shell leads it from the fork
// on (Setpgid) and the time limit kills the group. A descendant that leaves the
// group (setsid) is still in the cgroup of the terminal, which the engine kills
// with it.
type hookTree struct{ c *exec.Cmd }

func newHookTree(c *exec.Cmd) (*hookTree, error) {
	t := &hookTree{c: c}
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		t.kill()
		return nil
	}
	return t, nil
}

func (*hookTree) started(*exec.Cmd) error { return nil }

// kill ends every process of the group; a group already gone (ESRCH) is what
// the call wants anyway. Start sets Process before the context can call Cancel.
func (t *hookTree) kill() {
	if p := t.c.Process; p != nil && p.Pid > 0 {
		_ = syscall.Kill(-p.Pid, syscall.SIGKILL)
	}
}

func (*hookTree) close() {}
