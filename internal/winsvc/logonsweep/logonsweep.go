// Package logonsweep ends what is left of the logon session of a terminal on
// Windows: the conhost.exe of the ConPTY and the processes created through
// WMI, which are born outside the Job Object of the terminal but carry the
// logon session of the account that asked (docs/arquitetura/terminal.md#encerramento,
// docs/arquitetura/windows.md#conpty).
//
// It is only valid because the launcher makes one batch logon session per
// terminal. Sweeping any other session would end processes that are not the
// terminal's, so Allowed is the guard: a launcher that is not a service (the
// CI) shares the logon session of the test runner, and the sweep refuses it.
//
// The decision of what to end is portable (Select, Allowed) and tested in the
// fast suite; the enumeration of processes and their tokens is
// logonsweep_windows.go.
package logonsweep

import "fmt"

// LUID is the locally unique identifier of a logon session
// (TOKEN_STATISTICS.AuthenticationId).
type LUID uint64

// The logon sessions of the system, which are shared by every service and
// every process of those accounts: never swept.
const (
	SessionNetworkService LUID = 0x3e4
	SessionLocalService   LUID = 0x3e5
	SessionAnonymous      LUID = 0x3e6
	SessionSystem         LUID = 0x3e7
)

// LogonTypeBatch is SECURITY_LOGON_TYPE Batch, the type the launcher logs
// the agent account on with.
const LogonTypeBatch = 4

// Session is a logon session as LSA describes it.
type Session struct {
	ID        LUID
	LogonType uint32
}

// Allowed says whether the session may be swept: it must be a batch logon
// that is not one of the system's. The logon of a runner of the CI, a service
// or a person is never one, whatever else is configured.
func Allowed(s Session) error {
	switch s.ID {
	case 0:
		return fmt.Errorf("logon session %#x is not a session", uint64(s.ID))
	case SessionNetworkService, SessionLocalService, SessionAnonymous, SessionSystem:
		return fmt.Errorf("logon session %#x belongs to the system, not to a terminal", uint64(s.ID))
	}
	if s.LogonType != LogonTypeBatch {
		return fmt.Errorf("logon session %#x is of type %d, not the batch logon of a terminal", uint64(s.ID), s.LogonType)
	}
	return nil
}

// Proc is a process seen by the enumeration: its PID and the logon session
// of its token. Known is false when the token could not be read; such a
// process is never taken.
type Proc struct {
	PID   uint32
	LUID  LUID
	Known bool
}

// Select returns the PIDs of the processes in the target session, leaving
// out the Idle process, System and the ones in keep (the sweeper itself).
func Select(procs []Proc, target LUID, keep ...uint32) []uint32 {
	var out []uint32
next:
	for _, p := range procs {
		if !p.Known || p.LUID != target || p.PID == 0 || p.PID == 4 {
			continue
		}
		for _, k := range keep {
			if p.PID == k {
				continue next
			}
		}
		out = append(out, p.PID)
	}
	return out
}

// Options narrow a sweep.
type Options struct {
	// Keep lists PIDs never ended (the caller's own).
	Keep []uint32
	// Only, when set, restricts the sweep further: only PIDs for which it
	// returns true are ended. The tests use it to run the real enumeration
	// and termination on a throwaway process, in a session they cannot own.
	Only func(pid uint32) bool
}

// MaxRounds bounds the rounds of a sweep: each round ends what the previous
// one found plus whatever those processes created meanwhile.
const MaxRounds = 20
