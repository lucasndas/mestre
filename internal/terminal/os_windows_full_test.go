//go:build full && windows

package terminal_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// canLeaveSession: on Windows the Job Object of the terminal has no
// breakaway, so no process leaves it (TestNoProcessLeavesTheJobObject).
const canLeaveSession = false

func leaveSessionAttr() *syscall.SysProcAttr { return nil }

// checkHardened has nothing to check on Windows in this suite: the protection
// of the mestre-pty is the security descriptor the mestre-launcher gives the
// process (the engine terminates and queries, the owner only queries), and
// here the mestre-pty starts directly, as the account of the test, without the
// launcher. The descriptor of a mestre-pty started by the launcher is checked
// in internal/launcher (CA-TERM-07, "protected from the agents").
func checkHardened(t *testing.T, _ int) { t.Helper() }

// runBreak is the agent of the Job Object test: it tries to create a process
// outside the Job (CREATE_BREAKAWAY_FROM_JOB) and writes what happened: the
// Job of the terminal does not allow breakaway, so the system must refuse.
func runBreak(resultFile string) int {
	self, err := os.Executable()
	if err != nil {
		return 1
	}
	c := exec.Command(self, helperSleeper)
	c.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_BREAKAWAY_FROM_JOB}
	result := "denied"
	switch err := c.Start(); {
	case err == nil:
		_ = c.Process.Kill() // it left the Job: the test fails on the result
		_ = c.Wait()
		result = "allowed"
	case !errors.Is(err, windows.ERROR_ACCESS_DENIED):
		result = fmt.Sprintf("error: %v", err)
	}
	if err := os.WriteFile(resultFile, []byte(result), 0o600); err != nil {
		return 1
	}
	fmt.Print("READY\r\n")
	block()
	return 0
}
