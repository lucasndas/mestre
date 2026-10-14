//go:build full && linux

package main

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// waitGone waits up to d for the process to end, on a pidfd (readable once
// the process has exited, reaped or not), and reports whether it is still
// alive.
func waitGone(t *testing.T, pid int, d time.Duration) bool {
	t.Helper()
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return false
	}
	if err != nil {
		t.Fatalf("pidfd_open %d: %v", pid, err)
	}
	defer unix.Close(fd)
	fds := []unix.PollFd{{Fd: int32(fd), Events: unix.POLLIN}}
	for {
		n, err := unix.Poll(fds, int(d.Milliseconds()))
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			t.Fatalf("poll: %v", err)
		}
		return n == 0
	}
}
