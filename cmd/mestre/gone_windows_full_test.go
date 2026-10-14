//go:build full && windows

package main

import (
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// waitGone waits up to d for the process to end and reports whether it is
// still alive.
func waitGone(t *testing.T, pid int, d time.Duration) bool {
	t.Helper()
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false // gone already
	}
	defer func() { _ = windows.CloseHandle(h) }()
	ev, err := windows.WaitForSingleObject(h, uint32(d.Milliseconds()))
	if err != nil {
		t.Fatalf("wait for %d: %v", pid, err)
	}
	return ev == uint32(windows.WAIT_TIMEOUT)
}
