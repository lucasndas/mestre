package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/rituals"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
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
	fmu    sync.Mutex
	flying map[string]int
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
		return nil, fmt.Errorf("%w: %s did not answer %s: %w", errNoAnswer, machine, o.Type, err)
	case res.Status == protocol.AckError && res.Error != nil:
		return nil, remoteError(machine, res.Error)
	}
	return res.Result, nil
}

// errNoAnswer marks the call that got no answer in time (or was cancelled): the
// Tower may have done it.
var errNoAnswer = errors.New("no answer")

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
	m.markInFlight(cmd.Character, 1)
	defer m.markInFlight(cmd.Character, -1)
	ans, err := m.call(ctx, machine, towerStartWait, protocol.Outgoing{Type: protocol.TypeCharacterStart, Payload: protocol.CharacterStart{
		Character: cmd.Character, TerminalID: cmd.Terminal, Kind: string(cmd.Kind), Resumed: cmd.Resumed, ResumeMessage: cmd.ResumeMessage, Request: req,
	}})
	if errors.Is(err, errNoAnswer) {
		// The Citadel gave up, but the Tower may still be starting it, and a terminal
		// nobody waits for would be left alive: it is told to end it. The stop goes
		// behind the start on the same line, so it finds what the start made.
		sctx, cancel := clock.WithTimeout(context.WithoutCancel(ctx), m.clk, towerCallWait)
		defer cancel()
		_, _ = m.call(sctx, machine, towerCallWait, protocol.Outgoing{Type: protocol.TypeCharacterStop,
			Payload: protocol.CharacterStop{Character: cmd.Character, TerminalID: cmd.Terminal, Final: !cmd.Resumed, Reason: "the Citadel gave up on the start", Abandon: true}})
	}
	if err != nil {
		return characters.StartResult{}, err
	}
	// A class with hooks starts only on a Tower that confirms it applies the same hooks: a Tower
	// that does not say so (an old one) or says another package would run it without them.
	if c := cmd.Request.Class; c != nil && len(c.Hooks) > 0 {
		if why := hooksNotConfirmed(ans, c.Hash); why != "" {
			sctx, cancel := clock.WithTimeout(context.WithoutCancel(ctx), m.clk, towerCallWait)
			defer cancel()
			_, _ = m.call(sctx, machine, towerCallWait, protocol.Outgoing{Type: protocol.TypeCharacterStop,
				Payload: protocol.CharacterStop{Character: cmd.Character, TerminalID: cmd.Terminal, Final: !cmd.Resumed, Reason: why, Abandon: true}})
			return characters.StartResult{}, fmt.Errorf("%w: %s", errHooksUnconfirmed, why)
		}
	}
	return characters.StartResult{Terminal: cmd.Terminal}, nil
}

// errHooksUnconfirmed is why a character whose class has hooks was stopped on a Tower.
var errHooksUnconfirmed = errors.New("the Tower did not confirm the hooks of the class")

// hooksNotConfirmed says why the answer of character.start does not confirm the hooks of the
// class (hash is the one of its package), or "" when it does.
func hooksNotConfirmed(ans json.RawMessage, hash string) string {
	var res protocol.CharacterStartResult
	_ = json.Unmarshal(ans, &res)
	switch {
	case res.ClassHooks == "":
		return "the Tower did not say it applies the hooks of the class (a version that does not run them)"
	case hash != "" && res.ClassHooks != hash:
		return "the Tower applies another package of hooks than the one of the class"
	}
	return ""
}

// markInFlight counts the starts of a character on their way to a Tower;
// inFlight says whether one is.
func (m *machineRouter) markInFlight(id string, d int) {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	if m.flying == nil {
		m.flying = map[string]int{}
	}
	if m.flying[id] += d; m.flying[id] <= 0 {
		delete(m.flying, id)
	}
}

