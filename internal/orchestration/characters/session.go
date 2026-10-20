package characters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Timeouts of the runtime (docs/orquestracao/personagens.md#estados).
const (
	// DefaultStartTimeout is how long a character may stay in starting.
	DefaultStartTimeout = 120 * time.Second
	// DefaultStuckAfter is how long a busy character may stay without a
	// signal before it is stuck.
	DefaultStuckAfter = 15 * time.Minute
	// DefaultGracefulWait is how long the normal exit of the provider is
	// waited for before the tree is ended.
	DefaultGracefulWait = 30 * time.Second
)

// Errors of the use of a terminal.
var (
	// ErrBusy: the character is busy; nothing is typed.
	ErrBusy = errors.New("the character is busy")
	// ErrPermissionPrompt: the character waits on a native permission prompt,
	// which is decided by the hook and the approval flow, never by keys.
	ErrPermissionPrompt = errors.New("the character waits on a permission prompt")
	// ErrNotAtSafePoint: the character is not in a state it can be stopped from.
	ErrNotAtSafePoint = errors.New("the character is not at a safe point")
)

// firstPrefix marks the delivery id of the Mestre layer typed as the first
// message.
const firstPrefix = "first-message:"

// session is one character on this machine: its process, its state and the
// supervision that moves the state.
type session struct {
	rt *Runtime
	id string
	// launch is how it was started, kept in the machine database.
	launch  Launch
	adapter providers.Adapter
	info    providers.Info
	readyRe *regexp.Regexp

	// cmd is the command that started it, without the secret of the provider,
	// for the restart that follows the trust screen.
	cmd    StartCommand
	hadKey bool

	mu         sync.Mutex
	st         Status
	since      time.Time
	lastSignal time.Time
	proc       Process
	terminal   string
	sessionID  string
	lastTurn   string
	starts     int
	reduced    string
	turns      *providers.Turns
	sup        *providers.Supervisor
	// wait is the approval wait of the character ("approval #3").
	wait string
	// pendingFirst is the text to type once the terminal is ready: the
	// Mestre layer when it travels as the first message, and the resumption
	// message.
	pendingFirst string
	// firstHeld keeps the inbox from delivering anything until the first
	// message is typed: a task never comes before the Mestre layer.
	firstHeld bool
	// inputGiven says that raw input was typed in a blocked terminal: the
	// next screen says whether it is back at the prompt.
	inputGiven bool
	seq        uint64
	stopping   bool
	gone       bool

	stuckTimer, startTimer, delayTimer clock.Timer
	gen                                int
	// stallTimer watches a delivery that must start a turn (deliver.go).
	stallTimer clock.Timer
	stallGen   int
}

func (s *session) now() time.Time { return s.rt.clk.Now() }

// holdsFirst says the first message (the Mestre layer) was not typed yet: no
// delivery of the inbox goes before it.
func (s *session) holdsFirst() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firstHeld
}

// status is the state with its reason.
func (s *session) status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st
}

// applyLocked moves the state along an edge of the diagram, keeps the row of
// the machine, and arms or stops the timers the new state needs. It returns
// the report to send, and false when the diagram has no such edge from the
// state the character is in.
func (s *session) applyLocked(t Trigger, reason string) (Report, bool) {
	to, ok := Next(s.st.State, t)
	if !ok {
		return Report{}, false
	}
	now := s.now()
	s.st, s.since, s.lastSignal = Status{to, reason}, now, now
	if to != Blocked {
		s.inputGiven = false
	}
	s.timersLocked()
	s.persistLocked()
	return s.reportLocked(now), true
}

// reportLocked is the report of the current state.
func (s *session) reportLocked(at time.Time) Report {
	s.seq = s.rt.nextSeq(s.seq)
	r := Report{Character: s.id, Seq: s.seq, State: s.st.State, Reason: s.st.Reason, At: at, Session: s.sessionID, Terminal: s.terminal}
	if s.proc != nil {
		r.PTY = s.proc.PTY()
	}
	red := s.reduced
	r.Reduced = &red
	return r
}

