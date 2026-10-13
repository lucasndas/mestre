package localchan

import (
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

// SystemConfig configures NewSystemIdentifier, the identifier of the
// operating system the engine runs on: ProcIdentifier on Linux (cgroup of
// the terminal, or the process tree), JobIdentifier on Windows (the Job
// Object of the terminal, asked of its mestre-pty).
type SystemConfig struct {
	Clock     clock.Clock
	Terminals Terminals
	// System tells processes apart and who is on the other end of a socket
	// (default terminal.NewSystem()).
	System terminal.System

	// Linux only. Mode is ModeCgroup where the terminals have a delegated
	// cgroup and ModeTree where they do not; CgroupBase is the cgroup that
	// holds the terminals, as /proc/<pid>/cgroup shows it.
	Mode       Mode
	CgroupBase string

	// Windows only: Jobs asks the mestre-pty of a terminal whether a
	// process is in its Job Object (Directory does).
	Jobs JobAsker
}
