//go:build full && windows

package providers_test

import (
	"context"
	"os/exec"
	"syscall"
)

// hookShell runs a generated hook command with cmd /s /c "<line>", the form
// that keeps the quotes of line intact (Go would escape them with
// backslashes, which cmd does not understand).
func hookShell(ctx context.Context, line, dir string, env []string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /s /c "` + line + `"`}
	c.Dir, c.Env = dir, env
	return c
}