func (s *session) persistLocked() {
	row := RuntimeRow{Agent: s.id, Terminal: s.terminal, Status: s.st, Session: s.sessionID, LastTurn: s.lastTurn, Reduced: s.reduced,
		Starts: s.starts, Launch: s.launch, Since: s.since, LastSeen: s.lastSignal}
	row.Launch.PendingFirst = s.pendingFirst
	if err := s.rt.local.put(context.Background(), row); err != nil {
		s.rt.log.Warn("characters: the state of a character was not kept", "id", s.id, "err", err)
	}
}

// send tells the Citadel and the registry of the terminal; it runs without the
// lock, in the order the reports were made.
func (s *session) send(ctx context.Context, reps ...Report) {
	for _, r := range reps {
		if h := s.rt.o.Host; h != nil && s.terminal != "" {
			_ = h.SetStatus(ctx, s.terminal, string(r.State))
		}
		if err := s.rt.o.Reporter.Report(ctx, r); err != nil {
			s.rt.log.Warn("characters: the Citadel did not take a report", "id", s.id, "state", r.State, "err", err)
		}
	}
}

// do applies one trigger and reports it.
func (s *session) do(ctx context.Context, t Trigger, reason string) bool {
	s.mu.Lock()
	rep, ok := s.applyLocked(t, reason)
	s.mu.Unlock()
	if ok {
		s.send(ctx, rep)
	}
	return ok
}

// steps applies the steps of an event to the state.
func (s *session) doSteps(ctx context.Context, steps []Step) {
	if len(steps) == 0 {
		return
	}
	var reps []Report
	s.mu.Lock()
	for _, st := range steps {
		if r, ok := s.applyLocked(st.Trigger, st.Reason); ok {
			reps = append(reps, r)
		}
	}
	s.mu.Unlock()
	s.send(ctx, reps...)
}

// timersLocked arms the timer the state needs and stops the others: the 120
// seconds of starting, the 15 minutes without a signal of busy.
func (s *session) timersLocked() {
	s.gen++
	gen := s.gen
	stop(s.stuckTimer, s.startTimer)
	s.stuckTimer, s.startTimer = nil, nil
	switch s.st.State {
	case Starting:
		s.startTimer = s.rt.clk.AfterFunc(s.rt.o.StartTimeout, func() { s.startExpired(gen) })
	case Busy:
		s.stuckTimer = s.rt.clk.AfterFunc(s.rt.o.StuckAfter, func() { s.stuckExpired(gen) })
	}
	if s.st.State != Starting {
		stop(s.delayTimer)
		s.delayTimer = nil
	}
}

func stop(ts ...clock.Timer) {
	for _, t := range ts {
		if t != nil {
			t.Stop()
		}
	}
}

func (s *session) startExpired(gen int) {
	s.mu.Lock()
	if gen != s.gen || s.st.State != Starting || s.gone {
		s.mu.Unlock()
		return
	}
	rep, ok := s.applyLocked(TrigStartFailed, fmt.Sprintf("not ready in %s", s.rt.o.StartTimeout))
	s.mu.Unlock()
	if ok {
		s.send(context.Background(), rep)
	}
}

func (s *session) stuckExpired(gen int) {
	s.mu.Lock()
	if gen != s.gen || s.st.State != Busy || s.gone {
		s.mu.Unlock()
		return
	}
	// A signal after the timer was armed moved lastSignal: arm again for the
	// rest.
	if idle := s.now().Sub(s.lastSignal); idle < s.rt.o.StuckAfter {
		s.stuckTimer = s.rt.clk.AfterFunc(s.rt.o.StuckAfter-idle, func() { s.stuckExpired(gen) })
		s.mu.Unlock()
		return
	}
	rep, ok := s.applyLocked(TrigNoSignal, fmt.Sprintf("no signal for %s", s.rt.o.StuckAfter))
	s.mu.Unlock()
	if ok {
		s.send(context.Background(), rep)
	}
}

// signal is any sign of life: an event of the hook or output on the terminal.
// It moves the moment the 15 minutes count from, and brings a stuck
// character back to busy.
func (s *session) signal(ctx context.Context, why string) {
	s.mu.Lock()
	s.lastSignal = s.now()
	if s.st.State != Stuck {
		s.mu.Unlock()
		return
	}
	rep, ok := s.applyLocked(TrigSignal, "signal: "+why)
	s.mu.Unlock()
	if ok {
		s.send(ctx, rep)
	}
}

