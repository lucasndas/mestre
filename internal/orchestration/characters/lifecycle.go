package characters

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// Report applies a transition its machine saw. The machine is the truth about
// the process, so the report is applied even when the diagram has no such
// edge (it is logged); a report older than one already applied is dropped.
func (s *Service) Report(ctx context.Context, r Report) error {
	s.mu.Lock()
	if r.Seq != 0 && r.Seq <= s.seq[r.Character] {
		s.mu.Unlock()
		return nil
	}
	s.seq[r.Character] = r.Seq
	s.mu.Unlock()
	c, err := s.o.Registry.Get(ctx, r.Character)
	if err != nil {
		return err
	}
	if c.State == Stopped {
		return nil
	}
	if !r.State.Valid() {
		return fmt.Errorf("character %s: unknown state %q", r.Character, r.State)
	}
	at := r.At
	if at.IsZero() {
		at = s.clk.Now()
	}
	if r.State != c.State && !hasEdge(c.State, r.State) {
		s.log.Warn("characters: a report with no edge in the diagram, applied as the machine saw it", "id", c.ID, "from", c.State, "to", r.State, "reason", r.Reason)
	}
	from, changed, err := s.o.Registry.Transition(ctx, c.ID, r.State, Change{Reason: r.Reason, At: at, Session: r.Session, Terminal: r.Terminal, PTY: r.PTY, Reduced: r.Reduced})
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	c.State, c.Reason = r.State, r.Reason
	err = s.after(ctx, c, from)
	s.observe(func(o Observer) error { return o.StateChanged(ctx, c, from) })
	return err
}

// observe calls the Observer, if there is one; what it returns is logged.
func (s *Service) observe(f func(Observer) error) {
	if s.o.Observer == nil {
		return
	}
	if err := f(s.o.Observer); err != nil {
		s.log.Warn("characters: the observer failed", "err", err)
	}
}

// TurnEnded passes the end of a turn on to the Observer (the Runtime of the
// Citadel's machine calls it through the TurnObserver; a Tower sends it over
// the protocol).
func (s *Service) TurnEnded(ctx context.Context, id, lastMessage string) error {
	c, err := s.o.Registry.Get(ctx, id)
	if err != nil {
		return err
	}
	s.observe(func(o Observer) error { return o.TurnEnded(ctx, c, lastMessage) })
	return nil
}

func hasEdge(from, to State) bool {
	for _, t := range edges[from] {
		if t == to {
			return true
		}
	}
	return false
}

// after is what a change of state leads to on the Citadel's side.
func (s *Service) after(ctx context.Context, c Character, from State) error {
	switch c.State {
	case Idle:
		fresh, err := s.o.Registry.Get(ctx, c.ID)
		if err != nil {
			return err
		}
		return s.runScheduled(ctx, fresh)
	case Stuck:
		return s.tellMaster(ctx, c, NoticeStuck, fmt.Sprintf("%s está travado: ocupado e sem sinal há mais tempo que o limite.", c.Name))
	case Interrupted:
		if err := s.tellMaster(ctx, c, NoticeInterrupted, fmt.Sprintf("%s foi interrompido (%s).", c.Name, c.Reason)); err != nil {
			return err
		}
		if c.Kind == KindTerminal {
			return s.o.Notifier.Notify(ctx, Notice{Kind: NoticeNotResumed, To: Recipient{Kind: ToCreator}, Workspace: c.Workspace, Table: c.Table,
				Character: c.ID, Name: c.Name, Creator: c.CreatedBy,
				Text: fmt.Sprintf("O terminal %s foi interrompido; o motor não reexecuta comandos sozinho.", c.Name)})
		}
	case Stopped:
		s.mu.Lock()
		delete(s.summarized, c.ID)
		s.mu.Unlock()
		s.disconnect(ctx, c)
		return s.Pump(ctx)
	}
	return nil
}

// tellMaster warns the table master of the character's table, or the Grand
// Master for a table master.
func (s *Service) tellMaster(ctx context.Context, c Character, kind NoticeKind, text string) error {
	to := Recipient{Kind: ToTableMaster}
	if c.Role != RoleCharacter {
		to = Recipient{Kind: ToGrandMaster}
	}
	return s.o.Notifier.Notify(ctx, Notice{Kind: kind, To: to, Workspace: c.Workspace, Table: c.Table, Character: c.ID, Name: c.Name, Text: text})
}

