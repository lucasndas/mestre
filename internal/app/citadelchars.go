package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/pkg/clock"
)

// How long the Citadel waits for a Tower: the start prepares the files and
// verifies the command; the rest is a conversation.
const (
	towerStartWait = 90 * time.Second
	towerCallWait  = 20 * time.Second
)

// machineRouter is how the Citadel of a period reaches the engine of each
// machine that runs characters: the Runtime of this machine in process, a
// Tower over its connection.
type machineRouter struct {
	towerClient
	local string
	// rt is the Runtime of this machine; nil when its terminals are not wired.
	rt  *characters.Runtime
	reg characters.Registry
}

var _ OrchestrationMachines = (*machineRouter)(nil)

func (m *machineRouter) set() characters.MachineSet {
	if m.rt == nil {
		return characters.MachineSet{}
	}
	return characters.MachineSet{m.local: m.rt}
}

// replies are the answers the Citadel waits for that come as messages of
// their own (reply_to the id of the command): the end of a table.done. Each is
// kept with the machine it must come from.
type replies struct {
	mu sync.Mutex
	m  map[string]reply
}

type reply struct {
	machine string
	ch      chan any
}

func (r *replies) expect(id, machine string) (<-chan any, func()) {
	ch := make(chan any, 1)
	r.mu.Lock()
	if r.m == nil {
		r.m = map[string]reply{}
	}
	r.m[id] = reply{machine: machine, ch: ch}
	r.mu.Unlock()
	return ch, func() {
		r.mu.Lock()
		delete(r.m, id)
		r.mu.Unlock()
	}
}

// deliver gives the answer to whoever waits for it, once, and says whether
// someone did; an answer from another machine than the one asked is nobody's.
func (r *replies) deliver(id, machine string, v any) bool {
	r.mu.Lock()
	w, ok := r.m[id]
	if ok && w.machine == machine {
		delete(r.m, id)
	} else {
		ok = false
	}
	r.mu.Unlock()
	if ok {
		w.ch <- v // buffered: nobody else sends
	}
	return ok
}

// towerClient reaches the Towers of the Citadel of the period.
type towerClient struct {
	srv *atomic.Pointer[protocol.Server]
	clk clock.Clock
	// ids make the ids of the commands whose end comes as a message; rep waits for it.
	ids *protocol.IDGen
	rep *replies
}

// link is the open connection to a Tower, or the error of a machine that cannot
// be reached.
func (m towerClient) link(machine string) (*protocol.Link, error) {
	srv := m.srv.Load()
	if srv == nil {
		return nil, &characters.MachineUnreachableError{Machine: machine}
	}
	l := srv.Link(machine)
	if !l.Connected() {
		return nil, &characters.MachineUnreachableError{Machine: machine}
	}
	return l, nil
}

// call sends a command that asks for an ack, now and only on the open
// connection (a command that waits for a Tower to come back would run after
// the Citadel gave up on it), and turns the refusal into the error it means.
func (m towerClient) call(ctx context.Context, machine string, wait time.Duration, o protocol.Outgoing) (json.RawMessage, error) {
	l, err := m.link(machine)
	if err != nil {
		return nil, err
	}
	o.Transient = true
	ctx, cancel := clock.WithTimeout(ctx, m.clk, wait)
	defer cancel()
	res, err := l.Call(ctx, o)
	switch {
	case errors.Is(err, protocol.ErrNotConnected):
		return nil, &characters.MachineUnreachableError{Machine: machine}
	case err != nil:
		return nil, fmt.Errorf("%s did not answer %s: %w", machine, o.Type, err)
	case res.Status == protocol.AckError && res.Error != nil:
		return nil, remoteError(machine, res.Error)
	}
	return res.Result, nil
}