// waitReason is what a turn that ends should wait for: the approval wait, or
// the active wait of the Citadel.
func (s *session) waitReason(ctx context.Context) string {
	s.mu.Lock()
	w := s.wait
	s.mu.Unlock()
	if w != "" {
		return w
	}
	if p := s.rt.o.Waits; p != nil {
		if reason, ok, err := p.Active(ctx, s.id); err == nil && ok {
			return reason
		}
	}
	return ""
}

// onEvent takes one normalized event of the character.
func (s *session) onEvent(ctx context.Context, ev providers.Event) {
	s.mu.Lock()
	if s.gone {
		s.mu.Unlock()
		return
	}
	var reps []Report
	if ev.SessionID != "" && !providers.ValidSessionID(ev.SessionID) {
		s.rt.log.Warn("characters: an event with a session id that is not one, ignored", "id", s.id)
		ev.SessionID = ""
	}
	if ev.SessionID != "" && ev.SessionID != s.sessionID {
		s.sessionID = ev.SessionID
		s.persistLocked()
		r := s.reportLocked(s.now())
		reps = append(reps, r)
	}
	if ev.TurnID != "" {
		s.lastTurn = ev.TurnID
	}
	sup := s.sup
	s.mu.Unlock()
	if sup != nil {
		sup.Hook(ev)
	}
	s.send(ctx, reps...)
	s.signal(ctx, string(ev.Event))
	if ev.Event == providers.EventNone {
		return
	}
	if ev.Event == providers.EventTurnStart {
		s.mu.Lock()
		s.wait = "" // the decision that woke the character came with this turn
		s.mu.Unlock()
		s.turnBegan()
	}
	if ev.Event == providers.EventTurnEnd && s.rt.o.Turns != nil {
		// Before the state changes: the ask this turn answered is resolved
		// before the next item of the inbox is delivered.
		if err := s.rt.o.Turns.TurnEnded(ctx, s.id, ev.LastMessage); err != nil {
			s.rt.log.Warn("characters: the end of a turn was not reported", "id", s.id, "err", err)
		}
	}
	wait := ""
	if ev.Event == providers.EventTurnEnd || ev.Event == providers.EventInterrupt || ev.Event == providers.EventTurnFailure {
		wait = s.waitReason(ctx)
	}
	st := s.status()
	steps := HookSteps(st, ev, wait)
	s.doSteps(ctx, steps)
	if ev.Event == providers.EventSessionStart {
		s.becameReady(ctx)
	}
}

// becameReady types what waits for the terminal to be ready, once.
func (s *session) becameReady(ctx context.Context) {
	s.mu.Lock()
	text, sup := s.pendingFirst, s.sup
	if s.st.State != Idle || text == "" || sup == nil { // not ready, nothing to type, or the terminal is not ours yet
		s.mu.Unlock()
		return
	}
	s.pendingFirst = ""
	s.mu.Unlock()
	s.typeFirst(ctx, sup, text)
}

// typeFirst delivers the Mestre layer (or the resumption message) as the
// first message; if it cannot be typed, the character is stuck: it does not
// get a task without the layer.
func (s *session) typeFirst(ctx context.Context, sup *providers.Supervisor, text string) {
	s.do(ctx, TrigDeliver, "first message")
	s.mu.Lock()
	s.firstHeld = false
	s.mu.Unlock()
	if err := sup.Deliver(ctx, firstPrefix+s.id, text); err != nil {
		s.rt.log.Warn("characters: the first message was not typed", "id", s.id, "err", err)
		if s.do(ctx, TrigNoSignal, "the Mestre layer could not be delivered: "+err.Error()) {
			s.rt.tellTableMaster(ctx, s, NoticeUnconfirmed, "A camada do Mestre não pôde ser entregue a "+s.launch.Name+"; ele não recebe tarefa sem ela.")
		}
	}
}

