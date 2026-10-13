package localchan

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// Peer is the process on the other end of an accepted connection, as it was
// the moment the engine accepted: everything here is read before the engine
// reads a byte of the request, because the caller can die and its PID be
// reused at any time after.
type Peer struct {
	PID int
	// Start is the start identity of the process (terminal.System.Identify)
	// read at accept time.
	Start string
	// StartedAt is the start time of the process, when the system tells it
	// (Windows); zero otherwise.
	StartedAt time.Time
	// AcceptedAt is when the connection was accepted, taken by the server
	// right after Accept returned, before the peer was read.
	AcceptedAt time.Time
	// Pidfd is a handle on the very process that connected (Linux,
	// SO_PEERPIDFD). Nil means the system gave none: the process was
	// already reaped when the engine accepted, unless PidfdUnsupported.
	Pidfd Pidfd
	// PidfdUnsupported marks a kernel older than 6.5, which has no
	// SO_PEERPIDFD: the start identity is then the only protection against
	// a reused PID.
	PidfdUnsupported bool
}

// Close releases what the peer holds (the pidfd).
func (p Peer) Close() {
	if p.Pidfd != nil {
		_ = p.Pidfd.Close()
	}
}

// Pidfd is a process handle that does not go stale: it keeps pointing to
// the process it was made for, whatever happens to the PID.
type Pidfd interface {
	// PID returns the PID the handle points to, or an error once the
	// process was reaped.
	PID() (int, error)
	Close() error
}

// PeerSource reads who is on the other end of a connection. The system
// implementation is in peer_linux.go and peer_windows.go; tests use a fake.
type PeerSource interface {
	Peer(c net.Conn) (Peer, error)
}

// Identity is a verified caller: the terminal it runs in, and the character
// that terminal belongs to.
type Identity struct {
	AgentID    string
	TerminalID string
	PID        int
}

// Identifier decides who calls (docs/arquitetura/seguranca.md#identidade-de-quem-chama-a-cli).
type Identifier interface {
	// Accept is called right after a connection is accepted, before
	// anything is read from it. acceptedAt is the time of the accept, taken
	// by the server before the peer is read: a process that started after it
	// cannot be the one that connected. An error means the peer cannot be
	// identified, and every command that needs an identity then fails with
	// not_identified; Peer.Close must still be called on the result.
	Accept(c net.Conn, acceptedAt time.Time) (Peer, error)
	// Identify checks that the process is in a live terminal of the
	// character agentID. A failure is an *IdentifyError.
	Identify(ctx context.Context, p Peer, agentID string) (Identity, error)
}

// IdentifyError is why a caller was not identified; it is always
// not_identified for the CLI, and the reason goes to the audit trail.
type IdentifyError struct {
	Reason string
	Err    error
}

func (e *IdentifyError) Error() string {
	if e.Err != nil {
		return "not identified: " + e.Reason + ": " + e.Err.Error()
	}
	return "not identified: " + e.Reason
}

func (e *IdentifyError) Unwrap() error { return e.Err }

func refuse(reason string) error { return &IdentifyError{Reason: reason} }

func refuseErr(reason string, err error) error { return &IdentifyError{Reason: reason, Err: err} }

// TerminalInfo is a registered terminal.
type TerminalInfo struct {
	ID          string
	CharacterID string
}

// Terminals is what the identifiers need to know about the terminals of
// the machine. Directory implements it for the engine.
type Terminals interface {
	// Terminal returns the registered, live terminal with this id (the
	// part after "term-" in the name of its cgroup).
	Terminal(id string) (TerminalInfo, bool)
	// OfCharacter returns the live terminals of a character.
	OfCharacter(characterID string) []TerminalInfo
	// ByPTY returns the terminal whose mestre-pty is the process pid, only
	// if that process is the one recorded (same start identity).
	ByPTY(pid int) (TerminalInfo, bool)
}

// ProcRef is a process by PID and start identity.
type ProcRef struct {
	PID   int
	Start string
}

// JobAsker asks the mestre-pty of a terminal whether a process is in its
// Job Object (Windows).
type JobAsker interface {
	InJob(ctx context.Context, terminalID string, p ProcRef) (bool, error)
}

// ErrNoAgent is returned for a request that names no character.
var ErrNoAgent = errors.New("the request names no character")

// checkPidfd is the last step of the identification on Linux: after the
// /proc was read, the pidfd must still point to the PID that was read, so
// the caller was not reaped, and its PID not reused, in the meantime.
func checkPidfd(p Peer, pid int, sameStart func() (bool, error)) error {
	if p.Pidfd == nil {
		if !p.PidfdUnsupported {
			return refuse("the caller was already gone when the connection was accepted (no pidfd)")
		}
		same, err := sameStart()
		if err != nil {
			return refuseErr("the caller is gone", err)
		}
		if !same {
			return refuse("the PID now belongs to another process")
		}
		return nil
	}
	got, err := p.Pidfd.PID()
	if err != nil {
		return refuseErr("the caller was reaped while it was identified", err)
	}
	if got != pid {
		return refuse(fmt.Sprintf("the pidfd points to PID %d, not %d", got, pid))
	}
	return nil
}
