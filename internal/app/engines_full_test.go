//go:build full

package app

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/discovery"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakelock"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/leakcheck"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The engines of the Guild in the process of the test
// (docs/qualidade/testes.md#troca-de-cidadela-e-replicação-na-completa): each
// instance is an Engine with its folders, on the fake network, with the fake
// lock and an Owner who approves everything.

type rig struct {
	t    *testing.T
	root string
	net  *fakenet.Network
	lock *fakelock.Lock
	ips  int
}

// inproc is one instance.
type inproc struct {
	// ended is closed when Run of the engine returns.
	ended  <-chan struct{}
	r      *rig
	name   string
	ip     string
	id     string
	cfg    Config
	paths  Paths
	sheet  *sheetfake.Machine
	lock   *losingLock
	e      *Engine
	stop   func()
	joined *x509.Certificate // the engine certificate of the entry
	log    syncBuffer
	// setup runs on the engine before it starts (the commands of the CLI the
	// Citadel serves); onQueued is Deps.OnQueuedResult.
	setup func(*Engine)
	// host makes the engine host the orchestration (Deps.HostOrchestration).
	host     bool
	onQueued func(QueuedResult)
}

// syncBuffer is the log of an instance, shown when the test fails.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// losingLock is the lock as one machine sees it, which can be made to lose
// every claim (another machine wins the ref).
type losingLock struct {
	guild.Lock
	lose atomic.Bool
}

func (l *losingLock) Claim(ctx context.Context, n uint64, id string) (bool, error) {
	if l.lose.Load() {
		return false, nil
	}
	return l.Lock.Claim(ctx, n, id)
}

func newRig(t *testing.T) *rig {
	return &rig{t: t, root: t.TempDir(), net: fakenet.New(clock.Real{}), lock: fakelock.New(0, "")}
}

func (r *rig) node(name string, others ...*inproc) *inproc {
	r.ips++
	n := &inproc{r: r, name: name, ip: fmt.Sprintf("10.0.0.%d", r.ips), paths: DirPaths(filepath.Join(r.root, name)), sheet: sheetfake.Linux(name)}
	n.paths.CLISocket = ""
	n.cfg = Config{Instance: name, Name: name, Hostname: name, IPs: []string{n.ip}, Bind: name, EnginePort: 7443, NoDiscovery: true, Synchronous: "OFF"}
	for _, o := range others {
		n.cfg.Addresses = append(n.cfg.Addresses, net.JoinHostPort(o.name, "7443"))
	}
	r.net.SetIP(name, n.ip)
	n.sheet.SetIPs(n.ip)
	return n
}

// found makes n the Citadel of a new Guild.
func (r *rig) found(n *inproc, clk clock.Clock) {
	r.t.Helper()
	f, rk, err := Found(r.t.Context(), n.cfg, n.paths, clk)
	if err != nil {
		r.t.Fatal(err)
	}
	rk.Destroy()
	n.id = f.MachineID
	r.lock.Set(1, n.id)
}

// join makes n enter the Guild of the Citadel c, which runs.
func (r *rig) join(n, c *inproc, clk clock.Clock) {
	r.t.Helper()
	j, err := Enroll(r.t.Context(), n.cfg, n.paths, net.JoinHostPort(c.name, "7443"), r.net.Host(n.name), clk, nil)
	if err != nil {
		r.t.Fatalf("%s enters: %v", n.name, err)
	}
	n.id, n.joined = j.MachineID, j.Motor
}

