//go:build !windows

package main

import (
	"context"
	"os/exec"

	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
)

// command runs line with sh -c, as the providers do outside Windows.
func command(ctx context.Context, line, dir string, env []string) *exec.Cmd {
	c := exec.CommandContext(ctx, "sh", "-c", line)
	c.Dir, c.Env = dir, env
	return c
}

// hookCommand runs a hook line: sh -c for every provider outside Windows.
func hookCommand(ctx context.Context, _ fakeagent.Provider, line, dir string, env []string) *exec.Cmd {
	return command(ctx, line, dir, env)
}
