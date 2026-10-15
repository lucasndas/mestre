package waits

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Connections finds the character an agent addresses by name, if it is
// connected to it (connections.Service.Target: not_connected, not_visible).
type Connections interface {
	Target(ctx context.Context, agentID, table, name string) (characters.Character, error)
}

// StepInfo is a step of a quest.
type StepInfo struct{ ID, Name string }

// QuestInfo is a quest, as the waits need it.
type QuestInfo struct {
	ID, Name string
	Steps    []StepInfo
}

// Quests is what the waits need of the quests (milestone 4, stage 9): the
// quest by name and the character responsible for a step now, which is where
// an arrow of the graph of waits points to. Nil: there are no quests yet.
type Quests interface {
	Quest(ctx context.Context, workspace, table, name string) (QuestInfo, bool, error)
	Responsible(ctx context.Context, stepID string) (characterID string, err error)
}

// Waker tells the machine that the wait of a character ended without a
// delivery, so a waiting character goes back to idle (characters.Runtime.WaitResolved).
type Waker interface {
	WaitResolved(ctx context.Context, characterID string) error
}

// Terminals reaches the terminal of a character on its machine (`ask --raw`
// and `check`): characters.MachineSet.
type Terminals interface {
	Input(ctx context.Context, machine, character string, data []byte) error
	Screen(ctx context.Context, machine, character string) (characters.Screen, error)
}

// HistoryTerminals is a Terminals that can also read the history of the
// screen (`check --lines`); characters.MachineSet is one.
type HistoryTerminals interface {
	Terminals
	ScreenLines(ctx context.Context, machine, character string, lines int) (characters.Screen, error)
}

// Attachments copies the attachments of a message into the table of the
// target of an `ask --from-message` (milestone 5 owns the attachments,
// docs/telegram/anexos-e-transcricao.md) and returns the paths of the copies
// in that table. Nil: a message with attachments cannot be passed on.
type Attachments interface {
	Forward(ctx context.Context, workspace, table string, paths []string) ([]string, error)
}

// Options are the pieces of the Service.
type Options struct {
	Store    *Store
	Clock    clock.Clock
	Registry characters.Registry
	Inbox    *inbox.Service
	// Notifier tells the masters and the users (nil: the inbox's, which
	// writes to the masters and has no channel to the users yet).
	Notifier    characters.Notifier
	Connections Connections
	// Quests, Waker and Terminals may be nil.
	Quests    Quests
	Waker     Waker
	Terminals Terminals
	// Attachments is the copy of the attachments of `ask --from-message`.
	Attachments Attachments
	// Timeouts gives the time limits of a workspace (nil: the defaults).
	Timeouts func(ctx context.Context, workspace string) Timeouts
	// Zone is the time zone of the Guild, for a wait until "HH:MM" (UTC by
	// default).
	Zone   *time.Location
	Logger *slog.Logger
}

// Service keeps the waits and the asks of the characters
// (docs/orquestracao/esperas-e-caixa-de-entrada.md): the engine waits for
// them, never an agent in a loop. It registers a wait and returns at once;
// when the wait is satisfied it puts a `wake` item with the result in the inbox
// of who waits. It also holds the graph of who waits for whom and warns of a
// deadlock.
type Service struct {
	o   Options
	st  *Store
	clk clock.Clock
	log *slog.Logger

	// wmu serializes the changes of the waits and the asks, so a wait is
	// satisfied or expires once, and an ask is answered once.
	wmu sync.Mutex

	mu       sync.Mutex
	timers   map[string]clock.Timer
	blockers map[string]chan askResult
	// dones are the texts of the done of each character in its current turn:
	// the answer of an ask is the done of the turn that handled it.
	dones map[string]string
}

var (
	_ characters.Observer  = (*Service)(nil)
	_ characters.WaitProbe = (*Service)(nil)
)

