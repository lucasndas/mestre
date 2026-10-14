//go:build full && !windows

package providers_test

import (
	"context"
	"os/exec"
)

// hookShell runs a generated hook command the way agy does outside Windows:
// sh -c, in the folder of hooks.json.
func hookShell(ctx context.Context, line, dir string, env []string) *exec.Cmd {
	c := exec.CommandContext(ctx, "sh", "-c", line)
	c.Dir, c.Env = dir, env
	return c
}
