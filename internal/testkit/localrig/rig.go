package localrig

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Addr is where the engine of the rig listens on the fake network.
const Addr = "engine:cli"

// Rig is an engine on the fake network: a localchan.Server whose
// identifier is the real ProcIdentifier over a fake /proc, fake terminals
// and fake peers. Use it inside a testing/synctest bubble with clock.Real,
// or with a fake clock.
type Rig struct {
	Clock     clock.Clock
	Net       *fakenet.Network
	Peers     *FakePeers
	Proc      *FakeProc
	Terminals *FakeTerminals
	Forwarder *localchan.LocalForwarder
	Server    *localchan.Server

	mu      sync.Mutex
	audits  []localchan.Audit
	nextPID int
	wait    func()
}

// Options configure New; the zero value is a tree-less engine that reads
// cgroups.
type Options struct {
	Config localchan.Config // Clock and OnAudit are the rig's; Identifier is, unless set
	Mode   localchan.Mode
}

// New returns a rig. Call Start to serve.
func New(clk clock.Clock, o Options) *Rig {
	r := &Rig{
		Clock: clk, Net: fakenet.New(clk), Peers: &FakePeers{}, Proc: &FakeProc{}, Terminals: &FakeTerminals{},
		Forwarder: localchan.NewLocalForwarder(), nextPID: 1000,
	}
	cfg := o.Config
	cfg.Clock = clk
	if cfg.Identifier == nil {
		cfg.Identifier = &localchan.ProcIdentifier{Peers: r.Peers, Proc: r.Proc, Terminals: r.Terminals, Mode: o.Mode, CgroupBase: CgroupBase}
	}
	if cfg.Forwarder == nil {
		cfg.Forwarder = r.Forwarder
	}
	cfg.OnAudit = func(a localchan.Audit) {
		r.mu.Lock()
		r.audits = append(r.audits, a)
		r.mu.Unlock()
	}
	r.Server = localchan.NewServer(cfg)
	return r
}

// Start serves in the background until the test ends; Stop waits for it.
func (r *Rig) Start(t testing.TB) {
	t.Helper()
	ln, err := r.Net.Listen(Addr)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = r.Server.Serve(ctx, ln)
	}()
	r.wait = func() { cancel(); <-done }
	t.Cleanup(r.Stop)
}

// Stop ends the server and waits for the requests in progress.
func (r *Rig) Stop() {
	if r.wait != nil {
		r.wait()
		r.wait = nil
	}
}

// Dialer is the way for a process named host to connect.
func (r *Rig) Dialer(host string) localchan.Dialer {
	return func(ctx context.Context) (net.Conn, error) { return r.Net.Dial(ctx, host, Addr) }
}

// Audits returns what the server recorded so far.
func (r *Rig) Audits() []localchan.Audit {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]localchan.Audit(nil), r.audits...)
}

// CgroupBase is where the rig puts the cgroups of its terminals.
const CgroupBase = "/mestre-launcher.service"

// Character registers a live terminal of a character, with its cgroup.
func (r *Rig) Character(terminalID, characterID string) {
	r.Terminals.Add(terminalID, characterID, 0)
}

// Process registers a caller named host, in the cgroup of the terminal (or
// outside any, with an empty terminalID), with a pidfd and a start
// identity, and returns its pidfd and PID.
func (r *Rig) Process(host, terminalID string) (pid int, pidfd *FakePidfd) {
	return r.ProcessOf(host, terminalID, "")
}

// ProcessOf is Process for a process of the user uid ("" is not told), as
// SO_PEERCRED tells it.
func (r *Rig) ProcessOf(host, terminalID, uid string) (pid int, pidfd *FakePidfd) {
	r.mu.Lock()
	r.nextPID++
	pid = r.nextPID
	r.mu.Unlock()
	cg := "/user.slice/session.scope"
	if terminalID != "" {
		cg = CgroupBase + "/term-" + terminalID
	}
	start := fmt.Sprintf("boot:%d", pid)
	r.Proc.SetCgroup(pid, cg)
	r.Proc.SetIdentity(pid, start)
	pidfd = NewPidfd(pid)
	r.Peers.Set(host, localchan.Peer{PID: pid, Start: start, Pidfd: pidfd, Group: localchan.TerminalGroup(cg), UID: uid})
	return pid, pidfd
}