// TaskAssigned marks a task or step delivered to the character: it is not at
// a safe point until the done that answers it.
func (s *Service) TaskAssigned(ctx context.Context, id string) error {
	return s.o.Registry.SetTaskOpen(ctx, id, true)
}

// Done registers the done of a character: its text goes to the summary of its
// end, and it is at a safe point again once it is idle.
func (s *Service) Done(ctx context.Context, id, text string) error {
	if err := s.o.Registry.SetTaskOpen(ctx, id, false); err != nil {
		return err
	}
	if err := s.o.Registry.AddDone(ctx, id, Done{At: s.clk.Now(), Text: text}); err != nil {
		return err
	}
	c, err := s.o.Registry.Get(ctx, id)
	if err != nil {
		return err
	}
	s.observe(func(o Observer) error { return o.Done(ctx, c, text) })
	// A character that is already idle (the done came late) may have been
	// waiting for it to be ended.
	if c.State != Idle {
		return nil
	}
	return s.runScheduled(ctx, c)
}

// safe says whether a character is at a safe point: idle (waiting is not
// idle), nothing in its inbox, the done of the last task or step delivered
// (docs/orquestracao/personagens.md#encerramento-seguro). Pinned is checked
// by the callers, who must record the refusal.
func (s *Service) safe(ctx context.Context, c Character) (bool, error) {
	if c.State != Idle || c.TaskOpen {
		return false, nil
	}
	if s.o.Inbox != nil {
		n, err := s.o.Inbox.Pending(ctx, c.ID)
		if err != nil || n > 0 {
			return false, err
		}
	}
	return true, nil
}

// DismissStatus is how a dismiss ended.
type DismissStatus string

// The answers of dismiss (docs/cli/coordenacao.md).
const (
	Dismissed DismissStatus = "dismissed"
	Scheduled DismissStatus = "scheduled"
)

// DismissRequest asks to end a character or a terminal.
type DismissRequest struct {
	// ID or Name (in Table of Workspace) finds the character.
	ID, Name         string
	Workspace, Table string
	By               Creator
	Reason           string
}

// DismissResult is the answer of Dismiss.
type DismissResult struct {
	Status    DismissStatus
	Character Character
}

// Dismiss ends a character at its safe point: now if it is at one, otherwise
// the end is scheduled for the next one and the answer is "scheduled". A
// pinned character is never ended and the refusal is recorded; a stuck or
// interrupted one is ended by a table master or an Administrator, with a
// line in the audit
// (docs/orquestracao/personagens.md#encerramento-seguro).
func (s *Service) Dismiss(ctx context.Context, req DismissRequest) (DismissResult, error) {
	c, err := s.find(ctx, req)
	if err != nil {
		return DismissResult{}, err
	}
	if err := s.mayDismiss(ctx, req.By, c); err != nil {
		return DismissResult{}, err
	}
	if c.Pinned {
		s.refuse(ctx, c, "dismiss", req.By)
		return DismissResult{}, fmt.Errorf("%w: %s", ErrPinned, c.Name)
	}
	reason := req.Reason
	if reason == "" {
		reason = "dismiss"
	}
	switch c.State {
	case Stopped:
		return DismissResult{}, fmt.Errorf("%w: %s", ErrNoCharacter, c.Name)
	case Stopping:
		return DismissResult{Status: Dismissed, Character: c}, nil
	case Stuck, Interrupted:
		s.audit(ctx, c, "character.force_stopped", map[string]string{"by": string(req.By.Kind) + ":" + req.By.ID, "state": string(c.State), "reason": reason})
		if err := s.executeStop(ctx, c, reason); err != nil {
			return DismissResult{}, err
		}
		return DismissResult{Status: Dismissed, Character: c}, nil
	}
	ok, err := s.safe(ctx, c)
	if err != nil {
		return DismissResult{}, err
	}
	if ok {
		if err := s.executeStop(ctx, c, reason); err != nil {
			return DismissResult{}, err
		}
		return DismissResult{Status: Dismissed, Character: c}, nil
	}
	if err := s.o.Registry.SetStop(ctx, c.ID, s.clk.Now(), encodeBy(req.By), reason); err != nil {
		return DismissResult{}, err
	}
	c.StopRequestedAt = s.clk.Now()
	return DismissResult{Status: Scheduled, Character: c}, nil
}

