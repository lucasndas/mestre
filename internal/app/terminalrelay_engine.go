package app

import (
	"context"
	"fmt"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/termstream"
)

// engineRelayWorld is the Citadel of a period as the terminal relay sees it:
// what the machines of the orchestration and the protocol server give, read when
// asked, because the panel is made before them.
type engineRelayWorld struct {
	e   *Engine
	ids *protocol.IDGen
}

func newEngineRelay(e *Engine) *terminalRelay {
	return newTerminalRelay(engineRelayWorld{e: e, ids: protocol.NewIDGen(e.clk)})
}

var _ relayWorld = engineRelayWorld{}

func (w engineRelayWorld) router() (*machineRouter, error) {
	m := w.e.mach.Load()
	if m == nil {
		return nil, fmt.Errorf("%w: this Citadel hosts no orchestration", characters.ErrNoCharacter)
	}
	return m, nil
}

func (w engineRelayWorld) ByCharacter(ctx context.Context, id string) (characters.Character, error) {
	m, err := w.router()
	if err != nil {
		return characters.Character{}, err
	}
	return m.reg.Get(ctx, id)
}

func (w engineRelayWorld) Alive(ctx context.Context) ([]characters.Character, error) {
	m, err := w.router()
	if err != nil {
		return nil, err
	}
	return m.reg.Alive(ctx)
}

func (w engineRelayWorld) ByTerminal(ctx context.Context, terminal string) (characters.Character, error) {
	m, err := w.router()
	if err != nil {
		return characters.Character{}, err
	}
	return m.reg.ByTerminal(ctx, terminal)
}

func (w engineRelayWorld) Local() string { return w.e.id.MachineID }

func (w engineRelayWorld) LocalSource() termstream.Source {
	if w.e.rt == nil {
		return nil
	}
	return runtimeSourceFor(w.e.rt.rt)
}

func (w engineRelayWorld) Connected(machine string) bool {
	srv := w.e.srvp.Load()
	return srv != nil && srv.Link(machine).Connected()
}

func (w engineRelayWorld) SendTo(ctx context.Context, machine, typ string, payload any) error {
	m, err := w.router()
	if err != nil {
		return err
	}
	l, err := m.link(machine)
	if err != nil {
		return err
	}
	_, err = l.Send(ctx, protocol.Outgoing{Type: typ, Payload: payload, Transient: true})
	return err
}

func (w engineRelayWorld) TypeKeys(ctx context.Context, machine, character, terminal string, data []byte) error {
	m, err := w.router()
	if err != nil {
		return err
	}
	return m.TypeKeys(ctx, machine, character, terminal, data)
}

func (w engineRelayWorld) Screen(ctx context.Context, machine, character string) (characters.Screen, error) {
	m, err := w.router()
	if err != nil {
		return characters.Screen{}, err
	}
	return m.ScreenLines(ctx, machine, character, 0)
}

func (w engineRelayWorld) Now() time.Time { return w.e.clk.Now() }

func (w engineRelayWorld) NewStream() protocol.ULID { return w.ids.New() }

// TypeKeys types what a person typed on the keyboard of the panel into the
// terminal of a character: the Runtime of this machine, or the Tower over its
// connection, in the mode that never goes through the inbox and does not refuse a
// busy character (the panel asked the person to confirm).
func (m *machineRouter) TypeKeys(ctx context.Context, machine, character, terminal string, data []byte) error {
	if machine == m.local {
		if m.rt == nil {
			return &characters.MachineUnreachableError{Machine: machine}
		}
		return m.rt.Type(ctx, character, data)
	}
	_, err := m.call(ctx, machine, towerCallWait, protocol.Outgoing{Type: protocol.TypeTerminalInput,
		Payload: protocol.TerminalInput{TerminalID: terminal, Mode: protocol.InputKeys, Data: string(data)}})
	return err
}

// relayHooks chains the terminal relay into the hooks of the protocol server of
// the period: the relay is made with the panel, which may come later (or never).
func (e *Engine) relayHooks(base protocol.Hooks) protocol.Hooks {
	h := base
	h.OnReady = func(s *protocol.Session) {
		if base.OnReady != nil {
			base.OnReady(s)
		}
		if r := e.relay.Load(); r != nil {
			go r.MachineUp(context.Background(), s.Peer().ID)
		}
	}
	h.OnDisconnect = func(s *protocol.Session, cause error) {
		if base.OnDisconnect != nil {
			base.OnDisconnect(s, cause)
		}
		if r := e.relay.Load(); r != nil {
			r.MachineDown(s.Peer().ID)
		}
	}
	return h
}

// registerCitadelTerminals puts in the router of the Citadel what the Towers
// send of the terminals: the frames 0x01 of the streams browsers asked for, and
// terminal.replayed.
func (e *Engine) registerCitadelTerminals(router *protocol.Router, run *citadelRun) {
	router.HandleData(protocol.DataTerminal, protocol.DataHandlerFunc(func(ctx context.Context, peer protocol.Peer, f protocol.DataFrame) {
		if r := e.relay.Load(); r != nil {
			r.OnFrame(ctx, peer, f)
		}
	}))
	router.HandleFunc(protocol.TypeTerminalReplayed, run.gated(func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.TerminalReplayed)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "terminal.replayed: %T", m.Payload)
		}
		if r := e.relay.Load(); r != nil {
			r.OnReplayed(m.Peer, *p)
		}
		return nil, nil
	}))
}
