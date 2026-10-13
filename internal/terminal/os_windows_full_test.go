//go:build full && windows

package terminal_test

import (
	"syscall"
	"testing"
)

// canLeaveSession: on Windows the Job Object of the terminal has no
// breakaway, so no process leaves it and there is nothing to test.
const canLeaveSession = false

func leaveSessionAttr() *syscall.SysProcAttr { return nil }

// checkHardened is empty on Windows: the protection of the mestre-pty is the
// security descriptor the mestre-launcher gives the process (OWNER RIGHTS
// with query only), and that service is internal/winsvc, which waits for
// spike 5. In this suite the mestre-pty starts directly as the account of
// the test.
func checkHardened(t *testing.T, _ int) { t.Helper() }