func encodeBy(by Creator) string { return string(by.Kind) + ":" + by.ID }

func decodeBy(v string) Creator {
	kind, id, _ := strings.Cut(v, ":")
	return Creator{Kind: CreatorKind(kind), ID: id}
}

// find the character of a request.
func (s *Service) find(ctx context.Context, req DismissRequest) (Character, error) {
	if req.ID != "" {
		return s.o.Registry.Get(ctx, req.ID)
	}
	c, err := s.o.Registry.ByName(ctx, req.Workspace, req.Table, req.Name)
	if err != nil {
		return Character{}, fmt.Errorf("%w: %q na mesa %q", ErrNoCharacter, req.Name, req.Table)
	}
	return c, nil
}

// mayDismiss says whether who asks may end the character: the engine; a user
// of Administrator level or above; the Grand Master, for the characters of
// its workspace; a table master, for the characters of its own table (another
// table is not_visible).
func (s *Service) mayDismiss(ctx context.Context, by Creator, c Character) error {
	switch by.Kind {
	case CreatedByEngine:
		return nil
	case CreatedByUser:
		if by.Level == LevelOwner || by.Level == LevelAdministrator {
			return nil
		}
		return fmt.Errorf("%w: só o Dono e os Administradores encerram personagens", ErrLevelDenied)
	case CreatedByCharacter:
		who, err := s.o.Registry.Get(ctx, by.ID)
		if err != nil {
			return fmt.Errorf("%w: quem pediu não é um personagem", ErrLevelDenied)
		}
		switch {
		case who.Role == RoleGrandMaster && who.Workspace == c.Workspace && c.Role != RoleGrandMaster:
			return nil
		case who.Role == RoleTableMaster && c.Role == RoleCharacter:
			if who.Workspace != c.Workspace || who.Table != c.Table {
				return fmt.Errorf("%w: %s", ErrNotVisible, c.Name)
			}
			return nil
		}
	}
	return ErrLevelDenied
}

// executeStop ends a character: the summary first, then the machine ends the
// process (the normal exit of the provider, and after 30 s the tree).
func (s *Service) executeStop(ctx context.Context, c Character, reason string) error {
	if err := s.summarize(ctx, c); err != nil {
		return err
	}
	if err := s.o.Machines.Stop(ctx, c.Machine, StopCommand{Character: c.ID, Final: true, Reason: reason}); err != nil {
		return fmt.Errorf("end %s: %w", c.Name, err)
	}
	return s.o.Registry.SetStop(ctx, c.ID, time.Time{}, "", "")
}

// summarize writes the summary of a character that ends in the scroll of its
// quest or in the history of its table, once.
func (s *Service) summarize(ctx context.Context, c Character) error {
	if s.o.Scrolls == nil {
		return nil
	}
	s.mu.Lock()
	done := s.summarized[c.ID]
	s.mu.Unlock()
	if done {
		return nil
	}
	dones, err := s.o.Registry.Dones(ctx, c.ID)
	if err != nil {
		return err
	}
	sum := Summary{Character: c, Dones: dones}
	if s.o.Usage != nil {
		if sum.Usage, err = s.o.Usage.Usage(ctx, c.ID); err != nil {
			s.log.Warn("characters: the use of a character was not read", "id", c.ID, "err", err)
		}
	}
	if err := s.o.Scrolls.Record(ctx, sum); err != nil {
		return fmt.Errorf("summary of %s: %w", c.Name, err)
	}
	s.mu.Lock()
	s.summarized[c.ID] = true
	s.mu.Unlock()
	return nil
}

