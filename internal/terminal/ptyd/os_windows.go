package ptyd

import (
	"errors"
	"net"
)

// Harden protects the mestre-pty from the agents. On Windows that is the
// security descriptor the mestre-launcher creates the process with
// (internal/winsvc: SYSTEM everything, the engine terminate and query, the
// owner only query; docs/arquitetura/terminal.md#socket-e-aperto-de-mão). A
// process cannot add it to itself reliably, so there is nothing to do here.
func Harden() error { return nil }

// Listen creates the socket at path. The ACL of the folder that holds it (the
// sockets inherit SYSTEM and the engine with full control and OWNER RIGHTS
// with READ_CONTROL only; mestre-agente can only add a file there) is set by
// the engine before it asks for the terminal (winsvc.PTYDirACL), and by the
// installer (M7); in tests it is the default ACL. A descriptor from the engine
// does not exist on Windows.
func Listen(path string, fd int) (net.Listener, error) {
	if fd != 0 {
		return nil, errors.New("a listening descriptor from the engine is not supported on Windows")
	}
	return net.Listen("unix", path)
}