// NewService returns the Service. Recover brings back what the engine had
// before it went down.
func NewService(o Options) (*Service, error) {
	switch {
	case o.Store == nil || o.Store.DB == nil:
		return nil, errors.New("waits: the service needs the store")
	case o.Registry.DB == nil:
		return nil, errors.New("waits: the service needs the registry of characters")
	case o.Inbox == nil:
		return nil, errors.New("waits: the service needs the inbox")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Notifier == nil {
		o.Notifier = o.Inbox
	}
	if o.Zone == nil {
		o.Zone = time.UTC
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{o: o, st: o.Store, clk: o.Clock, log: o.Logger, timers: map[string]clock.Timer{}, blockers: map[string]chan askResult{}, dones: map[string]string{}}, nil
}

// Store gives access to the tables.
func (s *Service) Store() *Store { return s.st }

func (s *Service) timeouts(ctx context.Context, workspace string) Timeouts {
	if s.o.Timeouts == nil {
		return DefaultTimeouts()
	}
	return s.o.Timeouts(ctx, workspace).filled()
}

// WaitRequest is `mestre wait "<Name>" …`.
type WaitRequest struct {
	Waiter characters.Character
	// Table is the table the Grand Master names.
	Table string
	Names []string
	// First waits for the first of the names, not all.
	First bool
	// Timeout defaults to the one of the workspace.
	Timeout time.Duration
}

// WaitFor registers a wait for characters: satisfied when each one gives a
// done, or does an ask back to the one who waits. It returns at once.
func (s *Service) WaitFor(ctx context.Context, r WaitRequest) (Wait, error) {
	if len(r.Names) == 0 {
		return Wait{}, fmt.Errorf("%w: no name", ErrBadWait)
	}
	var targets []Target
	var names []string
	seen := map[string]bool{}
	for _, name := range r.Names {
		if strings.EqualFold(name, r.Waiter.Name) && r.Table == "" {
			return Wait{}, fmt.Errorf("%w: %s", ErrSelf, name)
		}
		if s.o.Connections == nil {
			return Wait{}, errors.New("waits: the service has no way to find the characters")
		}
		c, err := s.o.Connections.Target(ctx, r.Waiter.ID, r.Table, name)
		if err != nil {
			return Wait{}, err
		}
		if c.ID == r.Waiter.ID {
			return Wait{}, fmt.Errorf("%w: %s", ErrSelf, name)
		}
		if seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		targets = append(targets, Target{ID: c.ID, Name: c.Name})
		names = append(names, c.Name)
	}
	mode := All
	if r.First {
		mode = First
	}
	table := r.Waiter.Table
	if table == "" {
		if first, err := s.o.Registry.Get(ctx, targets[0].ID); err == nil {
			table = first.Table
		}
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = s.timeouts(ctx, r.Waiter.Workspace).Character
	}
	return s.create(ctx, Wait{Workspace: r.Waiter.Workspace, Table: table, Waiter: r.Waiter.ID, Kind: KindCharacter, Mode: mode,
		Targets: targets, Reason: reasonOf(names, mode)}, timeout)
}

// QuestRequest is `mestre wait --quest "<Quest>"`.
type QuestRequest struct {
	Waiter  characters.Character
	Table   string
	Quest   string
	Timeout time.Duration
}

// WaitQuest registers a wait for all the steps of a quest.
func (s *Service) WaitQuest(ctx context.Context, r QuestRequest) (Wait, error) {
	if s.o.Quests == nil {
		return Wait{}, fmt.Errorf("%w: %s", ErrNoQuest, r.Quest)
	}
	table := r.Waiter.Table
	if table == "" {
		table = r.Table
	}
	q, ok, err := s.o.Quests.Quest(ctx, r.Waiter.Workspace, table, r.Quest)
	if err != nil {
		return Wait{}, err
	}
	if !ok {
		return Wait{}, fmt.Errorf("%w: %s", ErrNoQuest, r.Quest)
	}
	steps := make([]Target, len(q.Steps))
	for i, st := range q.Steps {
		steps[i] = Target{ID: st.ID, Name: st.Name}
	}
	return s.WaitSteps(ctx, StepsRequest{Waiter: r.Waiter, Quest: q.ID, QuestName: q.Name, Steps: steps, Timeout: r.Timeout})
}

// StepsRequest is a wait for the steps of a quest, made by the quests of the
// engine or by `wait --quest`.
type StepsRequest struct {
	Waiter characters.Character
	// Quest is the quest the wait belongs to: it is cancelled with it.
	Quest     string
	QuestName string
	Steps     []Target
	// First waits for the first step, not all.
	First   bool
	Timeout time.Duration
}

// WaitSteps registers a wait for steps of a quest: satisfied when all of them
// (or the first one) are done (StepDone).
func (s *Service) WaitSteps(ctx context.Context, r StepsRequest) (Wait, error) {
	if len(r.Steps) == 0 {
		return Wait{}, fmt.Errorf("%w: no steps", ErrBadWait)
	}
	mode := All
	if r.First {
		mode = First
	}
	names := make([]string, len(r.Steps))
	for i, st := range r.Steps {
		names[i] = st.Name
	}
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = s.timeouts(ctx, r.Waiter.Workspace).Steps
	}
	return s.create(ctx, Wait{Workspace: r.Waiter.Workspace, Table: r.Waiter.Table, Waiter: r.Waiter.ID, Kind: KindSteps, Mode: mode,
		Targets: r.Steps, Reason: reasonOf(names, mode), Quest: r.Quest}, timeout)
}

// WaitUntil registers a wait for a time (`wait --until`); it has no time
// limit. A time in the past is an error.
func (s *Service) WaitUntil(ctx context.Context, waiter characters.Character, at time.Time) (Wait, error) {
	if !at.After(s.clk.Now()) {
		return Wait{}, fmt.Errorf("%w: %s", ErrPast, at.In(s.o.Zone).Format("2006-01-02 15:04"))
	}
	return s.create(ctx, Wait{Workspace: waiter.Workspace, Table: waiter.Table, Waiter: waiter.ID, Kind: KindTime, Mode: All,
		Reason: at.In(s.o.Zone).Format("15:04"), UntilAt: at}, 0)
}

// ParseUntil reads the time of `wait --until`: "HH:MM" is the next time it is
// that hour in the zone of the Guild today (a time already past today is an
// error, not tomorrow), or an ISO 8601 date and time.
func (s *Service) ParseUntil(v string) (time.Time, error) {
	now := s.clk.Now().In(s.o.Zone)
	if t, err := time.ParseInLocation("15:04", v, s.o.Zone); err == nil {
		at := time.Date(now.Year(), now.Month(), now.Day(), t.Hour(), t.Minute(), 0, 0, s.o.Zone)
		if !at.After(now) {
			return time.Time{}, fmt.Errorf("%w: %s", ErrPast, v)
		}
		return at, nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04"} {
		if t, err := time.ParseInLocation(layout, v, s.o.Zone); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("%w: %q is not HH:MM or an ISO 8601 time", ErrBadWait, v)
}

// WaitApproval registers the wait of a character for the decision of a request
// of approval; the engine makes it (the flow of approval, milestone 5). The
// time limit is the sum of the deadlines of the levels of the chain: the flow
// passes it.
func (s *Service) WaitApproval(ctx context.Context, waiter characters.Character, request int64, timeout time.Duration) (Wait, error) {
	return s.create(ctx, Wait{Workspace: waiter.Workspace, Table: waiter.Table, Waiter: waiter.ID, Kind: KindApproval, Mode: All,
		Targets: []Target{{ID: approvalTarget(request), Name: fmt.Sprintf("approval #%d", request)}}, Reason: fmt.Sprintf("approval #%d", request)}, timeout)
}

// WaitSync registers the wait of a character for the synchronization of a
// worktree (the tables, milestone 4 stage 8); ref identifies it.
func (s *Service) WaitSync(ctx context.Context, waiter characters.Character, ref string, timeout time.Duration) (Wait, error) {
	if timeout <= 0 {
		timeout = s.timeouts(ctx, waiter.Workspace).Slot
	}
	return s.create(ctx, Wait{Workspace: waiter.Workspace, Table: waiter.Table, Waiter: waiter.ID, Kind: KindSync, Mode: All,
		Targets: []Target{{ID: ref, Name: "sincronização"}}, Reason: "sincronização"}, timeout)
}

func approvalTarget(request int64) string { return fmt.Sprintf("approval:%d", request) }

// create registers the wait, arms its timer and looks for a deadlock.
func (s *Service) create(ctx context.Context, w Wait, timeout time.Duration) (Wait, error) {
	w.CreatedAt = s.clk.Now()
	if timeout > 0 {
		w.TimeoutAt = w.CreatedAt.Add(timeout)
	}
	s.wmu.Lock()
	w, err := s.st.InsertWait(ctx, w)
	s.wmu.Unlock()
	if err != nil {
		return Wait{}, err
	}
	s.arm(w)
	s.detect(ctx, w.Waiter)
	return w, nil
}

// arm sets the timer of a wait: the moment of a wait for a time, otherwise its
// time limit.
func (s *Service) arm(w Wait) {
	at := w.TimeoutAt
	if w.Kind == KindTime {
		at = w.UntilAt
	}
	if at.IsZero() {
		return
	}
	d := max(at.Sub(s.clk.Now()), 0)
	t := s.clk.AfterFunc(d, func() { s.fire(w.ID) })
	s.mu.Lock()
	if old := s.timers[w.ID]; old != nil {
		old.Stop()
	}
	s.timers[w.ID] = t
	s.mu.Unlock()
}

func (s *Service) disarm(id string) {
	s.mu.Lock()
	t := s.timers[id]
	delete(s.timers, id)
	s.mu.Unlock()
	if t != nil {
		t.Stop()
	}
}

// fire is the timer of a wait: its time came, or its time ran out.
func (s *Service) fire(id string) {
	ctx := context.Background()
	w, err := s.st.GetWait(ctx, id)
	if err != nil || w.State != Active {
		return
	}
	if w.Kind == KindTime {
		s.satisfy(ctx, w, w.Targets)
		return
	}
	s.expire(ctx, w)
}

// satisfy closes the wait as satisfied and wakes who waits, with the result.
func (s *Service) satisfy(ctx context.Context, w Wait, targets []Target) {
	s.wmu.Lock()
	result := wakeText(w, targets)
	ok, err := s.st.End(ctx, w.ID, Satisfied, result, s.clk.Now(), targets)
	s.wmu.Unlock()
	if err != nil {
		s.log.Warn("waits: a satisfied wait was not recorded", "wait", w.ID, "err", err)
		return
	}
	if !ok {
		return
	}
	s.disarm(w.ID)
	s.wake(ctx, w, result)
	s.refreshCycles(ctx)
}

// wakeText is the result a satisfied wait brings to who waits.
func wakeText(w Wait, targets []Target) string {
	var b strings.Builder
	if w.Kind == KindTime {
		fmt.Fprintf(&b, "Chegou o horário da espera %s (%s).", w.ID, w.Reason)
		return b.String()
	}
	fmt.Fprintf(&b, "A espera %s (aguardando %s) foi satisfeita.", w.ID, w.Reason)
	for _, t := range targets {
		if !t.Done {
			continue
		}
		if t.Result == "" {
			fmt.Fprintf(&b, "\n- %s concluiu.", t.Name)
			continue
		}
		fmt.Fprintf(&b, "\n- %s concluiu: %s", t.Name, t.Result)
	}
	return b.String()
}

func (s *Service) wake(ctx context.Context, w Wait, text string) {
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: w.Workspace, Target: w.Waiter, Kind: inbox.KindWake, Sender: inbox.SystemSender, Text: text,
		Refs: inbox.Refs{Wait: w.ID, Table: w.Table, Quest: w.Quest}}); err != nil {
		s.log.Warn("waits: the wake of a wait was not queued", "wait", w.ID, "waiter", w.Waiter, "err", err)
	}
}