// start runs the engine of n until stop or the end of the test.
func (r *rig) start(n *inproc, clk clock.Clock) {
	r.t.Helper()
	if n.lock == nil {
		n.lock = &losingLock{Lock: r.lock.For(n.id)}
	}
	owner := &testOwner{e: &atomic.Pointer[Engine]{}}
	e, err := New(n.cfg, n.paths, Deps{
		Clock: clk, Network: r.net.Host(n.name), Lock: n.lock, Approver: owner, Promotions: owner,
		// a mestre-pty the CI machine is too busy to schedule within the 5 s of the protocol is not what these tests are about
		TerminalHandshake: time.Minute,
		HostOrchestration: n.host, Sheet: n.sheet.Sources(), RenewEvery: 20 * time.Millisecond, OnQueuedResult: n.onQueued,
		Logger: slog.New(slog.NewTextHandler(&n.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		r.t.Fatal(err)
	}
	owner.e.Store(e)
	if n.setup != nil {
		n.setup(e)
	}
	ctx, cancel := context.WithCancel(r.t.Context())
	done := make(chan error, 1)
	ended := make(chan struct{})
	go func() { done <- e.Run(ctx); close(ended) }()
	n.e, n.ended = e, ended
	n.stop = func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			r.t.Errorf("%s stopped with %v", n.name, err)
		}
		n.stop = func() {}
	}
	r.t.Cleanup(func() {
		n.stop()
		// The engine leaves its terminals running, as it must when it dies: the
		// test ends the ones it started (their sockets are in the folder of n).
		if ps := leakcheck.Reap(n.paths.Maquina); len(ps) > 0 {
			r.t.Logf("%s: ended %d process(es) of its terminals", n.name, len(ps))
		}
		if r.t.Failed() {
			r.t.Logf("---- log of %s ----\n%s", n.name, n.log.String())
		}
	})
}

// waitFor polls ok for 20 s.
func (r *rig) waitFor(what string, ok func() bool) {
	r.t.Helper()
	clk := clock.Real{}
	deadline := clk.NewTimer(20 * time.Second)
	defer deadline.Stop()
	tick := clk.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for !ok() {
		select {
		case <-tick.C():
		case <-deadline.C():
			r.t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (n *inproc) acting() bool {
	st := n.e.Status()
	return st.Role == RoleCitadel && st.Acting && n.e.citadelNow() != nil && n.e.joins.open()
}

func (n *inproc) connected(epoch uint64) bool {
	st := n.e.Status()
	return st.Role == RoleTower && st.Connected && st.Epoch == epoch && !st.Failover
}

// citadelNow is the Citadel of the Guild of the period, when it acts.
func (e *Engine) citadelNow() *guild.Citadel {
	e.mu.Lock()
	run := e.cit
	e.mu.Unlock()
	if run == nil {
		return nil
	}
	return run.guild()
}

// mark writes a row on the Citadel and returns the lsn after it.
func (n *inproc) mark(id string) uint64 {
	n.r.t.Helper()
	var lsn uint64
	if err := n.e.withGuildDB(func(db *store.DB) error {
		if _, err := db.Writer().ExecContext(n.r.t.Context(),
			`INSERT INTO audit_log (id, at, machine_id, action, detail) VALUES (?, 0, 'test', 'test.mark', '{}')`, id); err != nil {
			return err
		}
		var err error
		lsn, err = db.LastLSN(n.r.t.Context())
		return err
	}); err != nil {
		n.r.t.Fatalf("mark on %s: %v", n.name, err)
	}
	return lsn
}

func (n *inproc) query(q string, args ...any) []string {
	n.r.t.Helper()
	var out []string
	if err := n.e.withGuildDB(func(db *store.DB) error {
		rows, err := db.Reader().QueryContext(n.r.t.Context(), q, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var s string
			if err := rows.Scan(&s); err != nil {
				return err
			}
			out = append(out, s)
		}
		return rows.Err()
	}); err != nil {
		n.r.t.Fatalf("query on %s: %v", n.name, err)
	}
	return out
}

func (n *inproc) marks() []string {
	return n.query(`SELECT id FROM audit_log WHERE action = 'test.mark' ORDER BY id`)
}

// A promotion that does not win the lock, because the lock is out of reach
// or another machine won it, leaves the replica exactly as the Citadel sent
// it: nothing of the promotion was written before the claim, and the Tower
// goes on replicating from the same Citadel without a gap.
func TestAPromotionThatDoesNotWinTheLockLeavesTheReplicaInStep(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	lsn := a.mark("t1")
	r.waitFor("b to apply t1", func() bool { return b.connected(1) && b.e.Status().LSN >= lsn })

	for i, how := range []struct {
		name string
		set  func(on bool)
	}{
		{"the lock out of reach", func(on bool) { r.lock.SetDown(b.id, on) }},
		{"another machine wins the lock", func(on bool) { b.lock.lose.Store(on) }},
	} {
		how.set(true)
		if err := b.e.Promote(t.Context(), "test"); err == nil {
			t.Fatalf("%s: the promotion happened", how.name)
		}
		how.set(false)
		r.waitFor("b back as a Tower: "+how.name, func() bool { return b.connected(1) })
		lsn := a.mark(fmt.Sprintf("u%d", i))
		r.waitFor("b to apply the next transaction: "+how.name, func() bool { return b.e.Status().LSN >= lsn })
		if got, want := b.marks(), a.marks(); !slices.Equal(got, want) {
			t.Fatalf("%s: the replica has %v, the Citadel %v", how.name, got, want)
		}
		r.waitFor("the replica at the lsn of the Citadel: "+how.name, func() bool { return b.e.Status().LSN == a.e.Status().LSN })
	}
	if n := len(a.query(`SELECT epoch FROM epochs`)); n != 1 {
		t.Errorf("the Guild has %d epochs after promotions that did not happen", n)
	}
}

func hasName(c *x509.Certificate, name string) bool { return slices.Contains(c.DNSNames, name) }

// mestre run renews the certificates (docs/guilda/guilda.md#credenciais): a
// Tower 30 days before its certificate expires and when the sheet brings it
// other addresses, over the connection it has; the Citadel its own, with its
// CA. Only the acting Citadel presents mestre.local.
func TestMestreRunRenewsTheCertificatesOfTheTowersAndOfTheCitadel(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	// the Guild was formed 336 days ago: 29 days left on every certificate
	past := clock.NewFake(clock.Real{}.Now().Add(-336 * 24 * time.Hour))
	r.found(a, past)
	r.start(a, past)
	r.waitFor("a to act, 336 days ago", a.acting)
	r.join(b, a, past)
	a.stop()
	oldA, oldB := a.e.machine.Leaf(), b.joined

	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.start(b, clock.Real{})
	fresh := func(c *x509.Certificate) bool { return c.NotAfter.After(clock.Real{}.Now().Add(360 * 24 * time.Hour)) }
	r.waitFor("the Citadel to renew its machine certificate", func() bool { return fresh(a.e.machine.Leaf()) })
	r.waitFor("the Tower to renew its certificate", func() bool { return b.connected(1) && b.e.machine != nil && fresh(b.e.machine.Leaf()) })
	if transport.Serial(a.e.machine.Leaf()) == transport.Serial(oldA) || transport.Serial(b.e.machine.Leaf()) == transport.Serial(oldB) {
		t.Fatal("a certificate kept its serial")
	}
	// what each machine keeps follows, and the Citadel's registry knows both
	for _, n := range []*inproc{a, b} {
		id, err := n.e.member.Identity(t.Context())
		if err != nil || transport.Serial(id.Motor) != transport.Serial(n.e.machine.Leaf()) {
			t.Errorf("%s keeps another certificate: %v", n.name, err)
		}
		if got := a.query(`SELECT serial FROM machine_credentials WHERE serial = ?`, transport.Serial(n.e.machine.Leaf())); len(got) != 1 {
			t.Errorf("the registry does not have the renewed certificate of %s", n.name)
		}
	}
	// only the acting Citadel presents mestre.local
	r.waitFor("the Citadel to present mestre.local", func() bool { return hasName(a.e.cert.Leaf(), "mestre.local") })
	if hasName(a.e.machine.Leaf(), "mestre.local") || hasName(b.e.cert.Leaf(), "mestre.local") {
		t.Error("a machine certificate carries mestre.local")
	}

	// the sheet brings b another address: its certificate follows at once
	b.sheet.SetIPs("10.0.9.9")
	if err := b.e.towerNow().agent.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	r.waitFor("the certificate of b to carry its new address", func() bool {
		ips := b.e.cert.Leaf().IPAddresses
		return len(ips) == 1 && ips[0].Equal(net.ParseIP("10.0.9.9"))
	})

	// guild.config gave b the address and the port of the Citadel
	entries, err := discovery.NewSQLCache(b.e.mdb).List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(entries, func(c discovery.CacheEntry) bool {
		return c.Addr == net.JoinHostPort(a.ip, "7443") && c.MachineID == a.id && c.Source == discovery.SourceGuildConfig
	}) {
		t.Errorf("b did not keep the address of the Citadel from guild.config: %+v", entries)
	}
}

// towerNow is the Tower period, when it runs.
func (e *Engine) towerNow() *towerRun {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tow
}

// The planned handover loses nothing: once it starts, the Citadel answers
// what would change the Guild with a retryable unavailable, so the outbox of
// the Tower sends it again to the new Citadel; here a renewal of the
// certificate of c, which the new Citadel registers. The old Citadel stops
// presenting mestre.local, and a removal on the new one reaches it in
// guild.config. Besides the renewal, the receipt vault.dropped of an excluded
// machine is audited by the new Citadel, and neither the decision on an entry
// nor a new entry is taken by the old one.
func TestAPlannedHandoverRefusesTheChangesItWouldLose(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	c := r.node("c", a, b)
	d := r.node("d", a, b)
	a.cfg.Addresses = []string{"b:7443", "c:7443", "d:7443"}
	b.cfg.Addresses = []string{"a:7443", "c:7443", "d:7443"}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.join(c, a, clock.Real{})
	r.join(d, a, clock.Real{})
	r.start(b, clock.Real{})
	r.start(c, clock.Real{})
	r.start(d, clock.Real{})
	lsn := a.mark("t1")
	r.waitFor("b, c and d to apply t1", func() bool {
		return b.connected(1) && c.connected(1) && d.connected(1) &&
			b.e.Status().LSN >= lsn && c.e.Status().LSN >= lsn && d.e.Status().LSN >= lsn
	})
	r.waitFor("a to present mestre.local", func() bool { return hasName(a.e.cert.Leaf(), "mestre.local") })
	// d is excluded: it erases its vault and the receipt is audited by a
	if err := a.e.citadelNow().SetExcluded(t.Context(), d.id, true); err != nil {
		t.Fatal(err)
	}
	const droppedRows = `SELECT id FROM audit_log WHERE action = 'vault.dropped' AND machine_id = ?`
	r.waitFor("a to audit the receipt of d", func() bool { return len(a.query(droppedRows, d.id)) == 1 })
	oldSerial := transport.Serial(c.e.cert.Leaf())

	// the lock waits: the handover drains and stays there
	r.lock.Hold()
	handed := make(chan error, 1)
	go func() { handed <- a.e.Handover(t.Context(), b.id, "aprovacao", "dono", time.Minute) }()
	r.waitFor("a to stop taking changes", func() bool {
		st := a.e.Status()
		return st.Fence == guild.FenceDemoted
	})
	if err := c.e.towerNow().renewer.Renew(t.Context()); err == nil {
		t.Error("the old Citadel renewed a certificate after the handover read its last lsn")
	}
	// the receipt of an excluded machine, the decision on an entry and a new
	// entry are refused too (the receipt is sent again to the new Citadel)
	res, err := d.e.towerNow().client.Link().Call(t.Context(), protocol.Outgoing{Type: protocol.TypeVaultDropped, Payload: protocol.Empty{}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != protocol.AckError || res.Error == nil || !res.Error.Retryable {
		t.Errorf("the old Citadel answered the receipt of d with %+v, want a retryable error", res)
	}
	if got := len(a.query(droppedRows, d.id)); got != 1 {
		t.Errorf("the old Citadel audited %d receipts of d, want the one before the handover", got)
	}
	if err := a.e.DecideEntry(t.Context(), "entrada", guild.Decision{Accept: true, ApprovedBy: "dono"}); !errors.Is(err, guild.ErrSuspended) {
		t.Errorf("the old Citadel decided an entry while it hands over: %v", err)
	}
	e := r.node("e", a, b)
	if _, err := Enroll(t.Context(), e.cfg, e.paths, net.JoinHostPort(a.name, "7443"), r.net.Host(e.name), clock.Real{}, nil); err == nil {
		t.Error("the old Citadel took an entry while it hands over")
	}
	r.lock.Release()
	if err := <-handed; err != nil {
		t.Fatal(err)
	}
	// b waits 60 s before it acts outside; inside the Guild it is the Citadel
	r.waitFor("b to be the Citadel of the epoch 2", func() bool {
		st := b.e.Status()
		return st.Role == RoleCitadel && st.Epoch == 2 && b.e.citadelNow() != nil
	})
	r.waitFor("c to follow b", func() bool { return c.connected(2) && c.e.Status().Citadel == b.id })
	r.waitFor("a to follow b", func() bool { return a.connected(2) })
	// the renewal refused by the old Citadel went again to b, which issued it
	r.waitFor("c to present the certificate b issued", func() bool { return transport.Serial(c.e.cert.Leaf()) != oldSerial })
	r.waitFor("the certificate c presents to be in the registry of b", func() bool {
		return len(b.query(`SELECT serial FROM machine_credentials WHERE serial = ?`, transport.Serial(c.e.cert.Leaf()))) == 1
	})
	r.waitFor("b to audit the receipt of d sent during the handover", func() bool { return len(b.query(droppedRows, d.id)) == 2 })
	if hasName(a.e.cert.Leaf(), "mestre.local") {
		t.Error("the old Citadel still presents mestre.local")
	}
	r.waitFor("b to present mestre.local", func() bool { return hasName(b.e.cert.Leaf(), "mestre.local") })
	if got := b.e.Status(); got.Epoch != 2 {
		t.Fatalf("b: %+v", got)
	}
	cert, err := guild.EpochStart(t.Context(), mustDB(t, b), 2)
	if err != nil || cert.ApprovedBy != "dono" || cert.ApprovalID != "aprovacao" {
		t.Errorf("the epoch 2 records %+v, %v; want the approval of the Owner", cert, err)
	}

	// the new Citadel removes c: a, a Tower now, refuses c at once
	leaf := c.e.cert.Leaf()
	if _, err := b.e.citadelNow().Remove(t.Context(), c.id, "dono"); err != nil {
		t.Fatal(err)
	}
	id, _ := transport.IdentityOf(leaf)
	r.waitFor("a to refuse the removed machine", func() bool {
		return errors.Is(a.e.creds.CheckCredential(id, transport.Serial(leaf)), transport.ErrRevoked)
	})
}

func mustDB(t *testing.T, n *inproc) *store.DB {
	t.Helper()
	n.e.mu.Lock()
	defer n.e.mu.Unlock()
	if n.e.cit == nil {
		t.Fatalf("%s is not the Citadel", n.name)
	}
	return n.e.cit.db
}

// A Citadel that does not act (suspended at the start) answers the cli.call
// of a Tower with citadel_unavailable, as it answers a call made on itself.
func TestASuspendedCitadelAnswersTheCLIOfATowerCitadelUnavailable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	a.stop()
	r.lock.Hold()
	defer r.lock.Release()
	r.start(a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect to the suspended Citadel", func() bool {
		return b.connected(1) && a.e.Status().Fence == guild.FenceSuspended
	})
	resp := forwarder{b.e}.Forward(t.Context(), &localchan.Call{Request: localchan.Request{V: 1, ID: "q1", Cmd: "list"}})
	if resp.Error == nil || resp.Error.Code != localchan.CodeCitadelUnavailable {
		t.Fatalf("the CLI of the Tower got %+v, want citadel_unavailable", resp)
	}
	if !strings.Contains(resp.Error.Message, "not available") {
		t.Errorf("message %q", resp.Error.Message)
	}
}

// A machine that asks a Tower, or a Citadel that has not opened its entries yet,
// to take it in gets "unavailable", which the installer tries again, not
// "refused or expired": the routes of the entry exist from the start and answer
// 503 until a Citadel that acts mounts its own (a missing route answered 404).
func TestTheEntryOfAGuildIsUnavailableWhereThereIsNoCitadelToTakeIt(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	c := r.node("c", b)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	_, err := Enroll(t.Context(), c.cfg, c.paths, net.JoinHostPort(b.name, "7443"), r.net.Host(c.name), clock.Real{}, nil)
	if !errors.Is(err, guild.ErrCitadelUnavailable) || errors.Is(err, guild.ErrJoinRefused) {
		t.Fatalf("the entry through a Tower: %v; want the Citadel unavailable (retryable), not a refusal", err)
	}
	if b.e.joins.open() {
		t.Error("a Tower has the routes of the entry open")
	}
	if !a.e.joins.open() {
		t.Error("the acting Citadel has not opened the routes of the entry")
	}
}