// remoteError is the refusal of a Tower as the Citadel's callers know it.
func remoteError(machine string, e *protocol.Error) error {
	if e.Code == protocol.CodeBusyTerminal {
		if strings.Contains(e.Message, permissionPromptMark) {
			return fmt.Errorf("%w: %s", characters.ErrPermissionPrompt, e.Message)
		}
		return fmt.Errorf("%w: %s", characters.ErrBusy, e.Message)
	}
	return fmt.Errorf("%s: %s", machine, e.Message)
}

// Start implements characters.Machines.
func (m *machineRouter) Start(ctx context.Context, machine string, cmd characters.StartCommand) (characters.StartResult, error) {
	if machine == m.local {
		if m.rt == nil {
			return characters.StartResult{}, errTerminalsNotWired
		}
		return m.rt.Start(ctx, cmd)
	}
	raw, err := json.Marshal(cmd.Request)
	if err != nil {
		return characters.StartResult{}, fmt.Errorf("the request of %s: %w", cmd.Character, err)
	}
	var req protocol.Object
	if err := json.Unmarshal(raw, &req); err != nil {
		return characters.StartResult{}, fmt.Errorf("the request of %s: %w", cmd.Character, err)
	}
	_, err = m.call(ctx, machine, towerStartWait, protocol.Outgoing{Type: protocol.TypeCharacterStart, Payload: protocol.CharacterStart{
		Character: cmd.Character, TerminalID: cmd.Terminal, Kind: string(cmd.Kind), Resumed: cmd.Resumed, ResumeMessage: cmd.ResumeMessage, Request: req,
	}})
	if err != nil {
		return characters.StartResult{}, err
	}
	return characters.StartResult{Terminal: cmd.Terminal}, nil
}

// Stop implements characters.Machines.
func (m *machineRouter) Stop(ctx context.Context, machine string, cmd characters.StopCommand) error {
	if machine == m.local {
		return m.set().Stop(ctx, machine, cmd)
	}
	_, err := m.call(ctx, machine, towerCallWait, protocol.Outgoing{Type: protocol.TypeCharacterStop,
		Payload: protocol.CharacterStop{Character: cmd.Character, Final: cmd.Final, Reason: cmd.Reason}})
	return err
}

// Deliver implements inbox.Deliverer.
func (m *machineRouter) Deliver(ctx context.Context, machine string, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	if machine == m.local {
		return m.set().Deliver(ctx, machine, d)
	}
	raw, err := m.call(ctx, machine, towerCallWait, protocol.Outgoing{Type: protocol.TypeInboxDeliver, Payload: d})
	if err != nil {
		return protocol.InboxResult{}, err
	}
	var res protocol.InboxResult
	if err := json.Unmarshal(raw, &res); err != nil || res.ItemID != d.ItemID || res.Validate() != nil {
		return protocol.InboxResult{}, fmt.Errorf("%s answered the delivery of %s with %s", machine, d.ItemID, raw)
	}
	return res, nil
}

// terminalOf is the terminal a character runs in, by the registry.
func (m *machineRouter) terminalOf(ctx context.Context, machine, character string) (string, error) {
	c, err := m.reg.Get(ctx, character)
	if err != nil {
		return "", err
	}
	if c.Machine != machine || c.TerminalID == "" {
		return "", fmt.Errorf("%s has no terminal on %s", character, machine)
	}
	return c.TerminalID, nil
}

// Input implements waits.Terminals.
func (m *machineRouter) Input(ctx context.Context, machine, character string, data []byte) error {
	if machine == m.local {
		return m.set().Input(ctx, machine, character, data)
	}
	term, err := m.terminalOf(ctx, machine, character)
	if err != nil {
		return err
	}
	_, err = m.call(ctx, machine, towerCallWait, protocol.Outgoing{Type: protocol.TypeTerminalInput,
		Payload: protocol.TerminalInput{TerminalID: term, Mode: protocol.InputRaw, Data: string(data)}})
	return err
}

