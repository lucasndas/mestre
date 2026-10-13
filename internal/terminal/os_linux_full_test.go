//go:build full && linux

package terminal_test

import (
	"errors"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// canLeaveSession: on Linux a process can leave the session of the terminal
// (setsid -f); only the cgroup reaches it.
const canLeaveSession = true

func leaveSessionAttr() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// runBreak is the Windows agent of the Job Object test; nothing on Linux.
func runBreak(string) int { return 2 }

// checkHardened is the Linux part of CA-TERM-11: PR_SET_DUMPABLE 0 keeps
// another process of the same account out of the mestre-pty. The test
// process is the parent of the mestre-pty, which even the Yama ptrace_scope
// 1 allows to trace it, so only the mestre-pty's own protection is tested.
func checkHardened(t *testing.T, pid int) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("root can read and trace any process: the protection is for the other agents' account")
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ"); err == nil {
		t.Errorf("read the environment of the mestre-pty: %q", b)
	}
	if b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/mem"); err == nil {
		t.Errorf("opened the memory of the mestre-pty (%d bytes)", len(b))
	}
	// ptrace works on the thread that attaches.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := unix.PtraceAttach(pid); err == nil {
		_ = unix.PtraceDetach(pid)
		t.Errorf("attached to the mestre-pty with ptrace")
	} else if !errors.Is(err, unix.EPERM) && !errors.Is(err, unix.EACCES) {
		t.Errorf("ptrace attach = %v, want EPERM", err)
	}
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(pidfd)
	if fd, err := unix.PidfdGetfd(pidfd, 3, 0); err == nil {
		_ = unix.Close(fd)
		t.Errorf("took a descriptor of the mestre-pty with pidfd_getfd")
	} else if !errors.Is(err, unix.EPERM) && !errors.Is(err, unix.EACCES) {
		t.Errorf("pidfd_getfd = %v, want EPERM", err)
	}
}
