//go:build full && windows

package providers_test

import (
	"os/exec"
	"syscall"
)

// shellCommand runs a hook line with cmd /s /c "<line>", the form that keeps
// the quoted paths of the line intact.
func shellCommand(line string) *exec.Cmd {
	c := exec.Command("cmd")
	c.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd /s /c "` + line + `"`}
	return c
}
