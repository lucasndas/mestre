package providers

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Default timeouts of docs/provedores/adaptador.md.
const (
	// DefaultStartTimeout is how long a start screen may stay without a
	// hook and without a known pattern before it escalates.
	DefaultStartTimeout = 30 * time.Second
	// DefaultHookTimeout is how long after the first delivered prompt the
	// first hook may take before the character gets reduced monitoring.
	DefaultHookTimeout = 60 * time.Second
	// DefaultConfirmTimeout is how long a delivery waits for turn_start.
	DefaultConfirmTimeout = 10 * time.Second
)

// The pieces the supervision drives belong to other subjects; each is a
// small interface here, implemented by its owner.

// Keyboard types into the character's terminal (terminal.Terminal.Input).
type Keyboard interface {
	Input(ctx context.Context, data []byte) error
}

// Characters is the part of the character state the supervision changes
// (docs/orquestracao/personagens.md).
type Characters interface {
	// Block puts the character in `blocked` with the reason.
	Block(ctx context.Context, agentID string, reason BlockReason, detail string) error
	// ReduceMonitoring marks the character with reduced monitoring.
	ReduceMonitoring(ctx context.Context, agentID string, reason ReducedReason, detail string) error
}

// BlockReason is the reason of the `blocked` state.
type BlockReason string

// BlockInput is a screen that waits for an answer the adapter does not give.
const BlockInput BlockReason = "input"

// ReducedReason is why monitoring is reduced.
type ReducedReason string

// Reasons of reduced monitoring.
const (
	ReducedProviderEvents ReducedReason = "provider_events" // events of the map the provider lacks
	ReducedNoHooks        ReducedReason = "no_hooks"        // no hook after the first prompt
)

// TableMaster gets the notices for the Mestre da mesa
// (docs/orquestracao/mestre-da-mesa.md), who may answer a screen with
// `mestre ask --raw`.
type TableMaster interface {
	Notify(ctx context.Context, n Notice) error
}

// NoticeKind is the subject of a notice.
type NoticeKind string

// Notices.
const (
	NoticeBlockedInput NoticeKind = "blocked_input"
	NoticeUnconfirmed  NoticeKind = "delivery_unconfirmed"
)

// Notice is one notice to the Mestre da mesa.
type Notice struct {
	Kind       NoticeKind
	AgentID    string
	TableID    string
	Screen     string // the screen that waits for input
	DeliveryID string // the delivery not confirmed
	Detail     string
}

// Checklist raises checklist alerts (docs/plataforma/checklist.md).
type Checklist interface {
	Alert(ctx context.Context, a Alert) error
}

// Alert is one checklist alert about a character.
type Alert struct {
	Item    string // ALL-11, ALL-12, ALL-15
	AgentID string
	Detail  string
}

// ChecklistNoHooks is the checklist item of characters with reduced
// monitoring for lack of hooks.
const ChecklistNoHooks = "ALL-15"

// Inbox records the outcome of a delivery
// (docs/orquestracao/esperas-e-caixa-de-entrada.md).
type Inbox interface {
	Confirmed(ctx context.Context, deliveryID string) error
	Unconfirmed(ctx context.Context, deliveryID string) error
}

// Decisions are the answers the engine already took for a screen: trust in
// a worktree the Mestre created, the recorded decision of a native
// permission request.
type Decisions interface {
	Decision(ctx context.Context, agentID, screenID string) (choice string, ok bool)
}

// SupervisorConfig configures the supervision of one character.
type SupervisorConfig struct {
	Clock   clock.Clock
	Adapter Adapter
	AgentID string
	TableID string
	// ReadyPattern is the base's `ready: pattern`, when it has one.
	ReadyPattern *regexp.Regexp

	Keyboard    Keyboard
	Characters  Characters
	TableMaster TableMaster
	Checklist   Checklist
	Inbox       Inbox
	Decisions   Decisions // nil: no decision is ever recorded

	// Zero means the default.
	StartTimeout, HookTimeout, ConfirmTimeout time.Duration
	Log                                       *slog.Logger
}

// Supervisor runs, for one character, the timeouts and screens of the
// adapter document: the start screen that stays unknown (30 s), the hooks
// that never arrive after the first prompt (60 s), the delivery without
// turn_start (10 s), and the known screens. It never types a delivery
// again. Safe for concurrent use.
type Supervisor struct {
	cfg  SupervisorConfig
	info Info
	ctx  context.Context

	mu             sync.Mutex
	starting       bool
	sessionStarted bool
	hooksSeen      bool // a hook after the first delivery
	delivered      bool
	noHooks        bool
	lastScreen     string
	handled        string // the known screen already acted on
	startTimer     clock.Timer
	startGen       int
	hookTimer      clock.Timer
	pending        map[string]clock.Timer
}

