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

// SendRetrier is an adapter whose CLI may keep a delivery in its input area
// without sending it: Codex 0.163.0 on Windows, which drops the send key that
// comes in the same write as the paste (docs/provedores/codex.md#digitação).
// SendsAgain is false where the CLI does not do that (the other systems);
// InputHolds tells whether the input area of screen still holds text; the
// supervisor then types SendKey once more, never the text.
type SendRetrier interface {
	SendsAgain() bool
	InputHolds(screen, text string) bool
	SendKey() []byte
}

// DefaultSendRetry is how long after a delivery, and after each look, the
// supervisor looks for it still in the input area of a SendRetrier; it
// looks SendRetries times, all before the confirmation timeout.
const (
	DefaultSendRetry = 2 * time.Second
	SendRetries      = 4
)

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

// Reasons of the `blocked` state the supervision sets. BlockInput is a
// screen that waits for an answer the adapter does not give; BlockQuota is
// the wait for the quota after the menu of the usage limit
// (docs/plataforma/limites-de-uso.md#aguardando-cota).
const (
	BlockInput BlockReason = "input"
	BlockQuota BlockReason = "quota"
)

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
// permission request. dialog is the dialog on display (ScreenResult.Dialog):
// a permission screen is answered with the decision of the call it shows
// (ScreenCall), never with the last decision of the character; with no
// such call, ok is false and the screen is denied.
type Decisions interface {
	Decision(ctx context.Context, agentID, screenID, dialog string) (choice string, ok bool)
}

// CallDecision is the decision the engine took on one tool call of a
// character (pre_tool or permission_request), kept while the call can
// still ask on a native screen.
type CallDecision struct {
	Tool   *Tool
	Choice string // "allow" or "deny"; anything else is unknown, answered as "deny"
	// Cwd is the folder of the call (Event.Cwd): a relative path on the
	// dialog is read from it. Without it only an absolute path matches.
	Cwd string
}

// TrustRecovery restarts a character whose start showed the folder trust
// screen (ActionRetrust). RecoverTrust ends the process without answering
// the screen (answering "trust" would trust the mirror of the repository),
// writes the trust of the worktree again (PreTrust, which also clears the
// mirror's) and starts the character again with the same session. The
// characters implement it (milestone 4).
type TrustRecovery interface {
	RecoverTrust(ctx context.Context, agentID string) error
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
	// Trust recovers the trust screen once; nil: the screen escalates at
	// once. TrustRetried is true when the character already restarted for
	// it (a new Supervisor after the restart).
	Trust        TrustRecovery
	TrustRetried bool

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
	begun          bool // a hook came or the screen was ready once (ScreenInput.Begun)
	hooksSeen      bool // a hook after the first delivery
	delivered      bool
	noHooks        bool
	lastScreen     string
	lastStable     bool   // lastScreen was stable when it came
	hooks          int    // hook events so far (a resend needs none after the delivery)
	handled        string // the known screen already acted on
	trustRetried   bool
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
	return &Supervisor{cfg: cfg, info: cfg.Adapter.Info(), ctx: context.Background(), pending: map[string]clock.Timer{},
		trustRetried: cfg.TrustRetried}
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

// Screen takes the current screen of the terminal (on every stable event),
// when the emulator does not give the cursor.
func (s *Supervisor) Screen(text string, stable bool) { s.screen(text, stable, 0, false) }

// ScreenAt takes the current screen with the row of the cursor in it (0
// is the first line of text): the input box counts only with the cursor
// in it.
func (s *Supervisor) ScreenAt(text string, stable bool, cursorRow int) {
	s.screen(text, stable, cursorRow, true)
}

func (s *Supervisor) screen(text string, stable bool, row int, cursor bool) {
	s.mu.Lock()
	res := s.cfg.Adapter.Screens(ScreenInput{
		Text: text, Stable: stable, SessionStarted: s.sessionStarted, Begun: s.begun || s.sessionStarted,
		CursorRow: row, CursorKnown: cursor, ReadyPattern: s.cfg.ReadyPattern,
	})
	dialog := res.Dialog
	if dialog == "" {
		dialog = text
	}
	changed := text != s.lastScreen
	s.lastScreen = text
	s.lastStable = stable
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
		s.handled, s.begun = "", true
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
		s.act(act, text, dialog)
	}
}