// runScheduled ends a character whose end was asked for, if it is at a safe
// point now, and tells whoever asked.
func (s *Service) runScheduled(ctx context.Context, c Character) error {
	if c.StopRequestedAt.IsZero() {
		return nil
	}
	if c.Pinned {
		s.refuse(ctx, c, "scheduled", decodeBy(c.StopRequestedBy))
		return nil
	}
	ok, err := s.safe(ctx, c)
	if err != nil || !ok {
		return err
	}
	if err := s.executeStop(ctx, c, c.StopReason); err != nil {
		s.log.Warn("characters: a scheduled end did not happen", "id", c.ID, "err", err)
		return nil
	}
	by := decodeBy(c.StopRequestedBy)
	if by.Kind == CreatedByEngine || by.Kind == "" {
		return nil
	}
	return s.o.Notifier.Notify(ctx, Notice{Kind: NoticeDismissed, To: Recipient{Kind: ToCreator}, Workspace: c.Workspace, Table: c.Table,
		Character: c.ID, Name: c.Name, Creator: by, Text: fmt.Sprintf("%s foi encerrado, como pedido.", c.Name)})
}

// refuse records that the engine did not end a pinned character, once for
// each cause.
func (s *Service) refuse(ctx context.Context, c Character, cause string, by Creator) {
	key := c.ID + "/" + cause
	s.mu.Lock()
	seen := s.refused[key]
	if cause != "dismiss" {
		s.refused[key] = true
	}
	s.mu.Unlock()
	if seen {
		return
	}
	s.audit(ctx, c, "character.end_refused_pinned", map[string]string{"cause": cause, "by": encodeBy(by)})
}

// audit writes a line of the audit trail about a character.
func (s *Service) audit(ctx context.Context, c Character, action string, detail map[string]string) {
	detail["character"], detail["name"] = c.ID, c.Name
	raw, _ := json.Marshal(detail)
	if err := s.o.Registry.Audit(ctx, s.ids.NewString(), s.clk.Now(), c.Machine, action, raw); err != nil {
		s.log.Warn("characters: the audit was not written", "action", action, "err", err)
	}
}

// Pin pins a character on the canvas, or unpins it. Only a user pins and
// unpins; a pinned character is never ended automatically, nor by dismiss.
func (s *Service) Pin(ctx context.Context, id string, pinned bool, by Creator) error {
	if by.Kind != CreatedByUser || by.Level != LevelOwner && by.Level != LevelAdministrator {
		return fmt.Errorf("%w: só um usuário fixa e desafixa", ErrLevelDenied)
	}
	c, err := s.o.Registry.Get(ctx, id)
	if err != nil {
		return err
	}
	if !c.State.Alive() {
		return fmt.Errorf("%w: %s", ErrNoCharacter, c.Name)
	}
	return s.o.Registry.SetPinned(ctx, id, pinned)
}

// EndQuest has the ephemeral characters of a quest ended when it ends
// (finished or cancelled), each at its safe point.
func (s *Service) EndQuest(ctx context.Context, quest string) error {
	cs, err := s.o.Registry.list(ctx, `state <> 'stopped' AND quest = ? AND lifecycle = 'ephemeral' AND role = 'character'`, quest)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if c.Pinned {
			s.refuse(ctx, c, "quest", Creator{Kind: CreatedByEngine})
			continue
		}
		if err := s.o.Registry.SetStop(ctx, c.ID, s.clk.Now(), encodeBy(Creator{Kind: CreatedByEngine}), "quest ended"); err != nil {
			return err
		}
		c.StopRequestedAt, c.StopReason = s.clk.Now(), "quest ended"
		c.StopRequestedBy = encodeBy(Creator{Kind: CreatedByEngine})
		if err := s.runScheduled(ctx, c); err != nil {
			return err
		}
	}
	return nil
}

// Sweep is the periodic work of the lifecycle (the engine runs it once a
// minute): the scheduled ends that reached a safe point, the ephemerals after
// their grace, the persistents after their idle time, the requests that wait
// for a vacancy, and the daily cleaning of the state history. It never ends a
// character that is not at a safe point or that is pinned.
func (s *Service) Sweep(ctx context.Context) error {
	now := s.clk.Now()
	cs, err := s.o.Registry.Alive(ctx)
	if err != nil {
		return err
	}
	for _, c := range cs {
		if c.Role != RoleCharacter {
			continue
		}
		if !c.StopRequestedAt.IsZero() {
			if err := s.runScheduled(ctx, c); err != nil {
				return err
			}
			continue
		}
		why, due, err := s.lifecycleDue(ctx, c, now)
		if err != nil {
			return err
		}
		if !due {
			continue
		}
		if c.Pinned {
			s.refuse(ctx, c, "lifecycle", Creator{Kind: CreatedByEngine})
			continue
		}
		ok, err := s.safe(ctx, c)
		if err != nil {
			return err
		}
		if ok {
			if err := s.executeStop(ctx, c, why); err != nil {
				s.log.Warn("characters: the end by the lifecycle did not happen", "id", c.ID, "err", err)
			}
		}
	}
	if err := s.Pump(ctx); err != nil {
		return err
	}
	s.mu.Lock()
	due := now.Sub(s.lastPruned) >= 24*time.Hour
	if due {
		s.lastPruned = now
	}
	s.mu.Unlock()
	if due {
		if _, err := s.o.Registry.PruneLog(ctx, now, s.o.LogRetention); err != nil {
			return err
		}
	}
	return nil
}

