package guild

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Leave takes this machine out of the Guild: the Wiper erases the replica,
// the replicated files, the copy of the vault and the Guild configuration,
// and the record of the identity goes. The machine is then outside any Guild,
// with its services stopped until a new entry (a new machine_id, the
// history of the old one stays in the Guild). When the wipe fails the record
// stays, and `mestre uninstall` tries the leave again.
func (m *Member) Leave(ctx context.Context, w Wiper, reason string) error {
	if _, err := m.Identity(ctx); err != nil {
		return err
	}
	if w != nil {
		if err := w.Wipe(ctx, reason); err != nil {
			return fmt.Errorf("guild: erase the share of the Guild: %w", err)
		}
	}
	return m.Forget(ctx)
}

// Caller is the part of *protocol.Link that sending with an ack needs.
type Caller interface {
	Call(ctx context.Context, o protocol.Outgoing) (protocol.AckResult, error)
}

// Uninstall is `mestre uninstall` on a Tower: it asks the Citadel to let it
// go (guild.leave with reason uninstall), waits at most wait for the answer,
// and erases its share of the Guild whatever the answer: with the Citadel
// away it uninstalls all the same, and the panel shows the machine as gone for
// the Owner to remove. link may be nil. It reports whether the Citadel
// acknowledged.
func (m *Member) Uninstall(ctx context.Context, link Caller, w Wiper, clk clock.Clock, wait time.Duration) (acknowledged bool, err error) {
	if link != nil {
		cctx, cancel := clock.WithTimeout(ctx, clk, wait)
		res, cerr := link.Call(cctx, protocol.Outgoing{Type: protocol.TypeGuildLeave, Payload: protocol.GuildLeave{Reason: "uninstall"}})
		cancel()
		acknowledged = cerr == nil && res.Status == protocol.AckOK
	}
	return acknowledged, m.Leave(ctx, w, "uninstall")
}

// TowerLeave is the handler of guild.leave on a Tower: the Citadel removed
// this machine and tells it to erase its share of the Guild.
type TowerLeave struct {
	Member *Member
	Epochs *Epochs
	Wiper  Wiper
	// OnLeft runs after the erasure: the installer stops the services here.
	OnLeft func()
}

// Handle implements protocol.Handler. Only the Citadel the certificate of
// epoch names may tell a machine to leave.
func (t *TowerLeave) Handle(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	if m.Peer.Role != protocol.RoleMachine || m.Peer.ID != t.Epochs.Citadel() {
		return nil, protocol.NewError(protocol.CodeForbidden, "only the Citadel tells a machine to leave the Guild")
	}
	// The Citadel waits a few seconds for the answer and revokes the machine
	// whatever it gets; the connection falls and takes the context of the
	// handler with it. The departure ends all the same: a machine that erased its
	// share and kept its identity is left with nothing, and cannot enter again.
	ctx = context.WithoutCancel(ctx)
	if err := t.Member.Leave(ctx, t.Wiper, "removed"); err != nil && !errors.Is(err, ErrNotMember) {
		return nil, err
	}
	if t.OnLeft != nil {
		t.OnLeft()
	}
	return nil, nil
}

// Wiring says which parts of the Guild a machine's router serves.
type Wiring struct {
	// Citadel returns the Citadel of this machine while it holds the role,
	// and nil on a Tower; it is read on every message, so a promotion takes
	// effect at once.
	Citadel func() *Citadel
	// Tower handles guild.leave on a Tower.
	Tower *TowerLeave
	// Renewer receives cert.issued; nil on a machine that does not renew over
	// this connection.
	Renewer *Renewer
}

// Register puts the handlers of the Guild on the router of the engine:
// cert.renew (the Citadel), cert.issued (the Renewer) and guild.leave (the
// Citadel for a Tower that uninstalls, the Tower for a removal).
func Register(r *protocol.Router, w Wiring) {
	forbidden := func(what string) (*protocol.Reply, error) {
		return nil, protocol.NewError(protocol.CodeForbidden, "this machine is not the Citadel: %s", what)
	}
	r.HandleFunc(protocol.TypeCertRenew, func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		if w.Citadel != nil {
			if c := w.Citadel(); c != nil {
				return c.HandleCertRenew(ctx, m)
			}
		}
		return forbidden("it does not issue certificates")
	})
	r.HandleFunc(protocol.TypeGuildLeave, func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		if w.Citadel != nil {
			if c := w.Citadel(); c != nil {
				return c.HandleLeave(ctx, m)
			}
		}
		if w.Tower != nil {
			return w.Tower.Handle(ctx, m)
		}
		return forbidden("it does not take departures")
	})
	if w.Renewer != nil {
		r.HandleFunc(protocol.TypeCertIssued, w.Renewer.HandleIssued)
	}
}

// ErrOffline: the machine is not connected.
var ErrOffline = errors.New("guild: the machine is not connected")

// ClosesRevoked is the part of *transport.Server that drops the connections
// of revoked credentials.
type ClosesRevoked interface{ CloseRevoked() int }

// ProtocolPeers is the Peers of a Citadel over the protocol and the
// transport: guild.leave goes through the Link of the machine, and the
// connections of the revoked are closed by the TLS server (4002).
type ProtocolPeers struct {
	Server    *protocol.Server
	Transport ClosesRevoked
	Clock     clock.Clock
	// Timeout is how long Leave waits for the ack (10 s).
	Timeout time.Duration
}

var _ Peers = ProtocolPeers{}

// Leave implements Peers.
func (p ProtocolPeers) Leave(ctx context.Context, machineID, reason string) error {
	link := p.Server.Link(machineID)
	if !link.Connected() {
		return ErrOffline
	}
	wait := p.Timeout
	if wait <= 0 {
		wait = 10 * time.Second
	}
	cctx, cancel := clock.WithTimeout(ctx, p.Clock, wait)
	defer cancel()
	res, err := link.Call(cctx, protocol.Outgoing{Type: protocol.TypeGuildLeave, Payload: protocol.GuildLeave{Reason: reason}})
	if err != nil {
		return fmt.Errorf("guild: guild.leave to %s: %w", machineID, err)
	}
	if res.Status != protocol.AckOK {
		return fmt.Errorf("guild: guild.leave to %s: %s", machineID, res.Status)
	}
	return nil
}

// CloseRevoked implements Peers.
func (p ProtocolPeers) CloseRevoked() int {
	if p.Transport == nil {
		return 0
	}
	return p.Transport.CloseRevoked()
}