func (m *machineRouter) inFlight(id string) bool {
	m.fmu.Lock()
	defer m.fmu.Unlock()
	return m.flying[id] > 0
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

// startGrace is how long a character that says starting in the registry is
// taken as a start on its way, whatever the Tower's list says: the row is made
// before the command goes, and the start takes the time it takes.
const startGrace = towerStartWait + 30*time.Second

// pictureSlack is how old the hour of a picture of a Tower may be taken to be.
const pictureSlack = 2 * time.Minute

// resyncFromTower takes the list of the terminals a Tower tells at each
// connection (taken at `at`) as the truth about its processes: the characters
// the registry has alive there that the list lacks are interrupted, except a
// start on its way and a character that changed on the Citadel after the
// picture was taken; and the terminals the Tower has that the Citadel does not
// have there are answered as the ones to end (without erasing their files).
func (e *Engine) resyncFromTower(ctx context.Context, orc *Orchestration, machine string, terminals []protocol.TerminalState, at time.Time) (end []string, err error) {
	live := e.liveWithWaits(ctx, orc, terminals)
	mine, err := orc.Registry.OnMachine(ctx, machine)
	if err != nil {
		return nil, err
	}
	now := e.clk.Now()
	// the hour of the picture is the Tower's clock: kept within a few minutes of ours
	if at.Before(now.Add(-pictureSlack)) {
		at = now.Add(-pictureSlack)
	}
	if at.After(now) {
		at = now
	}
	mr := e.mach.Load()
	inFlight := func(c characters.Character) bool {
		return mr != nil && mr.inFlight(c.ID) || c.State == characters.Starting && now.Sub(c.StartedAt) < startGrace
	}
	listed := func(id string) bool {
		return slices.ContainsFunc(live, func(t protocol.TerminalState) bool { return t.AgentID == id })
	}
	for _, c := range mine {
		// not judged by the list: a start on its way, a change made after the picture
		if !listed(c.ID) && (c.State == characters.Starting && inFlight(c) || c.LastSignalAt.After(at)) {
			live = append(live, protocol.TerminalState{TerminalID: c.TerminalID, AgentID: c.ID, State: string(c.State)})
		}
	}
	if err := orc.Characters.Reconcile(ctx, machine, live); err != nil {
		return nil, err
	}
	// The Tower has a terminal the registry has not on that machine (a start the
	// Citadel gave up on, a character that ended): the Tower ends it. Only what the
	// registry says plainly: a character it does not know may be one a replica
	// that is behind (a promotion) has not got yet, so it is left, and none of
	// them is erased.
	for _, t := range terminals {
		if t.AgentID == "" {
			continue
		}
		c, err := orc.Registry.Get(ctx, t.AgentID)
		if err != nil && !errors.Is(err, characters.ErrNoCharacter) {
			e.log.Warn("app: the registry could not be read for a terminal of a Tower", "machine", machine, "id", t.AgentID, "err", err)
		}
		switch {
		case err != nil || inFlight(c):
		case c.State == characters.Stopped || c.Machine != machine:
			end = append(end, t.AgentID)
		}
	}
	return end, nil
}

// liveWithWaits turns the idle of a list of terminals into waiting for the
// characters that have an active wait. A wait that cannot be read leaves the state as it was told.
func (e *Engine) liveWithWaits(ctx context.Context, orc *Orchestration, in []protocol.TerminalState) []protocol.TerminalState {
	out := make([]protocol.TerminalState, 0, len(in))
	for _, t := range in {
		if t.State == string(characters.Idle) {
			switch _, active, err := orc.Waits.Active(ctx, t.AgentID); {
			case err != nil:
				e.log.Warn("app: the waits of a character could not be read; its state stays as told", "id", t.AgentID, "err", err)
			case active:
				t.State = string(characters.Waiting)
			}
		}
		out = append(out, t)
	}
	return out
}

// withWait turns the idle a Tower reports into waiting when the character has
// an active wait: the waits live in the database of the Citadel, which a Tower
// cannot ask, and a character that waits is not an idle one.
func (e *Engine) withWait(ctx context.Context, orc *Orchestration, r characters.Report) (characters.Report, error) {
	if r.State != characters.Idle {
		return r, nil
	}
	reason, active, err := orc.Waits.Active(ctx, r.Character)
	if err != nil {
		return r, err // the Tower sends the report again: an idle that waits is not told as idle
	}
	if active {
		r.State, r.Reason = characters.Waiting, reason
	}
	return r, nil
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
		r, err := e.withWait(ctx, orc, r)
		if err != nil {
			return nil, protocol.NewError(protocol.CodeInternal, "terminal.status: the waits: %v", err)
		}
		if err := orc.Characters.Report(ctx, r); err != nil {
			return nil, protocol.NewError(protocol.CodeInternal, "terminal.status: %v", err)
		}
		return nil, nil
	})
	h(protocol.TypeCharacterResync, func(ctx context.Context, orc *Orchestration, m *protocol.Message) (*protocol.Reply, error) {
		p, ok := m.Payload.(*protocol.CharacterResync)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "character.resync: %T", m.Payload)
		}
		at := e.clk.Now()
		if p.At != nil {
			at = p.At.Time
		}
		end, err := e.resyncFromTower(ctx, orc, m.Peer.ID, p.Terminals, at)
		if err != nil {
			return nil, protocol.NewError(protocol.CodeInternal, "character.resync: %v", err)
		}
		// what the resync left interrupted is resumed once this answer is given
		e.resumes.request(m.Peer.ID)
		return &protocol.Reply{Type: protocol.TypeCharacterResync, InAck: true, Payload: protocol.CharacterResyncResult{End: end}}, nil
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