// expire closes the wait as expired and tells who waits what to do. The
// engine never sends the same request again by itself.
func (s *Service) expire(ctx context.Context, w Wait) {
	s.wmu.Lock()
	ok, err := s.st.End(ctx, w.ID, Expired, "", s.clk.Now(), w.Targets)
	s.wmu.Unlock()
	if err != nil || !ok {
		if err != nil {
			s.log.Warn("waits: an expired wait was not recorded", "wait", w.ID, "err", err)
		}
		return
	}
	s.disarm(w.ID)
	s.wake(ctx, w, expiredText(w, s.clk.Now().Sub(w.CreatedAt)))
	s.refreshCycles(ctx)
}

func expiredText(w Wait, waited time.Duration) string {
	mins := int(waited.Round(time.Minute) / time.Minute)
	return fmt.Sprintf("A espera %s (aguardando %s) estourou o prazo, depois de %d min. Nada foi reenviado. Você pode: consultar com `mestre check`, "+
		"reenviar com mais contexto, trocar o personagem ou avisar os usuários.", w.ID, w.Reason, mins)
}

// targetDone marks a target as done in the active waits of a type that wait
// for it (for the waiter given, when one is) and satisfies the waits that this
// completes. It returns how many waits it touched.
func (s *Service) targetDone(ctx context.Context, kind Kind, targetID, forWaiter, result string) int {
	s.wmu.Lock()
	all, err := s.st.ActiveWaits(ctx)
	if err != nil {
		s.wmu.Unlock()
		s.log.Warn("waits: the active waits were not read", "err", err)
		return 0
	}
	type change struct {
		w       Wait
		targets []Target
		done    bool
	}
	var changes []change
	for _, w := range all {
		if w.Kind != kind || (forWaiter != "" && w.Waiter != forWaiter) {
			continue
		}
		targets := append([]Target(nil), w.Targets...)
		touched := false
		for i, t := range targets {
			if t.ID == targetID && !t.Done {
				targets[i].Done, targets[i].Result = true, result
				touched = true
			}
		}
		if !touched {
			continue
		}
		w.Targets = targets
		if w.satisfied() {
			changes = append(changes, change{w, targets, true})
			continue
		}
		if err := s.st.SetTargets(ctx, w.ID, targets); err != nil {
			s.log.Warn("waits: a target done was not recorded", "wait", w.ID, "err", err)
		}
		changes = append(changes, change{w, targets, false})
	}
	s.wmu.Unlock()
	for _, c := range changes {
		if c.done {
			s.satisfy(ctx, c.w, c.targets)
		}
	}
	if len(changes) > 0 {
		s.refreshCycles(ctx)
	}
	return len(changes)
}