// act does the action of a known screen; the decision is looked for in its
// dialog.
func (s *Supervisor) act(k *KnownScreen, screen, dialog string) {
	ctx := s.ctx
	switch k.Action {
	case ActionAnswer:
		choice, ok := "", false
		if s.cfg.Decisions != nil {
			choice, ok = s.cfg.Decisions.Decision(ctx, s.cfg.AgentID, k.ID, dialog)
		}
		if !ok {
			choice = k.Default
		}
		if b, found := k.Answers[choice]; found && choice != "" {
			if err := s.cfg.Keyboard.Input(ctx, b); err != nil {
				s.report(err)
				return
			}
			if k.Block != "" {
				s.report(s.cfg.Characters.Block(ctx, s.cfg.AgentID, k.Block, "screen "+k.ID+": "+choice))
			}
			return
		}
		s.escalate(screen, "screen "+k.ID+" without a decision")
	case ActionRetrust:
		s.mu.Lock()
		retry := s.cfg.Trust != nil && !s.trustRetried
		s.trustRetried = true
		s.mu.Unlock()
		if retry {
			err := s.cfg.Trust.RecoverTrust(ctx, s.cfg.AgentID)
			if err == nil {
				s.mu.Lock() // a new process starts: the same screen is news again
				s.handled, s.starting, s.begun = "", true, false
				s.armStartLocked()
				s.mu.Unlock()
				return
			}
			s.report(err)
		}
		s.escalate(screen, "screen "+k.ID)
		if k.Checklist != "" {
			s.report(s.cfg.Checklist.Alert(ctx, Alert{Item: k.Checklist, AgentID: s.cfg.AgentID, Detail: "screen " + k.ID}))
		}
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
	s.hooks++
	if s.starting { // a hook arrived: the start is not stuck
		s.starting = false
		s.stopStartLocked()
	}
	s.begun = true
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
	if r, ok := s.cfg.Adapter.(SendRetrier); ok && r.SendsAgain() {
		s.mu.Lock()
		hooks := s.hooks
		s.mu.Unlock()
		s.cfg.Clock.AfterFunc(DefaultSendRetry, func() { s.resend(id, text, r, hooks, 1) })
	}
	return nil
}

// resend types the send key once more when the delivery is still waiting for
// its turn_start, no hook came since it was typed (a CLI that took the
// prompt runs its hooks; a slow turn_start is not a lost key), and the last
// screen, stable, is the input area with the text still in it and no known
// screen (a dialog, as an approval, would take the key as its answer). The
// look and the key are under the lock, so no screen comes in between. A key
// sent empties the input area, so the next look types nothing.
func (s *Supervisor) resend(id, text string, r SendRetrier, hooks, look int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, waiting := s.pending[id]; !waiting || s.hooks != hooks {
		return
	}
	res := s.cfg.Adapter.Screens(ScreenInput{
		Text: s.lastScreen, Stable: s.lastStable, SessionStarted: s.sessionStarted, Begun: s.begun || s.sessionStarted, ReadyPattern: s.cfg.ReadyPattern,
	})
	if s.lastStable && res.Known == nil && res.State == StateReady && r.InputHolds(s.lastScreen, text) {
		if err := s.cfg.Keyboard.Input(s.ctx, r.SendKey()); err != nil {
			s.report(fmt.Errorf("send key again for delivery %s: %w", id, err))
		}
	}
	if look < SendRetries {
		s.cfg.Clock.AfterFunc(DefaultSendRetry, func() { s.resend(id, text, r, hooks, look+1) })
	}
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
