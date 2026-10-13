package discovery

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// shareControl sets SO_REUSEADDR and SO_REUSEPORT before the bind: every
// instance of the machine receives the broadcasts of port 7445.
func shareControl(_, _ string, c syscall.RawConn) error {
	var serr error
	err := c.Control(func(fd uintptr) {
		if serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEADDR, 1); serr != nil {
			return
		}
		serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_REUSEPORT, 1)
	})
	if err != nil {
		return err
	}
	return serr
}
