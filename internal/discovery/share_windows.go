package discovery

import (
	"syscall"

	"golang.org/x/sys/windows"
)

// shareControl sets SO_REUSEADDR before the bind: on Windows it lets the
// instances of the machine share port 7445, and each one receives the
// broadcasts.
func shareControl(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		serr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_REUSEADDR, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