// Screen implements waits.Terminals.
func (m *machineRouter) Screen(ctx context.Context, machine, character string) (characters.Screen, error) {
	return m.ScreenLines(ctx, machine, character, 0)
}

// ScreenLines implements waits.HistoryTerminals.
func (m *machineRouter) ScreenLines(ctx context.Context, machine, character string, lines int) (characters.Screen, error) {
	if machine == m.local {
		return m.set().ScreenLines(ctx, machine, character, lines)
	}
	term, err := m.terminalOf(ctx, machine, character)
	if err != nil {
		return characters.Screen{}, err
	}
	raw, err := m.call(ctx, machine, towerCallWait, protocol.Outgoing{Type: protocol.TypeTerminalScreen, Payload: protocol.TerminalScreen{TerminalID: term, Lines: lines}})
	if err != nil {
		return characters.Screen{}, err
	}
	var res protocol.TerminalScreenResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return characters.Screen{}, fmt.Errorf("%s answered the screen with something that is not a screen", machine)
	}
	sc := characters.Screen{Text: res.Text, History: res.History}
	if raw, ok := res.Cursor["row"]; ok {
		_ = json.Unmarshal(raw, &sc.Row)
	}
	return sc, nil
}

// onMachine returns the character the registry puts on the machine, or the
// refusal of a message that names a character of another: a Tower may speak
// only of its own.
func (e *Engine) onMachine(ctx context.Context, orc *Orchestration, character, machine string) (characters.Character, error) {
	c, err := orc.Registry.Get(ctx, character)
	switch {
	case errors.Is(err, characters.ErrNoCharacter):
		return characters.Character{}, protocol.NewError(protocol.CodeForbidden, "the character %q does not run on %s", character, machine)
	case err != nil:
		return characters.Character{}, protocol.NewError(protocol.CodeInternal, "the registry: %v", err) // retried by the Tower
	case c.Machine != machine:
		return characters.Character{}, protocol.NewError(protocol.CodeForbidden, "the character %q does not run on %s", character, machine)
	}
	return c, nil
}

// citadelHandler runs f only while this machine hosts the orchestration.
func (e *Engine) citadelHandler(f func(ctx context.Context, orc *Orchestration, m *protocol.Message) (*protocol.Reply, error)) func(context.Context, *protocol.Message) (*protocol.Reply, error) {
	return func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		orc := e.orc.Load()
		if orc == nil {
			return nil, protocol.NewError(protocol.CodeUnavailable, "%s: this Citadel hosts no orchestration", m.Env.Type)
		}
		return f(ctx, orc, m)
	}
}