// StepDone says a step of a quest is done, with its result (the quests call
// it, stage 9).
func (s *Service) StepDone(ctx context.Context, stepID, result string) {
	s.targetDone(ctx, KindSteps, stepID, "", result)
}

// ApprovalDecided says a request of approval was decided; text says how.
func (s *Service) ApprovalDecided(ctx context.Context, request int64, text string) {
	s.targetDone(ctx, KindApproval, approvalTarget(request), "", text)
}

// SyncDone says a synchronization ended.
func (s *Service) SyncDone(ctx context.Context, ref, text string) {
	s.targetDone(ctx, KindSync, ref, "", text)
}

// Done implements characters.Observer: the done of a character satisfies the
// waits for it, and is the answer of the ask its turn handled.
func (s *Service) Done(ctx context.Context, c characters.Character, text string) error {
	s.mu.Lock()
	s.dones[c.ID] = text
	s.mu.Unlock()
	s.targetDone(ctx, KindCharacter, c.ID, "", text)
	return nil
}

// Cancel closes a wait as cancelled (the missions do it with theirs).
func (s *Service) Cancel(ctx context.Context, id string) error {
	w, err := s.st.GetWait(ctx, id)
	if err != nil {
		return err
	}
	s.cancelWait(ctx, w)
	return nil
}

