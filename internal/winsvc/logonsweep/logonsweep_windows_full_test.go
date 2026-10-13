//go:build full && windows

package logonsweep

import (
	"io"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

const helperEnv = "MESTRE_LOGONSWEEP_HELPER"

// TestMain makes the test binary its own helper: a process that waits for its
// stdin to close.
func TestMain(m *testing.M) {
	if os.Getenv(helperEnv) == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// startHelper starts a process that waits for its stdin to close; its stdout
// and stderr are not the test's. Cleanup ends it.
func startHelper(t *testing.T) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"=1")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = in.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})
	return cmd
}

// CA-TERM-05 (the enumeration and the termination of the sweep, run on the
// logon session of the test): the full suite cannot make a logon session of
// its own, and sweeping the one of the runner would end the runner, so the
// test narrows the sweep to one helper process. The sweep of a real terminal
// session (conhost.exe, a process created through WMI) is in the heavy suite.
func TestTheSweepEndsTheProcessesOfTheSession(t *testing.T) {
	t.Parallel()
	self, err := SessionOfSelf()
	if err != nil {
		t.Fatal(err)
	}
	if self.ID == 0 {
		t.Fatal("the logon session of this process is 0")
	}
	child, bystander := startHelper(t), startHelper(t)

	procs, err := Enumerate()
	if err != nil {
		t.Fatal(err)
	}
	for _, pid := range []int{os.Getpid(), child.Process.Pid, bystander.Process.Pid} {
		found := false
		for _, p := range procs {
			if p.PID == uint32(pid) {
				found = true
				if !p.Known || p.LUID != self.ID {
					t.Errorf("process %d: %+v, want the logon session %#x of the test", pid, p, uint64(self.ID))
				}
			}
		}
		if !found {
			t.Errorf("process %d is not in the enumeration", pid)
		}
	}

	// The guard: the logon of the runner is not a terminal's.
	if n, err := Run(self, Options{Only: func(uint32) bool { return true }}); err == nil || n != 0 {
		t.Fatalf("Run swept a session that is not a terminal's: %d, %v", n, err)
	}

	n, err := sweep(self.ID, Options{Only: func(pid uint32) bool { return pid == uint32(child.Process.Pid) }})
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v; want exactly the helper", n, err)
	}
	if err := child.Wait(); err == nil {
		t.Fatal("the helper exited cleanly: it was not ended")
	}
	if !alive(t, bystander.Process.Pid) {
		t.Fatal("the bystander was ended too")
	}
}

// CA-TERM-05 (the sweep after the Job Object): TerminateProcess answers
// "access denied" for a process that has already ended and whose handle
// somebody holds, which is what a process the Job Object of the terminal is
// just ending looks like to the sweep that follows it. That is not a failure
// of the sweep: the process is waited for like the others.
func TestTheSweepDoesNotFailOnAProcessThatIsAlreadyEnding(t *testing.T) {
	t.Parallel()
	self, err := SessionOfSelf()
	if err != nil {
		t.Fatal(err)
	}
	child := startHelper(t)
	pid := uint32(child.Process.Pid)
	// The handle of os/exec stays open until Wait, so the ended process is
	// still there for the sweep to find.
	if err := child.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, pid)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	if ev, err := windows.WaitForSingleObject(h, windows.INFINITE); err != nil || ev != windows.WAIT_OBJECT_0 {
		t.Fatalf("the helper did not end: %d, %v", ev, err)
	}

	n, err := sweep(self.ID, Options{Only: func(p uint32) bool { return p == pid }})
	if err != nil || n != 1 {
		t.Fatalf("sweep = %d, %v; want the ended helper taken as done, without an error", n, err)
	}
}

// alive reports whether the process still runs.
func alive(t *testing.T, pid int) bool {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		t.Fatal(err)
	}
	return code == stillActive
}

// stillActive is STILL_ACTIVE, the exit code of a running process.
const stillActive = 259
