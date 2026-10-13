package localchan

import (
	"context"
	"fmt"
	"net"
	pathpkg "path"
	"strings"
	"time"
)

// ProcStat is what the identification reads of a process from /proc.
type ProcStat struct {
	PPID int
	// Start is the start time in clock ticks (/proc/<pid>/stat), to compare
	// a parent with its child.
	Start uint64
}

// Proc is /proc, as far as the identification needs it. The system
// implementation is SystemProc (proc_linux.go); tests use a fake /proc.
type Proc interface {
	// Cgroup returns the cgroup v2 path of the process ("/a/b/term-X").
	Cgroup(pid int) (string, error)
	// Stat returns the parent and the start time of the process.
	Stat(pid int) (ProcStat, error)
	// Identify returns the start identity of the process, in the format of
	// Peer.Start (terminal.System.Identify).
	Identify(pid int) (string, error)
}

// Mode is how a ProcIdentifier finds the terminal of a caller.
type Mode int

const (
	// ModeCgroup reads /proc/<pid>/cgroup: every terminal has the cgroup
	// term-<id>, and every descendant inherits it, orphans included.
	ModeCgroup Mode = iota
	// ModeTree climbs the process tree up to the mestre-pty of a terminal:
	// where there is no delegated cgroup. Orphans, which init adopted, are
	// refused (fails safe).
	ModeTree
)

// DefaultTreeAttempts is how many times the climb restarts when a parent
// turns out to be a reused PID.
const DefaultTreeAttempts = 3

// ProcIdentifier identifies callers on Linux, through /proc
// (docs/arquitetura/seguranca.md#identidade-de-quem-chama-a-cli). Its logic
// is the same on every system so that the fast suite tests it with a fake
// /proc; only the sources (peer, pidfd, /proc) are Linux code.
type ProcIdentifier struct {
	Peers     PeerSource
	Proc      Proc
	Terminals Terminals
	Mode      Mode
	// CgroupBase is the cgroup that holds the terminals, as /proc shows it.
	// Empty accepts a term-<id> cgroup anywhere.
	CgroupBase string
	// TreeAttempts defaults to DefaultTreeAttempts.
	TreeAttempts int
}

var _ Identifier = (*ProcIdentifier)(nil)

// Accept reads the peer before anything else happens to the connection.
func (i *ProcIdentifier) Accept(c net.Conn, acceptedAt time.Time) (Peer, error) {
	p, err := i.Peers.Peer(c)
	p.AcceptedAt = acceptedAt
	return p, err
}

// Identify finds the terminal of the caller and checks it is the one of
// the character agentID, and that the caller is still the same process
// after /proc was read.
func (i *ProcIdentifier) Identify(_ context.Context, p Peer, agentID string) (Identity, error) {
	if agentID == "" {
		return Identity{}, refuseErr("no character to check", ErrNoAgent)
	}
	if p.PID <= 0 {
		return Identity{}, refuse("the peer has no PID")
	}
	var (
		term TerminalInfo
		err  error
	)
	switch i.Mode {
	case ModeTree:
		term, err = i.byTree(p.PID)
	default:
		term, err = i.byCgroup(p.PID)
	}
	if err != nil {
		return Identity{}, err
	}
	// The pidfd is checked after /proc was read: it proves the caller was
	// not reaped, and its PID not reused, while the answer was being built.
	if err := checkPidfd(p, p.PID, func() (bool, error) {
		id, err := i.Proc.Identify(p.PID)
		return id == p.Start, err
	}); err != nil {
		return Identity{}, err
	}
	if term.CharacterID != agentID {
		return Identity{}, refuse("the caller runs in a terminal of another character")
	}
	return Identity{AgentID: agentID, TerminalID: term.ID, PID: p.PID}, nil
}

func (i *ProcIdentifier) byCgroup(pid int) (TerminalInfo, error) {
	cg, err := i.Proc.Cgroup(pid)
	if err != nil {
		return TerminalInfo{}, refuseErr("read the cgroup of the caller", err)
	}
	dir, name := pathpkg.Split(pathpkg.Clean(cg))
	id, ok := strings.CutPrefix(name, "term-")
	if !ok || id == "" {
		return TerminalInfo{}, refuse(fmt.Sprintf("the cgroup %q is not the one of a terminal", cg))
	}
	if i.CgroupBase != "" && pathpkg.Clean(dir) != pathpkg.Clean(i.CgroupBase) {
		return TerminalInfo{}, refuse(fmt.Sprintf("the cgroup %q is not under %q", cg, i.CgroupBase))
	}
	t, ok := i.Terminals.Terminal(id)
	if !ok {
		return TerminalInfo{}, refuse(fmt.Sprintf("terminal %q is not registered", id))
	}
	return t, nil
}

// maxDepth bounds the climb: a process tree is never this deep.
const maxDepth = 512

func (i *ProcIdentifier) byTree(pid int) (TerminalInfo, error) {
	attempts := i.TreeAttempts
	if attempts <= 0 {
		attempts = DefaultTreeAttempts
	}
	for range attempts {
		t, raced, err := i.climb(pid)
		if !raced {
			return t, err
		}
	}
	return TerminalInfo{}, refuse(fmt.Sprintf("the process tree changed while it was read, %d times in a row", attempts))
}

// climb walks from the caller to the mestre-pty of a terminal. raced is
// true when a parent started after its child: the real parent died during
// the climb and its PID was reused, so the climb must start again.
func (i *ProcIdentifier) climb(pid int) (t TerminalInfo, raced bool, err error) {
	cur := pid
	st, err := i.Proc.Stat(cur)
	if err != nil {
		return TerminalInfo{}, false, refuseErr("read the caller", err)
	}
	for range maxDepth {
		if t, ok := i.Terminals.ByPTY(cur); ok {
			return t, false, nil
		}
		if st.PPID <= 1 {
			return TerminalInfo{}, false, refuse("the caller has no terminal among its ancestors (an orphan adopted by init, or a process outside)")
		}
		pst, err := i.Proc.Stat(st.PPID)
		if err != nil || pst.Start > st.Start {
			return TerminalInfo{}, true, nil
		}
		cur, st = st.PPID, pst
	}
	return TerminalInfo{}, false, refuse("the process tree is too deep")
}
