//go:build !linux && !windows

package discovery

import "syscall"

// shareControl: the Mestre runs on Linux and Windows; elsewhere (a
// developer's machine) the port is not shared.
func shareControl(_, _ string, _ syscall.RawConn) error { return nil }
