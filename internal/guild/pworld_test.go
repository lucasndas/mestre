package guild_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/testkit/protorig"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The protocol world: the Citadel of a rig with its protocol server, and
// Towers connected to it, over the in-memory network of the protocol
// (internal/testkit/protorig). It runs inside a synctest bubble, so the
// protocol runs on the bubble's clock; the Guild keeps the fake clock of the
// rig, which a test advances by months in an instant.

const protoAddr = "citadel:7444"

// pworld is a protocol server on top of a rig.
type pworld struct {
	r      *rig
	srv    *protocol.Server
	router *protocol.Router
	epochs *guild.Epochs
	closes *closeCounter
	nw     protorig.Network
}

// closeCounter stands for the TLS server of the Citadel when a test is about
// the protocol only.
type closeCounter struct{ n atomic.Int32 }

func (c *closeCounter) CloseRevoked() int { c.n.Add(1); return 0 }

// bubble runs f in a synctest bubble with a rig whose Guild runs on a fake
// clock.
func bubble(t *testing.T, f func(t *testing.T, r *rig, w *pworld)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		r := newRig(t, clock.NewFake(day0))
		f(t, r, r.startProtocol(t))
	})
}

// startProtocol runs the protocol server of the Citadel of the rig.
func (r *rig) startProtocol(t *testing.T) *pworld {
	t.Helper()
	w := &pworld{r: r, router: protocol.NewRouter(), closes: &closeCounter{}, nw: protorig.Network{Net: r.net}}
	w.epochs = loadEpochs(t, r.founder, guild.EpochsConfig{})
	id := r.founded.MachineID
	cfg, _ := protorig.Config(clock.Real{}, id, w.epochs.Epoch(), id)
	cfg.Epoch, cfg.Certs, cfg.Router = w.epochs, w.epochs, w.router
	cfg.IsCitadel = func() bool { return w.epochs.Citadel() == id }
	sql := protocol.NewSQLStore(r.gdb) // the Citadel's outbox lives in the Guild database
	cfg.Outbox, cfg.Processed = sql, sql
	srv, err := protocol.NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	w.srv = srv
	lis, err := w.nw.Listen(protoAddr, protocol.Peer{Role: protocol.RoleMachine, ID: id})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	var wg sync.WaitGroup
	wg.Go(func() { _ = srv.Serve(ctx, lis) })
	t.Cleanup(func() { cancel(); wg.Wait() })
	r.peers.set(guild.ProtocolPeers{Server: srv, Transport: w.closes, Clock: clock.Real{}})
	guild.Register(w.router, guild.Wiring{Citadel: func() *guild.Citadel { return r.citadel }})
	return w
}

func loadEpochs(t *testing.T, n *node, cfg guild.EpochsConfig) *guild.Epochs {
	t.Helper()
	cfg.Member = n.member
	e, err := guild.LoadEpochs(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// wiper records what a machine that leaves is told to erase.
type wiper struct {
	mu      sync.Mutex
	reasons []string
	err     error
}

func (w *wiper) Wipe(_ context.Context, reason string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	w.reasons = append(w.reasons, reason)
	return nil
}

func (w *wiper) wiped() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]string(nil), w.reasons...)
}

// tower is a machine of the Guild connected to the Citadel by the protocol.
type tower struct {
	n       *node
	joined  *guild.Joined
	client  *protocol.Client
	link    *protocol.Link
	epochs  *guild.Epochs
	renewer *guild.Renewer
	src     *transport.CertSource
	wiper   *wiper
	left    chan struct{}
	names   atomic.Pointer[transport.Names]
	refused chan protocol.Certificate
}

// addTower connects the machine n, which joined as j, and lets it settle.
func (w *pworld) addTower(t *testing.T, n *node, j *guild.Joined) *tower {
	t.Helper()
	ctx := t.Context()
	tw := &tower{n: n, joined: j, wiper: &wiper{}, left: make(chan struct{}), refused: make(chan protocol.Certificate, 4)}
	tw.epochs = loadEpochs(t, n, guild.EpochsConfig{OnRefused: func(c protocol.Certificate, _ error) { tw.refused <- c }})
	id := w.r.founded.MachineID
	cfg, _ := protorig.Config(clock.Real{}, j.MachineID, tw.epochs.Epoch(), id)
	cfg.Epoch, cfg.Certs, cfg.Router = tw.epochs, tw.epochs, protocol.NewRouter()
	client, err := protocol.NewClient(cfg, w.nw.Dialer(n.name, protoAddr, protocol.Peer{Role: protocol.RoleMachine, ID: j.MachineID}))
	if err != nil {
		t.Fatal(err)
	}
	tw.client, tw.link = client, client.Link()
	_, tw.src, err = n.member.TLS(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := j.Motor
	tw.names.Store(&transport.Names{MachineID: j.MachineID, Hostname: n.name, IPs: names.IPAddresses})
	tw.renewer, err = guild.NewRenewer(guild.RenewerConfig{
		Kind: transport.KindMachine, CA: j.CA, Source: tw.src, Link: tw.link, Clock: w.r.clk,
		Names: func() transport.Names { return *tw.names.Load() },
		Save: func(ctx context.Context, cert *x509.Certificate, key *ecdsa.PrivateKey) error {
			if err := n.member.SaveCertificate(ctx, transport.KindMachine, cert); err != nil {
				return err
			}
			return n.member.SaveEngineKey(ctx, key)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	guild.Register(cfg.Router, guild.Wiring{
		Tower:   &guild.TowerLeave{Member: n.member, Epochs: tw.epochs, Wiper: tw.wiper, OnLeft: func() { close(tw.left) }},
		Renewer: tw.renewer,
	})
	runCtx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	wg.Go(func() { _ = client.Run(runCtx) })
	t.Cleanup(func() { cancel(); wg.Wait() })
	synctest.Wait()
	return tw
}
