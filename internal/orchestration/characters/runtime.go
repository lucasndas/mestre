package characters

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// RuntimeOptions are the pieces of the engine of one machine.
type RuntimeOptions struct {
	// Machine is the id of this machine in the Guild.
	Machine string
	Clock   clock.Clock
	// DB is the machine database (character_runtime).
	DB        *store.DB
	Host      Host
	Prep      Preparer
	Providers *providers.Registry
	Reporter  Reporter
	Notifier  Notifier

	// Tables, Waits, Inbox, Checklist, Decisions and Retrust may be nil: the
	// files of the table are not checked, no wait is active, deliveries are
	// not recorded, no alert is raised, no decision is recorded, the trust
	// screen escalates at once.
	Tables    TableChecker
	Waits     WaitProbe
	Inbox     providers.Inbox
	Checklist providers.Checklist
	Decisions providers.Decisions
	Retrust   Retruster
	// Turns, when set, is told when a turn ends with the last message of the
	// agent (the Citadel's Service, or the protocol to it); nil: nothing is.
	Turns TurnObserver
	// LongText is the size above which a delivery goes to a file (2000
	// characters by default) and StallAfter how long a delivery may wait for
	// the start of its turn before Enter is sent (30 s).
	LongText   int
	StallAfter time.Duration

	// Layout, Home and OS fill the environment the permission pipeline
	// decides with; EnvExtra adds what only the machine knows (the git state,
	// the edits of the table).
	Layout   classes.Layout
	Home, OS string
	EnvExtra func(l Launch, env *classes.Env)

	// StartTimeout, StuckAfter and GracefulWait default to the document's;
	// ScreenTimeout is how long a start screen that nothing knows may stay
	// (30 s, providers.DefaultStartTimeout).
	StartTimeout, StuckAfter, GracefulWait, ScreenTimeout time.Duration
	Logger                                                *slog.Logger
}

// Runtime is the engine of one machine for its characters: it starts them,
// follows their state and ends them, and finds them again after a restart.
// It is the hook.Characters, hook.States and hook.Contexts of the machine and
// the Machines of the Citadel for it.
type Runtime struct {
	o     RuntimeOptions
	clk   clock.Clock
	local LocalStore
	log   *slog.Logger

	mu       sync.Mutex
	sessions map[string]*session
	byTerm   map[string]*session
	lastSeq  uint64
	starting map[string]bool // characters a Start is preparing: one Start at a time
}

var (
	_ hook.Characters = (*Runtime)(nil)
	_ hook.States     = (*Runtime)(nil)
	_ hook.Contexts   = (*Runtime)(nil)
	_ Machines        = MachineSet(nil)
)

