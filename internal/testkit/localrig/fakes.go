// Package localrig is the double of the local channel for the fast suite
// (docs/qualidade/dubles.md): the engine side of internal/localchan on the
// fake network, with a fake /proc, fake terminals and fake peers, so a test
// plays "this process, in that cgroup, calls" without a process or a socket.
package localrig

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/mestre-ia/mestre/internal/localchan"
)

// ErrNoProcess is what the fake /proc says about a process it does not know.
var ErrNoProcess = errors.New("localrig: no such process")

// FakePidfd is a pidfd the test can reap.
type FakePidfd struct {
	mu     sync.Mutex
	pid    int
	reaped bool
}

// NewPidfd returns a pidfd pointing to pid.
func NewPidfd(pid int) *FakePidfd { return &FakePidfd{pid: pid} }

// PID implements localchan.Pidfd.
func (p *FakePidfd) PID() (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.reaped {
		return 0, errors.New("the process was reaped")
	}
	return p.pid, nil
}

// Close implements localchan.Pidfd.
func (p *FakePidfd) Close() error { return nil }

// Reap makes the pidfd stop pointing to the process, as when the caller is
// collected and its PID can be reused.
func (p *FakePidfd) Reap() {
	p.mu.Lock()
	p.reaped = true
	p.mu.Unlock()
}

// FakePeers is the PeerSource of the fake network: the host a caller dials
// from is the name of its process, so each test caller has a Peer.
type FakePeers struct {
	mu    sync.Mutex
	peers map[string]localchan.Peer
}

// Set registers the peer seen when a connection comes from host.
func (f *FakePeers) Set(host string, p localchan.Peer) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.peers == nil {
		f.peers = map[string]localchan.Peer{}
	}
	f.peers[host] = p
}

// Peer implements localchan.PeerSource.
func (f *FakePeers) Peer(c net.Conn) (localchan.Peer, error) {
	host, _, err := net.SplitHostPort(c.RemoteAddr().String())
	if err != nil {
		return localchan.Peer{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p, ok := f.peers[host]
	if !ok {
		return localchan.Peer{}, fmt.Errorf("localrig: no peer registered for %q", host)
	}
	return p, nil
}

// FakeProc is a /proc the test writes.
type FakeProc struct {
	mu      sync.Mutex
	cgroups map[int]string
	stats   map[int][]localchan.ProcStat
	calls   map[int]int
	ids     map[int]string

	// OnCgroup, if set, runs after the cgroup of pid was read: the moment
	// the test makes the caller die or its PID be reused.
	OnCgroup func(pid int)
}

// SetCgroup sets the cgroup v2 path of a process.
func (f *FakeProc) SetCgroup(pid int, cgroup string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cgroups == nil {
		f.cgroups = map[int]string{}
	}
	f.cgroups[pid] = cgroup
}

// SetStat sets what /proc/<pid>/stat says; given several, each read gets
// the next one and the last repeats, which is how a test plays a PID that
// changes between two reads.
func (f *FakeProc) SetStat(pid int, stats ...localchan.ProcStat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.stats == nil {
		f.stats, f.calls = map[int][]localchan.ProcStat{}, map[int]int{}
	}
	f.stats[pid] = stats
	f.calls[pid] = 0
}

// SetIdentity sets the start identity of a process.
func (f *FakeProc) SetIdentity(pid int, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ids == nil {
		f.ids = map[int]string{}
	}
	f.ids[pid] = id
}

// Cgroup implements localchan.Proc.
func (f *FakeProc) Cgroup(pid int) (string, error) {
	f.mu.Lock()
	cg, ok := f.cgroups[pid]
	hook := f.OnCgroup
	f.mu.Unlock()
	if !ok {
		return "", ErrNoProcess
	}
	if hook != nil {
		hook(pid)
	}
	return cg, nil
}

// Stat implements localchan.Proc.
func (f *FakeProc) Stat(pid int) (localchan.ProcStat, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	seq := f.stats[pid]
	if len(seq) == 0 {
		return localchan.ProcStat{}, ErrNoProcess
	}
	i := min(f.calls[pid], len(seq)-1)
	f.calls[pid]++
	return seq[i], nil
}

// Identify implements localchan.Proc.
func (f *FakeProc) Identify(pid int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.ids[pid]
	if !ok {
		return "", ErrNoProcess
	}
	return id, nil
}

// FakeTerminals is the registry of terminals of the test.
type FakeTerminals struct {
	mu    sync.Mutex
	terms []fakeTerminal
}

type fakeTerminal struct {
	info   localchan.TerminalInfo
	ptyPID int
}

// Add registers a live terminal of a character whose mestre-pty is ptyPID
// (0 for none).
func (f *FakeTerminals) Add(id, characterID string, ptyPID int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terms = append(f.terms, fakeTerminal{localchan.TerminalInfo{ID: id, CharacterID: characterID}, ptyPID})
}

// Terminal implements localchan.Terminals.
func (f *FakeTerminals) Terminal(id string) (localchan.TerminalInfo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.terms {
		if t.info.ID == id {
			return t.info, true
		}
	}
	return localchan.TerminalInfo{}, false
}

// OfCharacter implements localchan.Terminals.
func (f *FakeTerminals) OfCharacter(characterID string) []localchan.TerminalInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []localchan.TerminalInfo
	for _, t := range f.terms {
		if t.info.CharacterID == characterID {
			out = append(out, t.info)
		}
	}
	return out
}

// ByPTY implements localchan.Terminals.
func (f *FakeTerminals) ByPTY(pid int) (localchan.TerminalInfo, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, t := range f.terms {
		if t.ptyPID != 0 && t.ptyPID == pid {
			return t.info, true
		}
	}
	return localchan.TerminalInfo{}, false
}

// FakeJobs is the JobAsker of the test: the set of (terminal, process)
// pairs that are in a Job Object.
type FakeJobs struct {
	mu   sync.Mutex
	in   map[string]map[localchan.ProcRef]bool
	Fail error // returned by InJob when set (a mestre-pty that cannot answer)
}

// Put says that p is (or is not) in the Job Object of the terminal.
func (f *FakeJobs) Put(terminalID string, p localchan.ProcRef, in bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.in == nil {
		f.in = map[string]map[localchan.ProcRef]bool{}
	}
	if f.in[terminalID] == nil {
		f.in[terminalID] = map[localchan.ProcRef]bool{}
	}
	f.in[terminalID][p] = in
}

// InJob implements localchan.JobAsker.
func (f *FakeJobs) InJob(_ context.Context, terminalID string, p localchan.ProcRef) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Fail != nil {
		return false, f.Fail
	}
	return f.in[terminalID][p], nil
}