// screen takes the screen of a stable terminal: the supervision reads it,
// and the readiness of the base decides the start and, for a simple terminal,
// the end of each command.
func (s *session) screen(ctx context.Context) {
	s.mu.Lock()
	proc, sup, gone := s.proc, s.sup, s.gone
	s.mu.Unlock()
	if proc == nil || gone {
		return
	}
	sc, err := proc.Screen(ctx)
	if err != nil {
		s.rt.log.Warn("characters: the screen was not read", "id", s.id, "err", err)
		return
	}
	s.signal(ctx, "output")
	if sup != nil {
		sup.ScreenAt(sc.Text, true, sc.Row)
	}
	s.mu.Lock()
	st, given := s.st.State, s.inputGiven
	pattern := s.launch.Ready == string(presets.ReadyPattern)
	null := s.launch.Provider == providers.Null
	s.mu.Unlock()
	res := s.adapter.Screens(providers.ScreenInput{Text: sc.Text, Stable: true, SessionStarted: st != Starting, Begun: st != Starting,
		CursorRow: sc.Row, CursorKnown: true, ReadyPattern: s.readyRe})
	switch {
	case st == Starting && pattern && res.State == providers.StateReady:
		s.ready(ctx)
	case st == Blocked && given && res.Known == nil:
		switch res.State {
		case providers.StateReady:
			s.do(ctx, TrigUnblockIdle, "input given, back at the prompt")
		case providers.StateBusy:
			s.do(ctx, TrigUnblockBusy, "input given")
		}
	case null && st == Busy && res.State == providers.StateReady:
		s.do(ctx, TrigTurnEnd, "prompt back")
	}
}

// ready is the start that ended: starting to idle, and the first message.
func (s *session) ready(ctx context.Context) {
	if s.do(ctx, TrigReady, "ready") {
		s.becameReady(ctx)
	}
}

// ended is the process that ended without being asked to.
func (s *session) ended(ctx context.Context, reason string) {
	s.mu.Lock()
	if s.stopping || s.gone {
		s.mu.Unlock()
		return
	}
	rep, ok := s.applyLocked(TrigDied, reason)
	sup := s.sup
	s.mu.Unlock()
	if sup != nil {
		sup.Close()
	}
	if ok {
		s.send(ctx, rep)
	}
}

// close stops everything the session runs.
func (s *session) close() {
	s.mu.Lock()
	s.gone = true
	s.gen++
	stop(s.stuckTimer, s.startTimer, s.delayTimer, s.stallTimer)
	sup := s.sup
	s.mu.Unlock()
	if sup != nil {
		sup.Close()
	}
}

// --- what the supervision of the adapter drives ---

type sessionChars struct{ s *session }

// Block implements providers.Characters.
func (c sessionChars) Block(ctx context.Context, _ string, reason providers.BlockReason, detail string) error {
	why := ReasonInput
	if reason == providers.BlockQuota {
		why = ReasonQuota
	}
	s := c.s
	st := s.status().State
	if st == Idle || st == Waiting {
		// A screen that asks for something is not an idle character.
		s.do(ctx, TrigDeliver, "screen: "+detail)
		st = Busy
	}
	trig := TrigBlock
	if st == Starting {
		trig = TrigStartInput
	}
	if !s.do(ctx, trig, why) {
		return nil
	}
	s.mu.Lock()
	s.inputGiven = false
	s.mu.Unlock()
	return nil
}

// ReduceMonitoring implements providers.Characters.
func (c sessionChars) ReduceMonitoring(ctx context.Context, _ string, reason providers.ReducedReason, _ string) error {
	s := c.s
	s.mu.Lock()
	s.reduced = string(reason)
	s.persistLocked()
	rep := s.reportLocked(s.now())
	s.mu.Unlock()
	s.send(ctx, rep)
	return nil
}

type sessionMaster struct{ s *session }

// Notify implements providers.TableMaster.
func (m sessionMaster) Notify(ctx context.Context, n providers.Notice) error {
	kind, text := NoticeBlockedInput, fmt.Sprintf("%s está parado numa tela que pede resposta; use `check` para vê-la e `ask --raw` para responder.", m.s.launch.Name)
	if n.Kind == providers.NoticeUnconfirmed {
		kind, text = NoticeUnconfirmed, fmt.Sprintf("A entrega %s a %s não foi confirmada; ela não é digitada de novo.", n.DeliveryID, m.s.launch.Name)
	}
	return m.s.rt.o.Notifier.Notify(ctx, Notice{Kind: kind, To: Recipient{Kind: ToTableMaster}, Workspace: m.s.launch.Workspace, Table: m.s.launch.Table,
		Character: m.s.id, Name: m.s.launch.Name, Screen: n.Screen, Text: text + " " + n.Detail})
}

