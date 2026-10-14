package main

import (
	"context"
	"os/exec"
	"syscall"
)

// shellCommand runs line with cmd /s /c "<line>", the form that keeps the
// quoted paths of line intact.
func shellCommand(ctx context.Context, line string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /s /c "` + line + `"`}
	return c
}
