//go:build full && !windows

package providers_test

import (
	"os/exec"
	"testing"
)

// shellCommand runs a hook line the way Claude Code does on Linux: sh -c.
func shellCommand(_ *testing.T, line string) *exec.Cmd { return exec.Command("sh", "-c", line) }
