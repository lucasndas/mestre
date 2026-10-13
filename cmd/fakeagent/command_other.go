//go:build !windows

package main

import (
	"context"
	"os/exec"
)

// command runs line with sh -c, as the providers do outside Windows.
func command(ctx context.Context, line, dir string, env []string) *exec.Cmd {
	c := exec.CommandContext(ctx, "sh", "-c", line)
	c.Dir, c.Env = dir, env
	return c
}