func (s *Service) cancelWait(ctx context.Context, w Wait) {
	s.wmu.Lock()
	ok, err := s.st.End(ctx, w.ID, Cancelled, "", s.clk.Now(), w.Targets)
	s.wmu.Unlock()
	if err != nil || !ok {
		return
	}
	s.disarm(w.ID)
	// Nothing is delivered for a wait that was cancelled: a character still
	// waiting for it goes back to idle.
	if s.o.Waker != nil {
		if rest, err := s.st.ActiveOf(ctx, w.Waiter); err == nil && len(rest) == 0 {
			if err := s.o.Waker.WaitResolved(ctx, w.Waiter); err != nil {
				s.log.Warn("waits: a cancelled wait was not resolved on the machine", "wait", w.ID, "err", err)
			}
		}
	}
	s.refreshCycles(ctx)
}

// CancelQuest cancels the waits of a quest that was cancelled.
func (s *Service) CancelQuest(ctx context.Context, quest string) error {
	all, err := s.st.ActiveWaits(ctx)
	if err != nil {
		return err
	}
	for _, w := range all {
		if w.Quest == quest {
			s.cancelWait(ctx, w)
		}
	}
	return nil
}

// CancelFor cancels what a character that ended was waiting for and what was
// asked of it: its waits, its asks, and the asks made to it, whose askers are
// told.
func (s *Service) CancelFor(ctx context.Context, c characters.Character) {
	if ws, err := s.st.ActiveOf(ctx, c.ID); err == nil {
		for _, w := range ws {
			s.cancelWait(ctx, w)
		}
	}
	asks, err := s.st.OpenAsks(ctx)
	if err != nil {
		return
	}
	for _, a := range asks {
		switch c.ID {
		case a.Asker:
			s.endAsk(ctx, a, AskCancelled, "")
		case a.Target:
			s.endAsk(ctx, a, AskCancelled, fmt.Sprintf("%s foi encerrado antes de responder ao seu pedido %s.", c.Name, a.ID))
		}
	}
}

