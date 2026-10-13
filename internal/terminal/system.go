package terminal

import (
	"context"
	"errors"
	"net"
)

// ErrNoProcess means the process does not exist (or is a zombie).
var ErrNoProcess = errors.New("no such process")

// ProcID is a process by PID and start identity: on Linux the boot ID and
// the start time in clock ticks (/proc/<pid>/stat), on Windows the creation
// time (GetProcessTimes). Both together never belong to two processes, so a
// reused PID is never taken for the original.
type ProcID struct {
	PID   int
	Start string
}

// System is what differs by operating system on the engine side: telling
// processes apart, who is on the other end of a socket, waiting for a
// process and ending a tree (docs/desenvolvimento/convencoes.md#layout-do-repositório).
// The implementations are in system_linux.go and system_windows.go.
type System interface {
	// Identify returns the start identity of pid, or ErrNoProcess.
	Identify(pid int) (string, error)
	// PeerPID returns the PID of the process on the other end of a Unix
	// socket: SO_PEERCRED on Linux, SIO_AF_UNIX_GETPEERPID on Windows.
	PeerPID(c net.Conn) (int, error)
	// WaitExit blocks until pid is gone (or ctx ends), whether or not it is
	// a child of this process.
	WaitExit(ctx context.Context, pid int) error
	// KillTree ends the mestre-pty p and what is left of its terminal,
	// after checking the identity (an empty p.Start skips the check: the
	// PID came from a socket this engine just connected to). sessions are
	// the session IDs of the agent, for Linux without a cgroup; the Job
	// Object takes the tree on Windows.
	KillTree(ctx context.Context, p ProcID, sessions []int) error
}

// Alive reports whether p is still the same process.
func Alive(sys System, p ProcID) bool {
	start, err := sys.Identify(p.PID)
	return err == nil && (p.Start == "" || start == p.Start)
}