// NewSupervisor returns the supervision of one character; Start begins it.
func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	if cfg.StartTimeout == 0 {
		cfg.StartTimeout = DefaultStartTimeout
	}
	if cfg.HookTimeout == 0 {
		cfg.HookTimeout = DefaultHookTimeout
	}
	if cfg.ConfirmTimeout == 0 {
		cfg.ConfirmTimeout = DefaultConfirmTimeout
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Supervisor{cfg: cfg, info: cfg.Adapter.Info(), ctx: context.Background(), pending: map[string]clock.Timer{}}
}

// Start begins the supervision of a character whose terminal just started.
// ctx bounds the calls the timeouts make.
func (s *Supervisor) Start(ctx context.Context) {
	s.mu.Lock()
	s.ctx, s.starting = ctx, true
	s.armStartLocked()
	s.mu.Unlock()
	if s.info.Reduced != "" {
		s.report(s.cfg.Characters.ReduceMonitoring(ctx, s.cfg.AgentID, ReducedProviderEvents, s.info.Reduced))
	}
}

// Close stops every timeout.
func (s *Supervisor) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.starting = false
	s.startGen++
	stop(s.startTimer, s.hookTimer)
	for id, t := range s.pending {
		t.Stop()
		delete(s.pending, id)
	}
}

func stop(ts ...clock.Timer) {
	for _, t := range ts {
		if t != nil {
			t.Stop()
		}
	}
}

// armStartLocked (re)starts the 30 s of the start screen.
func (s *Supervisor) armStartLocked() {
	stop(s.startTimer)
	s.startGen++
	gen := s.startGen
	s.startTimer = s.cfg.Clock.AfterFunc(s.cfg.StartTimeout, func() { s.startExpired(gen) })
}

func (s *Supervisor) stopStartLocked() {
	stop(s.startTimer)
	s.startGen++
}

func (s *Supervisor) startExpired(gen int) {
	s.mu.Lock()
	if gen != s.startGen || !s.starting {
		s.mu.Unlock()
		return
	}
	s.starting = false
	screen := s.lastScreen
	s.mu.Unlock()
	s.escalate(screen, fmt.Sprintf("start screen without hook and without a known pattern for %s", s.cfg.StartTimeout))
}

// escalate blocks the character with reason input and tells the Mestre da
// mesa.
func (s *Supervisor) escalate(screen, detail string) {
	ctx := s.ctx
	s.report(s.cfg.Characters.Block(ctx, s.cfg.AgentID, BlockInput, detail))
	s.report(s.cfg.TableMaster.Notify(ctx, Notice{
		Kind: NoticeBlockedInput, AgentID: s.cfg.AgentID, TableID: s.cfg.TableID, Screen: screen, Detail: detail,
	}))
}

// Screen takes the current screen of the terminal (on every stable event).
func (s *Supervisor) Screen(text string, stable bool) {
	s.mu.Lock()
	res := s.cfg.Adapter.Screens(ScreenInput{
		Text: text, Stable: stable, SessionStarted: s.sessionStarted, ReadyPattern: s.cfg.ReadyPattern,
	})
	changed := text != s.lastScreen
	s.lastScreen = text
	var act *KnownScreen
	var confirmed []string
	switch {
	case res.Known != nil: // a known screen has its action instead of the 30 s
		if s.starting {
			s.stopStartLocked()
		}
		if key := res.Known.ID + "\x00" + text; key != s.handled {
			s.handled, act = key, res.Known
		}
	case res.State == StateReady:
		s.handled = ""
		if s.starting {
			s.starting = false
			s.stopStartLocked()
		}
	case res.State == StateBusy:
		s.handled = ""
		if s.starting {
			s.stopStartLocked()
		}
		if !s.info.Hooks || s.noHooks { // no turn_start to wait for: the busy screen confirms
			confirmed = s.confirmLocked()
		}
	default:
		if s.starting && changed {
			s.armStartLocked()
		}
	}
	s.mu.Unlock()
	s.confirmed(confirmed)
	if act != nil {
		s.act(act, text)
	}
}

