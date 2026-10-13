package ptyd

import (
	"errors"
	"net"
)

// Harden protects the mestre-pty from the agents. On Windows that is the
// security descriptor the process is created with (docs/arquitetura/
// terminal.md#socket-e-aperto-de-mão); the process cannot add it to itself
// reliably, so until the launcher exists (internal/winsvc, spike 5) there is
// nothing to do here.
func Harden() error { return nil }

// Listen creates the socket at path. The ACL of the folder that holds it
// (OWNER RIGHTS with read-permissions only) is set by the installer and the
// launcher, not here; in tests it is the default ACL. A descriptor from the
// engine does not exist on Windows.
func Listen(path string, fd int) (net.Listener, error) {
	if fd != 0 {
		return nil, errors.New("a listening descriptor from the engine is not supported on Windows")
	}
	return net.Listen("unix", path)
}
