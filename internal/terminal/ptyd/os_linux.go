package ptyd

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// Harden protects the mestre-pty from the agents, which run with the same
// account: PR_SET_DUMPABLE 0 stops another process of that account from
// attaching with ptrace, reading this process's memory and environment
// (/proc/<pid>/environ) or taking its descriptors (pidfd_getfd)
// (docs/arquitetura/terminal.md#socket-e-aperto-de-mão). Children do not
// inherit it across exec.
func Harden() error {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		return fmt.Errorf("prctl(PR_SET_DUMPABLE): %w", err)
	}
	return nil
}

// Listen returns the listener of the terminal. On Linux the engine creates
// the socket (owner mestre, mode 0600, folder 0700) and passes the
// descriptor already listening, so the agent account does not even cross
// the folder: fd is that descriptor. With fd 0 the mestre-pty creates the
// socket at path itself (used by tests and by hosts without the engine's
// help).
func Listen(path string, fd int) (net.Listener, error) {
	if fd == 0 {
		return listenPath(path)
	}
	// Listening again on the inherited socket makes the kernel record this
	// process as the owner of the listening end: the engine reads the PID of
	// the peer with SO_PEERCRED and must see the mestre-pty, not itself.
	if err := unix.Listen(fd, 128); err != nil {
		return nil, fmt.Errorf("listen on descriptor %d: %w", fd, err)
	}
	f := os.NewFile(uintptr(fd), "listener")
	defer f.Close()
	ln, err := net.FileListener(f)
	if err != nil {
		return nil, fmt.Errorf("listener from descriptor %d: %w", fd, err)
	}
	return ln, nil
}

func listenPath(path string) (net.Listener, error) {
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("chmod %s: %w", path, err)
	}
	return ln, nil
}
