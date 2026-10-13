//go:build full

package app

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// runToEnd starts an engine of n and returns what Run ended with, once it ended
// (or the context of the test did).
func (r *rig) runToEnd(n *inproc, clk clock.Clock) error {
	r.t.Helper()
	if n.lock == nil {
		n.lock = &losingLock{Lock: r.lock.For(n.id)}
	}
	owner := &testOwner{e: &atomic.Pointer[Engine]{}}
	e, err := New(n.cfg, n.paths, Deps{
		Clock: clk, Network: r.net.Host(n.name), Lock: n.lock, Approver: owner, Promotions: owner,
		Sheet: n.sheet.Sources(), RenewEvery: 20 * time.Millisecond,
		Logger: slog.New(slog.NewTextHandler(&n.log, &slog.HandlerOptions{Level: slog.LevelDebug})),
	})
	if err != nil {
		r.t.Fatal(err)
	}
	owner.e.Store(e)
	ctx, cancel := clock.WithTimeout(r.t.Context(), clock.Real{}, 20*time.Second)
	defer cancel()
	return e.Run(ctx)
}

// The engine does not open its port with an empty list of refusals: when the
// credentials cannot be read from the database, it does not start.
func TestTheEngineDoesNotStartWhenTheRefusalsCannotBeRead(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	a.stop()

	// the file as SQLite sees it, outside the guard of the replicated schema
	db, err := sql.Open("sqlite", a.paths.guildDB())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `DROP TABLE machine_credentials`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	err = r.runToEnd(a, clock.Real{})
	if err == nil || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Fatalf("the engine ran with a list of refusals it could not read: %v", err)
	}
}

// A machine removed while its Citadel is stopped is refused at the handshake
// as soon as the Citadel starts again: the list is read from the database
// before the port opens.
func TestARemovedMachineIsRefusedByTheHandshakeRightAfterARestart(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	c := r.node("c", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(c, a, clock.Real{})
	r.start(c, clock.Real{})
	r.waitFor("c to connect", func() bool { return c.connected(1) })
	id, src, err := c.e.member.TLS(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tc, err := transport.NewClient(transport.ClientConfig{CA: id.CA, Cert: src, Network: r.net.Host(c.name), Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	c.stop()
	if _, err := a.e.citadelNow().Remove(t.Context(), c.id, "dono"); err != nil {
		t.Fatal(err)
	}
	a.stop()

	r.start(a, clock.Real{})
	r.waitFor("a to act again", a.acting)
	if conn, err := tc.Dial(t.Context(), net.JoinHostPort(a.name, "7443"), transport.RouteTower); err == nil {
		_ = conn.CloseNow()
		t.Error("a machine removed before the restart connected")
	}
}

// guild.config makes a Tower drop the open connection of a machine that was
// removed: with 4002, whether or not its own replica has the removal yet.
func TestGuildConfigMakesATowerDropTheConnectionOfARemovedMachine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	c := r.node("c", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.join(c, a, clock.Real{})
	r.start(b, clock.Real{})
	r.start(c, clock.Real{})
	r.waitFor("b and c to connect", func() bool { return b.connected(1) && c.connected(1) })

	// b holds an open connection to the port of c
	id, src, err := b.e.member.TLS(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	tc, err := transport.NewClient(transport.ClientConfig{CA: id.CA, Cert: src, Network: r.net.Host(b.name), Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	conn, err := tc.Dial(t.Context(), net.JoinHostPort(c.name, "7443"), transport.RouteTower)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	deadline := clock.Real{}.NewTimer(20 * time.Second)
	defer deadline.Stop()
	ended := make(chan error, 1)
	go func() {
		_, _, err := conn.Read(t.Context())
		ended <- err
	}()

	if _, err := a.e.citadelNow().Remove(t.Context(), b.id, "dono"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-ended:
		if got := transport.CloseStatus(err); got != transport.StatusRevoked {
			t.Errorf("the connection of the removed machine ended with %v, want 4002", err)
		}
	case <-deadline.C():
		t.Fatal("the Tower kept the connection of the removed machine")
	}
}
