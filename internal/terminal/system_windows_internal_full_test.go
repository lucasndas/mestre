//go:build full && windows

package terminal

import (
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

// CA-TERM-05: TerminateProcess answers "access denied" for a process that is
// already ending, and the engine meets it at the end of a stop (the mestre-pty
// leaves by itself as the agent goes). The real-account tests met it as a stop
// that failed although nothing was left. The test makes the answer real: the
// process ends meanwhile and the system call says "denied". The process is the
// sleeper helper of the external tests of this package (TestMain).
func TestKillTreeTakesAnEndingProcessAsEnded(t *testing.T) {
	t.Parallel()
	cmd := exec.Command(os.Args[0], "helper-sleeper")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	start, err := NewSystem().Identify(cmd.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	sys := windowsSystem{terminate: func(h windows.Handle) error {
		_ = windows.TerminateProcess(h, 1)
		return windows.ERROR_ACCESS_DENIED
	}}
	if err := sys.KillTree(t.Context(), ProcID{PID: cmd.Process.Pid, Start: start}, nil); err != nil {
		t.Fatalf("KillTree = %v, want the process that ended taken as ended", err)
	}
	if _, err := NewSystem().Identify(cmd.Process.Pid); err == nil {
		t.Error("the process is still running")
	}
}