// NewRuntime returns the runtime of a machine. Recover brings back what the
// engine had before it went down.
func NewRuntime(o RuntimeOptions) (*Runtime, error) {
	switch {
	case o.Machine == "" || o.DB == nil || o.Host == nil || o.Prep == nil || o.Providers == nil || o.Reporter == nil || o.Notifier == nil:
		return nil, errors.New("characters: the runtime needs the machine, its database, the host, the session zero, the providers, the reporter and the notifier")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.StartTimeout <= 0 {
		o.StartTimeout = DefaultStartTimeout
	}
	if o.StuckAfter <= 0 {
		o.StuckAfter = DefaultStuckAfter
	}
	if o.GracefulWait <= 0 {
		o.GracefulWait = DefaultGracefulWait
	}
	if o.StallAfter <= 0 {
		o.StallAfter = DefaultStallAfter
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if o.Checklist == nil {
		o.Checklist = nopChecklist{}
	}
	rt := &Runtime{o: o, clk: o.Clock, local: LocalStore{DB: o.DB}, log: o.Logger, sessions: map[string]*session{}, byTerm: map[string]*session{}, starting: map[string]bool{}}
	o.Host.Events(rt.onHostEvent)
	return rt, nil
}

// nextSeq gives the number of the next report of a character: time-based, so
// it keeps growing across the restarts of the engine, and strictly increasing.
func (rt *Runtime) nextSeq(prev uint64) uint64 {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	n := uint64(rt.clk.Now().UnixNano())
	if n <= rt.lastSeq {
		n = rt.lastSeq + 1
	}
	if n <= prev {
		n = prev + 1
	}
	rt.lastSeq = n
	return n
}

// Live is the state of every terminal this machine knows, as the Citadel
// reconciles its registry with it (Service.Reconcile) when it takes over the
// reports a machine could not make while no Citadel was there.
func (rt *Runtime) Live() []protocol.TerminalState {
	rt.mu.Lock()
	all := make([]*session, 0, len(rt.sessions))
	for _, s := range rt.sessions {
		all = append(all, s)
	}
	rt.mu.Unlock()
	out := make([]protocol.TerminalState, 0, len(all))
	for _, s := range all {
		st := s.status()
		s.mu.Lock()
		out = append(out, protocol.TerminalState{TerminalID: s.terminal, AgentID: s.id, State: string(st.State), SessionID: s.sessionID})
		s.mu.Unlock()
	}
	return out
}

func (rt *Runtime) session(id string) *session {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	return rt.sessions[id]
}

// Start starts a character: the table's files are checked, the session zero
// prepares it, the command is verified, the terminal is created and the
// supervision begins. Nothing starts if any step fails, and what the
// preparation wrote is taken away again (the files of a restart are kept).
func (rt *Runtime) Start(ctx context.Context, cmd StartCommand) (StartResult, error) {
	req := cmd.Request
	if cmd.Character == "" || cmd.Character != req.ID {
		return StartResult{}, fmt.Errorf("characters: the command names %q and the request %q", cmd.Character, req.ID)
	}
	// The character is reserved before anything is prepared: two Starts of
	// the same character never both run (the loser would undo the files of
	// the winner).
	rt.mu.Lock()
	old := rt.sessions[cmd.Character]
	if rt.starting[cmd.Character] {
		rt.mu.Unlock()
		return StartResult{}, fmt.Errorf("characters: %s is already starting on this machine", cmd.Character)
	}
	if old != nil && old.status().State.Alive() && old.status().State != Interrupted {
		rt.mu.Unlock()
		return StartResult{}, fmt.Errorf("characters: %s already runs on this machine", cmd.Character)
	}
	rt.starting[cmd.Character] = true
	rt.mu.Unlock()
	defer func() {
		rt.mu.Lock()
		delete(rt.starting, cmd.Character)
		rt.mu.Unlock()
	}()
	provider := providerOfAgent(req.Snapshot.Sheet.Agent)
	if cmd.Request.Role != RoleGrandMaster && req.Table.ID != "" && rt.o.Tables != nil {
		if err := rt.o.Tables.CheckStart(ctx, req.Workspace, req.Table.ID, req.Dir, provider); err != nil {
			return StartResult{}, err
		}
	}
	plan, err := rt.o.Prep.Prepare(ctx, req)
	if err != nil {
		return StartResult{}, err
	}
	undo := func(err error) (StartResult, error) {
		if eerr := rt.o.Prep.End(context.WithoutCancel(ctx), sessionzero.EndRequest{ID: req.ID, Dir: req.Dir, Provider: plan.Provider, Final: !cmd.Resumed}); eerr != nil {
			rt.log.Warn("characters: what a failed start wrote was not removed", "id", req.ID, "err", eerr)
		}
		return StartResult{}, err
	}
	if plan.Launch.Verify != nil {
		if err := plan.Launch.Verify(); err != nil {
			return undo(fmt.Errorf("the command of %s does not verify: %w", req.Name, err))
		}
	}
	adapter, err := rt.o.Providers.For(plan.Provider)
	if err != nil {
		return undo(err)
	}
	s := &session{rt: rt, id: req.ID, launch: launchOf(cmd, plan, plan.Provider), adapter: adapter, info: adapter.Info(), terminal: cmd.Terminal,
		sessionID: plan.Launch.SessionID, turns: providers.NewTurns(adapter.Info()), starts: 1, cmd: cmd, hadKey: req.ProviderKey != nil}
	s.cmd.Request.ProviderKey = nil
	s.readyRe = readyRegexp(s.launch)
	if row, ok, err := rt.local.Get(ctx, req.ID); err == nil && ok {
		s.starts = row.Starts + 1
		s.lastTurn = row.LastTurn
		s.turns.ResumeAfter(row.LastTurn)
	}
	if text := joinText(plan.FirstMessage, cmd.ResumeMessage); text != "" {
		s.pendingFirst, s.firstHeld = text, true
	}
	s.mu.Lock()
	now := rt.clk.Now()
	s.st, s.since, s.lastSignal = Status{Starting, "start"}, now, now
	s.persistLocked() // the row first, as the registry of the terminals does
	first := s.reportLocked(now)
	s.mu.Unlock()
	// The session is known before the process exists: the agent calls the
	// hook of its start the moment it runs, which can be before Create returns.
	rt.mu.Lock()
	rt.sessions[req.ID], rt.byTerm[cmd.Terminal] = s, s
	if old != nil {
		delete(rt.byTerm, old.terminal)
	}
	rt.mu.Unlock()
	s.send(ctx, first)
	spec := HostSpec{ID: cmd.Terminal, CharacterID: req.ID, Dir: req.Dir, Class: s.launch.Class, ProviderSessionID: plan.Launch.SessionID,
		Path: plan.Launch.Path, Args: plan.Launch.Args, Env: plan.Launch.Env, WorkDir: plan.Launch.Dir}
	proc, err := rt.o.Host.Create(ctx, spec)
	if err != nil {
		s.close()
		rt.mu.Lock()
		delete(rt.sessions, req.ID)
		delete(rt.byTerm, cmd.Terminal)
		if old != nil {
			rt.sessions[req.ID], rt.byTerm[old.terminal] = old, old
		}
		rt.mu.Unlock()
		if old != nil {
			old.mu.Lock()
			old.persistLocked()
			old.mu.Unlock()
		} else {
			_ = rt.local.Delete(ctx, req.ID)
		}
		return undo(fmt.Errorf("terminal of %s: %w", req.Name, err))
	}
	s.mu.Lock()
	s.proc = proc
	s.sup = rt.supervisor(s)
	starting := s.st.State == Starting
	if starting {
		s.timersLocked()
		if s.launch.Ready == string(presets.ReadyDelay) && s.launch.ReadyDelay > 0 {
			s.delayTimer = rt.clk.AfterFunc(time.Duration(s.launch.ReadyDelay)*time.Second, func() { s.ready(context.Background()) })
		}
	}
	rep := s.reportLocked(rt.clk.Now())
	sup := s.sup
	s.mu.Unlock()
	if old != nil { // an interrupted character, started again
		old.close()
	}
	if starting {
		sup.Start(context.WithoutCancel(ctx))
	} else {
		// The hook of the start came before the terminal was ours: the
		// supervision begins past the start screens.
		sup.Hook(providers.Event{Event: providers.EventSessionStart})
	}
	s.send(ctx, rep)
	s.becameReady(ctx) // the first message, if it is ready already
	go rt.watch(s, proc)
	return StartResult{Terminal: cmd.Terminal, Session: plan.Launch.SessionID, Warnings: plan.Warnings}, nil
}

// nonEmpty is a, or b when a is empty.
func nonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// joinText joins two texts that may be empty with a blank line.
func joinText(a, b string) string {
	switch {
	case a != "" && b != "":
		return a + "\n\n" + b
	case a != "":
		return a
	}
	return b
}

// supervisor builds the supervision of the adapter for a session.
func (rt *Runtime) supervisor(s *session) *providers.Supervisor {
	return providers.NewSupervisor(providers.SupervisorConfig{
		Clock: rt.clk, Adapter: s.adapter, AgentID: s.id, TableID: s.launch.Table, ReadyPattern: s.readyRe,
		Keyboard: sessionKeyboard{s}, Characters: sessionChars{s}, TableMaster: sessionMaster{s}, Checklist: rt.o.Checklist,
		Inbox: sessionInbox{s}, Decisions: rt.o.Decisions, Trust: rt, TrustRetried: s.starts > 1, Log: rt.log,
		StartTimeout: rt.o.ScreenTimeout,
	})
}

// watch ends the session when the process ends on its own.
func (rt *Runtime) watch(s *session, p Process) {
	<-p.Exited()
	s.ended(context.Background(), "the agent exited")
}

// onHostEvent takes the events of the terminals.
func (rt *Runtime) onHostEvent(e HostEvent) {
	rt.mu.Lock()
	s := rt.byTerm[e.Terminal]
	rt.mu.Unlock()
	if s == nil {
		return
	}
	ctx := context.Background()
	switch e.Kind {
	case HostStable:
		s.screen(ctx)
	case HostExit:
		s.ended(ctx, fmt.Sprintf("the agent exited (code %d)", e.ExitCode))
	case HostLost:
		if e.Dead {
			s.ended(ctx, "the mestre-pty is gone")
		} else {
			rt.log.Warn("characters: a terminal is alive but cannot be reached", "id", s.id)
		}
	}
}

// OnHook takes the events of the hook engine (hook.Engine.OnEvent): the state
// of the character follows them.
func (rt *Runtime) OnHook(ev providers.Event, ans hook.Answer) {
	if ans.Unidentified {
		// The caller is not a process of ev.AgentID: nothing it wrote moves
		// that character.
		return
	}
	s := rt.session(ev.AgentID)
	if s == nil {
		return
	}
	s.mu.Lock()
	events := s.turns.Apply(ev)
	s.mu.Unlock()
	for _, e := range events {
		s.onEvent(context.Background(), e)
	}
}

// Env implements hook.Characters: the environment of a character that runs
// here, for the permission pipeline.
func (rt *Runtime) Env(ctx context.Context, agentID string) (classes.Env, error) {
	s := rt.session(agentID)
	if s == nil {
		return classes.Env{}, fmt.Errorf("characters: %s does not run on this machine", agentID)
	}
	l := s.launch
	env := classes.Env{AgentID: agentID, TableID: l.Table, Worktree: l.Dir, Tmp: l.Tmp, Home: rt.o.Home, Layout: rt.o.Layout, OS: rt.o.OS}
	if rt.o.EnvExtra != nil {
		rt.o.EnvExtra(l, &env)
	}
	return env, nil
}

// WaitingApproval implements hook.States: the character asked for an approval
// and waits for it, "aguardando aprovação" (not "aguardando permissão", which
// is the native prompt of the provider). Its turn, when it ends, ends in
// waiting.
func (rt *Runtime) WaitingApproval(_ context.Context, agentID string, request int64) error {
	s := rt.session(agentID)
	if s == nil {
		return fmt.Errorf("characters: %s does not run on this machine", agentID)
	}
	s.mu.Lock()
	s.wait = fmt.Sprintf("approval #%d", request)
	s.mu.Unlock()
	return nil
}

// Context implements hook.Contexts: the Mestre layer when it travels by the
// hook, and the sections that changed at the start of each turn.
func (rt *Runtime) Context(ctx context.Context, ev providers.Event) (string, error) {
	return rt.o.Prep.Contexts().Context(ctx, ev)
}

// WaitResolved says the wait of a character ended without a delivery: a
// waiting character goes to idle; the approval wait is forgotten.
func (rt *Runtime) WaitResolved(ctx context.Context, agentID string) error {
	s := rt.session(agentID)
	if s == nil {
		return fmt.Errorf("characters: %s does not run on this machine", agentID)
	}
	s.mu.Lock()
	s.wait = ""
	waiting := s.st.State == Waiting
	s.mu.Unlock()
	if waiting {
		s.do(ctx, TrigWaitResolved, "wait resolved")
	}
	return nil
}

// Deliver types a delivery of the inbox into the terminal of a character that
// is idle or waiting, marks it busy, and starts the wait for the turn to
// begin. The sections of the layer that changed go in front of the text of a
// character without the hook of the start of the turn.
func (rt *Runtime) Deliver(ctx context.Context, agentID, deliveryID, text string) error {
	s := rt.session(agentID)
	if s == nil {
		return fmt.Errorf("characters: %s does not run on this machine", agentID)
	}
	if st := s.status().State; !st.Receives() || s.holdsFirst() {
		return fmt.Errorf("%w: %s is %s", ErrBusy, agentID, st)
	}
	if upd := rt.update(s); upd != "" {
		text = upd + "\n" + text
	}
	return rt.deliverRaw(ctx, s, deliveryID, text)
}

// update takes the sections of the layer that changed, for a character that
// has no hook of the start of the turn to bring them (nothing for the others).
func (rt *Runtime) update(s *session) string {
	s.mu.Lock()
	noHook := !s.info.Hooks || s.reduced == string(providers.ReducedNoHooks)
	s.mu.Unlock()
	if !noHook {
		return ""
	}
	return rt.o.Prep.TakeUpdate(s.id)
}

// deliverRaw types the text as it is. The adapter must be able to type it
// before the state moves: a text it refuses (too long, or several lines for a
// version that does not take them) leaves the character as it was.
func (rt *Runtime) deliverRaw(ctx context.Context, s *session, deliveryID, text string) error {
	if _, err := s.adapter.TypeMessage(text); err != nil {
		return err
	}
	s.mu.Lock()
	sup := s.sup
	s.mu.Unlock()
	if sup == nil {
		return noProcess(s.id)
	}
	s.do(ctx, TrigDeliver, "delivery "+deliveryID)
	if err := sup.Deliver(ctx, deliveryID, text); err != nil {
		s.do(ctx, TrigTurnEnd, ReasonDeliveryFailed)
		return err
	}
	return nil
}

// Input types raw input into the terminal (`ask --raw`): menus and screens
// the adapter does not know. A character that works is never interrupted, and
// a native permission prompt is decided by the hook and the approval flow,
// never by keys. A simple terminal takes input in any state.
func (rt *Runtime) Input(ctx context.Context, agentID string, data []byte) error {
	s := rt.session(agentID)
	if s == nil {
		return fmt.Errorf("characters: %s does not run on this machine", agentID)
	}
	st := s.status()
	if s.launch.Kind != KindTerminal {
		switch {
		case st.State == Blocked && st.Reason == ReasonPermission:
			return fmt.Errorf("%w: %s", ErrPermissionPrompt, agentID)
		case st.State != Idle && st.State != Stuck && (st.State != Blocked || st.Reason != ReasonInput):
			return fmt.Errorf("%w: %s is %s", ErrBusy, agentID, st.State)
		}
	}
	s.mu.Lock()
	p := s.proc
	if st.State == Blocked {
		s.inputGiven = true
	}
	s.mu.Unlock()
	if p == nil {
		return noProcess(agentID)
	}
	if s.launch.Provider == providers.Null && (st.State == Idle || st.State == Waiting) {
		s.do(ctx, TrigDeliver, "input")
	}
	return p.Input(ctx, data)
}

// Screen is the rendered screen of a character's terminal (`check`).
func (rt *Runtime) Screen(ctx context.Context, agentID string) (Screen, error) {
	s := rt.session(agentID)
	if s == nil {
		return Screen{}, fmt.Errorf("characters: %s does not run on this machine", agentID)
	}
	s.mu.Lock()
	p := s.proc
	s.mu.Unlock()
	if p == nil {
		return Screen{}, noProcess(agentID)
	}
	return p.Screen(ctx)
}

// ScreenLines is Screen with up to lines lines of the history (`check
// --lines`); a terminal that keeps none answers with the screen alone.
func (rt *Runtime) ScreenLines(ctx context.Context, agentID string, lines int) (Screen, error) {
	if lines > 0 {
		if s := rt.session(agentID); s != nil {
			s.mu.Lock()
			p := s.proc
			s.mu.Unlock()
			if sl, ok := p.(interface {
				ScreenLines(context.Context, int) (Screen, error)
			}); ok {
				return sl.ScreenLines(ctx, lines)
			}
		}
	}
	return rt.Screen(ctx, agentID)
}

// State is the state of a character on this machine.
func (rt *Runtime) State(agentID string) (Status, bool) {
	s := rt.session(agentID)
	if s == nil {
		return Status{}, false
	}
	return s.status(), true
}

// RecoverTrust implements providers.TrustRecovery: the start showed the screen
// of the trust of the folder. The process is ended without answering it
// (answering "trust" would trust the mirror of the repository), the trust of
// the worktree is written again, and the character starts again with the same
// session.
func (rt *Runtime) RecoverTrust(ctx context.Context, agentID string) error {
	s := rt.session(agentID)
	if s == nil {
		return fmt.Errorf("characters: %s does not run on this machine", agentID)
	}
	if rt.o.Retrust == nil || s.hadKey {
		return errors.New("characters: the trust of the folder cannot be written again here")
	}
	if err := rt.o.Retrust.Retrust(ctx, s.launch.Workspace, s.launch.Table, s.launch.Dir); err != nil {
		return err
	}
	s.mu.Lock()
	s.stopping = true
	p := s.proc
	s.mu.Unlock()
	if p != nil {
		if err := p.Stop(ctx, nil, 0); err != nil {
			return err
		}
	}
	cmd := s.cmd
	cmd.Resumed = true
	cmd.Terminal = terminalID(Character{ID: agentID, TerminalID: s.terminal}, true)
	cmd.Request.ResumeSessionID = s.sessionID
	cmd.Request.NewSessionID = ""
	s.close()
	rt.mu.Lock()
	delete(rt.byTerm, s.terminal)
	delete(rt.sessions, agentID)
	rt.mu.Unlock()
	// The same character, a new process: the Citadel does not hear of an
	// interruption.
	_, err := rt.Start(context.WithoutCancel(ctx), cmd)
	return err
}

// Close lets go of the terminals without touching them: the engine is going
// down, and the mestre-pty of each keeps its agent running.
func (rt *Runtime) Close() {
	rt.mu.Lock()
	all := make([]*session, 0, len(rt.sessions))
	for _, s := range rt.sessions {
		all = append(all, s)
	}
	rt.mu.Unlock()
	for _, s := range all {
		s.close()
		s.mu.Lock()
		p := s.proc
		s.mu.Unlock()
		if p != nil {
			p.Detach()
		}
	}
}

// tellTableMaster sends a notice about a session to the table master.
func (rt *Runtime) tellTableMaster(ctx context.Context, s *session, kind NoticeKind, text string) {
	err := rt.o.Notifier.Notify(ctx, Notice{Kind: kind, To: Recipient{Kind: ToTableMaster}, Workspace: s.launch.Workspace, Table: s.launch.Table,
		Character: s.id, Name: s.launch.Name, Text: text})
	if err != nil {
		rt.log.Warn("characters: a notice was not sent", "id", s.id, "err", err)
	}
}
