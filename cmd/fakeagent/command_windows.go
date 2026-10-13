package main

import (
	"context"
	"os/exec"
	"syscall"
)

// command runs line with cmd /s /c "<line>", the form that keeps quoted
// paths inside line intact (Go would escape them with backslashes, which
// cmd does not understand).
func command(ctx context.Context, line, dir string, env []string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /s /c "` + line + `"`}
	c.Dir, c.Env = dir, env
	return c
}