// lifecycleDue says whether the lifecycle of the sheet wants the character
// ended now, and why.
func (s *Service) lifecycleDue(ctx context.Context, c Character, now time.Time) (string, bool, error) {
	if c.Kind == KindTerminal || c.State != Idle || c.IdleSince.IsZero() {
		return "", false, nil
	}
	switch c.Lifecycle {
	case Persistent:
		if c.MaxIdle > 0 && now.Sub(c.IdleSince) >= c.MaxIdle {
			return fmt.Sprintf("idle for %s", c.MaxIdle), true, nil
		}
	case Ephemeral:
		if c.Quest != "" {
			return "", false, nil // it ends with its quest
		}
		dones, err := s.o.Registry.Dones(ctx, c.ID)
		if err != nil {
			return "", false, err
		}
		if len(dones) > 0 && now.Sub(c.IdleSince) >= s.o.EphemeralGrace {
			return "task finished", true, nil
		}
	}
	return "", false, nil
}

// Run does the periodic work of the lifecycle (Sweep) every interval until ctx
// ends.
func (s *Service) Run(ctx context.Context, every time.Duration) error {
	tk := s.clk.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tk.C():
			if err := s.Sweep(ctx); err != nil {
				s.log.Warn("characters: the sweep of the lifecycle failed", "err", err)
			}
		}
	}
}

// ReplaceRequest is `recruit --replace`.
type ReplaceRequest struct {
	Workspace, Table, Name string
	Sheet, Class           string
	By                     Creator
}

// Replace changes the sheet of a character keeping its name: the old one ends
// at its next safe point, and the new one starts when it is gone, on the same
// machine if it still fits. It answers like Create: the new character, or the
// pending wait. The new one keeps who created the old (its superior).
func (s *Service) Replace(ctx context.Context, rq ReplaceRequest) (CreateResult, error) {
	old, err := s.o.Registry.ByName(ctx, rq.Workspace, rq.Table, rq.Name)
	if err != nil {
		return CreateResult{}, err
	}
	creator := old.CreatedBy
	req := CreateRequest{Name: old.Name, Sheet: rq.Sheet, Class: rq.Class, Workspace: old.Workspace, Table: old.Table, Role: old.Role,
		Creator: rq.By, Quest: old.Quest, ReplaceOf: old.ID, Prefer: old.Machine, Inherit: &creator}
	if err := s.authorize(ctx, req); err != nil {
		return CreateResult{}, err
	}
	s.mu.Lock()
	s.replacing[old.ID] = true
	s.mu.Unlock()
	forget := func() {
		s.mu.Lock()
		delete(s.replacing, old.ID)
		s.mu.Unlock()
	}
	if _, err := s.Dismiss(ctx, DismissRequest{ID: old.ID, By: rq.By, Reason: "replaced by sheet " + rq.Sheet}); err != nil {
		forget()
		return CreateResult{}, err
	}
	res, err := s.Create(ctx, req)
	if err != nil {
		forget()
	}
	return res, err
}

// Superior is who decides the requests of a character in the chain of
// approval (docs/orquestracao/cadeia-de-aprovacao.md#superior).
type Superior struct {
	// People is true when the chain ends in the people; ID and Name are then
	// empty.
	People bool
	ID     string
	Name   string
}