type sessionInbox struct{ s *session }

// Confirmed implements providers.Inbox.
func (i sessionInbox) Confirmed(ctx context.Context, id string) error {
	if strings.HasPrefix(id, firstPrefix) || i.s.rt.o.Inbox == nil {
		return nil
	}
	return i.s.rt.o.Inbox.Confirmed(ctx, id)
}

// Unconfirmed implements providers.Inbox: the first message that never started a
// turn leaves the character stuck.
func (i sessionInbox) Unconfirmed(ctx context.Context, id string) error {
	if strings.HasPrefix(id, firstPrefix) {
		i.s.do(ctx, TrigNoSignal, "the Mestre layer was not confirmed by a turn")
		return nil
	}
	if i.s.rt.o.Inbox == nil {
		return nil
	}
	return i.s.rt.o.Inbox.Unconfirmed(ctx, id)
}

type sessionKeyboard struct{ s *session }

// Input implements providers.Keyboard.
func (k sessionKeyboard) Input(ctx context.Context, data []byte) error {
	k.s.mu.Lock()
	p := k.s.proc
	k.s.mu.Unlock()
	if p == nil {
		return noProcess(k.s.id)
	}
	return p.Input(ctx, data)
}

type sessionReader struct{ s *session }

// ReadScreen implements providers.ScreenReader: the screen as it is now.
func (r sessionReader) ReadScreen(ctx context.Context) (string, error) {
	r.s.mu.Lock()
	p := r.s.proc
	r.s.mu.Unlock()
	if p == nil {
		return "", noProcess(r.s.id)
	}
	sc, err := p.Screen(ctx)
	return sc.Text, err
}

type nopChecklist struct{}

func (nopChecklist) Alert(context.Context, providers.Alert) error { return nil }

// readyRegexp compiles the pattern of a base that is ready by pattern.
func readyRegexp(l Launch) *regexp.Regexp {
	if l.Ready != string(presets.ReadyPattern) || l.ReadyPattern == "" {
		return nil
	}
	re, err := regexp.Compile(l.ReadyPattern)
	if err != nil {
		return nil
	}
	return re
}

// launchOf is what is kept of how a request starts a character.
func launchOf(cmd StartCommand, plan sessionzero.CharacterPlan, provider providers.ID) Launch {
	req := cmd.Request
	b := req.Snapshot.Base
	l := Launch{Workspace: req.Workspace, Table: req.Table.ID, Name: req.Name, Role: req.Role, Kind: cmd.Kind, Machine: req.Machine,
		Dir: req.Dir, Tmp: plan.Tmp, Provider: provider, Ready: string(b.Ready.Mode), ReadyPattern: b.Ready.Pattern, ReadyDelay: b.Ready.Delay}
	if req.Class != nil {
		l.Class = req.Class.Name
	}
	// The request travels in the row so the session zero can be brought back
	// after a restart of the engine; the key of the provider never does.
	req.ProviderKey = nil
	if raw, err := json.Marshal(req); err == nil {
		l.Request = raw
	}
	return l
}

// providerOfAgent maps the agent type of a sheet to the provider.
func providerOfAgent(agent string) providers.ID {
	switch agent {
	case presets.AgentClaudeCode:
		return providers.ClaudeCode
	case presets.AgentCodex:
		return providers.Codex
	case presets.AgentAntigravity:
		return providers.Antigravity
	}
	return providers.Null
}

// exitInput is what the adapter types to ask the provider for its normal
// exit. The commands are the ones the three CLIs document; the conformance
// test of a version that changes one shows it as a step that falls to the
// signal.
func exitInput(p providers.ID) []byte {
	switch p {
	case providers.Null:
		return []byte("exit\r")
	case providers.Antigravity:
		return []byte("/quit\r")
	}
	return []byte("/exit\r")
}