// StateChanged implements characters.Observer: what depends on a character
// that ended goes with it.
func (s *Service) StateChanged(ctx context.Context, c characters.Character, _ characters.State) error {
	if c.State == characters.Stopped {
		s.CancelFor(ctx, c)
	}
	return nil
}

// Active implements characters.WaitProbe: the character with an active wait
// ends its turn in waiting, with the reason of the oldest one.
func (s *Service) Active(ctx context.Context, characterID string) (string, bool, error) {
	ws, err := s.st.ActiveOf(ctx, characterID)
	if err != nil {
		return "", false, err
	}
	if len(ws) > 0 {
		return ws[0].Reason, true, nil
	}
	slots, err := s.o.Registry.ActiveSlotWaits(ctx)
	if err != nil {
		return "", false, err
	}
	for _, sw := range slots {
		if req, ok := slotRequest(sw); ok && req.Creator.Kind == characters.CreatedByCharacter && req.Creator.ID == characterID {
			return "vaga", true, nil
		}
	}
	return "", false, nil
}

// Recover brings back the waits after a restart of the engine
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#persistência-e-retomada): a
// wait for a time that came during the fall is satisfied, one whose time ran
// out is expired (the waiter is told), the others get their timers again. The
// calls of the CLI did not survive: the asks are detached, and their answers
// go to the inbox.
func (s *Service) Recover(ctx context.Context) error {
	if err := s.st.DetachAll(ctx); err != nil {
		return err
	}
	all, err := s.st.ActiveWaits(ctx)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	for _, w := range all {
		switch {
		case w.Kind == KindTime && !w.UntilAt.After(now):
			s.satisfy(ctx, w, w.Targets)
		case w.Kind != KindTime && !w.TimeoutAt.IsZero() && !w.TimeoutAt.After(now):
			s.expire(ctx, w)
		default:
			s.arm(w)
		}
	}
	s.refreshCycles(ctx)
	return nil
}