// Superior resolves the superior of a character from who created it:
//
//   - the Grand Master: the people;
//   - a table master: the Grand Master;
//   - created by a character (a recruit of a table master, the Operator of the PC
//     asked for by who asked): that character; if it ended, was removed or was
//     replaced, the superior of the creator, at once;
//   - created by the engine for a step of a quest, or by a user on the canvas:
//     the table master of the table; without one, the Grand Master; without
//     it, the people.
func (s *Service) Superior(ctx context.Context, id string) (Superior, error) {
	c, err := s.o.Registry.Get(ctx, id)
	if err != nil {
		return Superior{}, err
	}
	return s.superior(ctx, c, 0)
}

func (s *Service) superior(ctx context.Context, c Character, depth int) (Superior, error) {
	if depth > 16 {
		return Superior{People: true}, nil
	}
	fallback := func() (Superior, error) {
		if m, ok, err := s.o.Registry.TableMaster(ctx, c.Workspace, c.Table); err != nil {
			return Superior{}, err
		} else if ok && m.ID != c.ID {
			return Superior{ID: m.ID, Name: m.Name}, nil
		}
		return s.grandMaster(ctx, c.Workspace)
	}
	switch {
	case c.Role == RoleGrandMaster:
		return Superior{People: true}, nil
	case c.Role == RoleTableMaster:
		return s.grandMaster(ctx, c.Workspace)
	case c.CreatedBy.Kind == CreatedByCharacter:
		cr, err := s.o.Registry.Get(ctx, c.CreatedBy.ID)
		switch {
		case errors.Is(err, ErrNoCharacter):
			return fallback()
		case err != nil:
			return Superior{}, err
		case cr.State == Stopped || cr.State == Stopping:
			return s.superior(ctx, cr, depth+1)
		}
		return Superior{ID: cr.ID, Name: cr.Name}, nil
	}
	return fallback()
}

func (s *Service) grandMaster(ctx context.Context, workspace string) (Superior, error) {
	gm, ok, err := s.o.Registry.GrandMaster(ctx, workspace)
	if err != nil {
		return Superior{}, err
	}
	if !ok {
		return Superior{People: true}, nil
	}
	return Superior{ID: gm.ID, Name: gm.Name}, nil
}

// Get returns a character.
func (s *Service) Get(ctx context.Context, id string) (Character, error) {
	return s.o.Registry.Get(ctx, id)
}

// Alive lists the characters that are not stopped.
func (s *Service) Alive(ctx context.Context) ([]Character, error) { return s.o.Registry.Alive(ctx) }

// resumeMessage is the message of a character that restarts with no session to
// resume: where it was, so it can go on
// (docs/orquestracao/personagens.md#retomada-após-reinício).
func (s *Service) resumeMessage(ctx context.Context, c Character) string {
	var b strings.Builder
	b.WriteString("The engine restarted you after an interruption; your previous session could not be resumed.")
	if c.Quest != "" {
		fmt.Fprintf(&b, " You were working on the step of quest %s.", c.Quest)
	}
	if dones, err := s.o.Registry.Dones(ctx, c.ID); err == nil && len(dones) > 0 {
		fmt.Fprintf(&b, " Your last done was: %s", dones[len(dones)-1].Text)
	}
	b.WriteString(" Go on from there; your inbox and your waits were kept.")
	return b.String()
}

// GrandMasterView is the workspaces.GrandMaster over the registry: the state
// of the Grand Master of a workspace, and the system item told to it.
type GrandMasterView struct{ S *Service }

var _ workspaces.GrandMaster = GrandMasterView{}

// State implements workspaces.GrandMaster.
func (v GrandMasterView) State(ctx context.Context, workspace string) (workspaces.GrandMasterState, error) {
	cs, err := v.S.o.Registry.list(ctx, `role = 'grand_master' AND workspace = ?`, workspace)
	if err != nil || len(cs) == 0 {
		return workspaces.GMNone, err
	}
	c := cs[len(cs)-1]
	switch c.State {
	case Idle:
		return workspaces.GMIdle, nil
	case Waiting:
		return workspaces.GMWaiting, nil
	case Stopped:
		return workspaces.GMStopped, nil
	}
	return workspaces.GMBusy, nil
}

// NotifySystem implements workspaces.GrandMaster.
func (v GrandMasterView) NotifySystem(ctx context.Context, workspace, text string) error {
	return v.S.o.Notifier.Notify(ctx, Notice{Kind: NoticeSystem, To: Recipient{Kind: ToGrandMaster}, Workspace: workspace, Text: text})
}