// registerCitadelCharacters puts in the router of the Citadel the handlers of
// what the Towers tell about their characters.
func (e *Engine) registerCitadelCharacters(router *protocol.Router, run *citadelRun) {
	h := func(typ string, f func(ctx context.Context, orc *Orchestration, m *protocol.Message) (*protocol.Reply, error)) {
		router.HandleFunc(typ, run.gated(protocol.HandlerFunc(e.citadelHandler(f))))
	}
	h(protocol.TypeTableNotice, e.handleTableNotice)
	h(protocol.TypeTableSent, func(_ context.Context, _ *Orchestration, m *protocol.Message) (*protocol.Reply, error) {
		if p, ok := m.Payload.(*protocol.TableSent); ok && e.sent.deliver(m.Env.ReplyTo, m.Peer.ID, *p) {
			return nil, nil
		}
		return nil, protocol.NewError(protocol.CodeForbidden, "table.sent: nobody asked for it from %s", m.Peer.ID)
	})
	h(protocol.TypeTerminalStatus, func(ctx context.Context, orc *Orchestration, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.TerminalStatus)
		if !ok || p.AgentID == "" {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "terminal.status: the report of a character names it in agent_id")
		}
		if _, err := e.onMachine(ctx, orc, p.AgentID, m.Peer.ID); err != nil {
			return nil, err
		}
		r := characters.Report{Character: p.AgentID, Seq: p.Seq, State: characters.State(p.State), Reason: p.Reason, Session: p.SessionID, Terminal: p.TerminalID,
			PTY: characters.PTYInfo{PID: p.PTYPid, Started: p.PTYStarted, Version: p.PTYVersion}, Reduced: p.Reduced, Resync: p.Resync}
		if p.At != nil {
			r.At = p.At.Time
		} else {
			r.At = e.clk.Now()
		}
		if err := orc.Characters.Report(ctx, r); err != nil {
			return nil, protocol.NewError(protocol.CodeInternal, "terminal.status: %v", err)
		}
		return nil, nil
	})
	h(protocol.TypeCharacterTurn, func(ctx context.Context, orc *Orchestration, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.CharacterTurn)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "character.turn: %T", m.Payload)
		}
		if _, err := e.onMachine(ctx, orc, p.AgentID, m.Peer.ID); err != nil {
			return nil, err
		}
		if err := orc.Characters.TurnEnded(ctx, p.AgentID, p.LastMessage); err != nil {
			return nil, protocol.NewError(protocol.CodeInternal, "character.turn: %v", err)
		}
		return nil, nil
	})
	h(protocol.TypeCharacterNotice, func(ctx context.Context, orc *Orchestration, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.CharacterNotice)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "character.notice: %T", m.Payload)
		}
		kind := characters.NoticeKind(p.Kind)
		if !noticeKinds[kind] {
			return nil, protocol.NewError(protocol.CodeForbidden, "character.notice: a Tower does not tell %q", p.Kind)
		}
		c, err := e.onMachine(ctx, orc, p.Character, m.Peer.ID)
		if err != nil {
			return nil, err
		}
		// who it is about and who it is for come from the registry, not from the Tower
		err = orc.Inbox.Notify(ctx, characters.Notice{Kind: kind, To: characters.Recipient{Kind: characters.ToTableMaster}, Workspace: c.Workspace,
			Table: c.Table, Character: c.ID, Name: c.Name, Text: p.Text, Screen: p.Screen})
		if err != nil {
			return nil, protocol.NewError(protocol.CodeInternal, "character.notice: %v", err)
		}
		return nil, nil
	})
	h(protocol.TypeInboxResult, func(ctx context.Context, orc *Orchestration, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.InboxResult)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "inbox.result: %T", m.Payload)
		}
		if p.Status != protocol.InboxConfirmed && p.Status != protocol.InboxUnconfirmed {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "inbox.result: %s arrives in the ack of inbox.deliver, not as a message", p.Status)
		}
		// confirmed or unconfirmed: the turn the delivery began, seen on the
		// machine the item's character runs on
		target, err := orc.Inbox.TargetOf(ctx, p.ItemID)
		if err != nil {
			return nil, protocol.NewError(protocol.CodeNotFound, "inbox.result: %v", err)
		}
		if _, err := e.onMachine(ctx, orc, target, m.Peer.ID); err != nil {
			return nil, err
		}
		if p.Status == protocol.InboxConfirmed {
			err = orc.Inbox.Confirmed(ctx, p.ItemID)
		} else {
			err = orc.Inbox.Unconfirmed(ctx, p.ItemID)
		}
		if err != nil {
			return nil, protocol.NewError(protocol.CodeInternal, "inbox.result: %v", err)
		}
		return nil, nil
	})
}

// towerRules sends the effective rules of a character to its Tower
// (agent.rules), where its hooks read them.
type towerRules struct {
	srv *atomic.Pointer[protocol.Server]
}

func (t towerRules) SendRules(ctx context.Context, machine string, msg protocol.Outgoing) error {
	srv := t.srv.Load()
	if srv == nil {
		return &characters.MachineUnreachableError{Machine: machine}
	}
	_, err := srv.Link(machine).Send(ctx, msg)
	return err
}