// Run implements rituals.PreRunner: the command runs on the machine of the
// target, as the target, in its folder.
func (m *machineRouter) Run(ctx context.Context, r rituals.PreRunRequest) (rituals.PreRunResult, error) {
	if r.Machine == m.local {
		if m.rt == nil {
			return rituals.PreRunResult{}, errTerminalsNotWired
		}
		out, err := m.rt.PreRun(ctx, r.Character, r.Command, preRunTimeout(r.Timeout), r.MaxOutput)
		return rituals.PreRunResult{Output: out.Output, ExitCode: out.ExitCode, TimedOut: out.TimedOut, Stderr: out.Stderr}, err
	}
	// the Tower runs for the time asked, as bounded here, and the Citadel waits for it plus a margin
	timeout := preRunTimeout(r.Timeout)
	raw, err := m.call(ctx, r.Machine, timeout+towerCallWait, protocol.Outgoing{Type: protocol.TypeRitualPreRun, Payload: protocol.RitualPreRun{
		Character: r.Character, Command: r.Command, TimeoutSeconds: int(timeout / time.Second), MaxOutput: r.MaxOutput}})
	if err != nil {
		return rituals.PreRunResult{}, err
	}
	var res protocol.RitualPreRunResult
	if err := json.Unmarshal(raw, &res); err != nil {
		return rituals.PreRunResult{}, fmt.Errorf("%s answered the pre-run with something that is not a result", r.Machine)
	}
	return rituals.PreRunResult{Output: res.Output, ExitCode: res.ExitCode, Stderr: res.Stderr, TimedOut: res.TimedOut}, nil
}

var (
	_ rituals.PreRunner = (*machineRouter)(nil)
	_ scrolls.Copies    = (*machineRouter)(nil)
)

// Put implements scrolls.Copies: the copy of a scroll in the worktree of a
// table, on this machine or on a Tower.
func (m *machineRouter) Put(ctx context.Context, t scrolls.Target, rel string, data []byte) error {
	if t.Machine == m.local {
		return scrolls.LocalCopies{Machine: m.local}.Put(ctx, t, rel, data)
	}
	_, err := m.call(ctx, t.Machine, towerCallWait, protocol.Outgoing{Type: protocol.TypeScrollPut, Payload: protocol.ScrollPut{Dir: t.Dir, Rel: rel, Content: string(data)}})
	return err
}

// Remove implements scrolls.Copies.
func (m *machineRouter) Remove(ctx context.Context, t scrolls.Target, rel string) error {
	if t.Machine == m.local {
		return scrolls.LocalCopies{Machine: m.local}.Remove(ctx, t, rel)
	}
	_, err := m.call(ctx, t.Machine, towerCallWait, protocol.Outgoing{Type: protocol.TypeScrollRemove, Payload: protocol.ScrollRemove{Dir: t.Dir, Rel: rel}})
	return err
}

// The time a pre-run may take: asked as a whole number of seconds, between one
// second and ten minutes, the same on the Citadel and on the Tower.
const (
	preRunMin = time.Second
	preRunMax = 10 * time.Minute
)

func preRunTimeout(d time.Duration) time.Duration {
	return min(max(d.Round(time.Second), preRunMin), preRunMax)
}
