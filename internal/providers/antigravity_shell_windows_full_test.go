//go:build full && windows

package providers_test

import (
	"context"
	"os/exec"
)

// hookShell runs a generated hook command as agy 1.4.2 does on Windows:
// cmd /c with the quoting of Go, which turns every " of line into \" (seen
// with %CMDCMDLINE% in the Windows round of 2026-10-19). A line with quotes
// breaks here as it breaks in agy.
func hookShell(ctx context.Context, line, dir string, env []string) *exec.Cmd {
	c := exec.CommandContext(ctx, "cmd", "/c", line)
	c.Dir, c.Env = dir, env
	return c
}