// act does the action of a known screen.
func (s *Supervisor) act(k *KnownScreen, screen string) {
	ctx := s.ctx
	switch k.Action {
	case ActionAnswer:
		choice, ok := "", false
		if s.cfg.Decisions != nil {
			choice, ok = s.cfg.Decisions.Decision(ctx, s.cfg.AgentID, k.ID)
		}
		if !ok {
			choice = k.Default
		}
		if b, found := k.Answers[choice]; found && choice != "" {
			s.report(s.cfg.Keyboard.Input(ctx, b))
			return
		}
		s.escalate(screen, "screen "+k.ID+" without a decision")
	case ActionAlert:
		s.report(s.cfg.Characters.Block(ctx, s.cfg.AgentID, BlockInput, "screen "+k.ID))
		s.report(s.cfg.Checklist.Alert(ctx, Alert{Item: k.Checklist, AgentID: s.cfg.AgentID, Detail: "screen " + k.ID}))
	default: // ActionEscalate, and ActionAvoid when the pre-configuration failed
		s.escalate(screen, "screen "+k.ID)
		if k.Checklist != "" {
			s.report(s.cfg.Checklist.Alert(ctx, Alert{Item: k.Checklist, AgentID: s.cfg.AgentID, Detail: "screen " + k.ID}))
		}
	}
}

// Hook takes a normalized event of the character (after Turns).
func (s *Supervisor) Hook(ev Event) {
	s.mu.Lock()
	if s.starting { // a hook arrived: the start is not stuck
		s.starting = false
		s.stopStartLocked()
	}
	if ev.Event == EventSessionStart {
		s.sessionStarted = true
	}
	if s.delivered && !s.hooksSeen {
		s.hooksSeen = true
		stop(s.hookTimer)
	}
	var confirmed []string
	if ev.Event == EventTurnStart {
		confirmed = s.confirmLocked()
	}
	s.mu.Unlock()
	s.confirmed(confirmed)
}

// confirmLocked takes the pending deliveries out; the caller reports them
// confirmed. A timer that fired meanwhile finds its delivery gone.
func (s *Supervisor) confirmLocked() []string {
	var ids []string
	for id, t := range s.pending {
		t.Stop()
		delete(s.pending, id)
		ids = append(ids, id)
	}
	return ids
}

func (s *Supervisor) confirmed(ids []string) {
	for _, id := range ids {
		s.report(s.cfg.Inbox.Confirmed(s.ctx, id))
	}
}

// Deliver types one delivery of the inbox and waits for its confirmation:
// turn_start (or the busy screen, where there is no event) within 10 s.
// Without it the delivery is recorded as unconfirmed and the Mestre da mesa
// is told; the text is never typed again. A text above MaxTyped is
// ErrTooLong: it goes to a file (inbox).
func (s *Supervisor) Deliver(ctx context.Context, id, text string) error {
	b, err := s.cfg.Adapter.TypeMessage(text)
	if err != nil {
		return err
	}
	// The wait starts before the typing: the turn_start of a fast agent
	// must find the delivery pending.
	s.mu.Lock()
	timer := s.cfg.Clock.AfterFunc(s.cfg.ConfirmTimeout, func() { s.unconfirmed(id) })
	s.pending[id] = timer
	if !s.delivered {
		s.delivered = true
		if s.info.Hooks {
			s.hookTimer = s.cfg.Clock.AfterFunc(s.cfg.HookTimeout, s.noHooksExpired)
		}
	}
	s.mu.Unlock()
	if err := s.cfg.Keyboard.Input(ctx, b); err != nil {
		s.mu.Lock()
		timer.Stop()
		delete(s.pending, id)
		s.mu.Unlock()
		return fmt.Errorf("type delivery %s: %w", id, err)
	}
	return nil
}

func (s *Supervisor) unconfirmed(id string) {
	s.mu.Lock()
	_, ok := s.pending[id]
	delete(s.pending, id)
	s.mu.Unlock()
	if !ok {
		return
	}
	s.report(s.cfg.Inbox.Unconfirmed(s.ctx, id))
	s.report(s.cfg.TableMaster.Notify(s.ctx, Notice{
		Kind: NoticeUnconfirmed, AgentID: s.cfg.AgentID, TableID: s.cfg.TableID, DeliveryID: id,
		Detail: fmt.Sprintf("no turn_start %s after the delivery; it is not typed again", s.cfg.ConfirmTimeout),
	}))
}

func (s *Supervisor) noHooksExpired() {
	s.mu.Lock()
	if s.hooksSeen || s.noHooks {
		s.mu.Unlock()
		return
	}
	s.noHooks = true
	s.mu.Unlock()
	detail := fmt.Sprintf("no hook %s after the first prompt (hooks not loaded or folder not trusted)", s.cfg.HookTimeout)
	s.report(s.cfg.Characters.ReduceMonitoring(s.ctx, s.cfg.AgentID, ReducedNoHooks, detail))
	s.report(s.cfg.Checklist.Alert(s.ctx, Alert{Item: ChecklistNoHooks, AgentID: s.cfg.AgentID, Detail: detail}))
}

func (s *Supervisor) report(err error) {
	if err != nil {
		s.cfg.Log.Error("adapter supervision", "agent", s.cfg.AgentID, "err", err)
	}
}
