package tables

import (
	"context"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

// apply moves a table with an event: the diagram decides, and the state
// changes only if the table is still where it was read.
func (s *Service) apply(ctx context.Context, t Table, ev Event) (Table, error) {
	to, err := Transition(t.State, ev)
	if err != nil {
		return t, err
	}
	moved, err := s.store.Move(ctx, t.Workspace, t.ID, t.State, to, s.clk.Now())
	if err != nil {
		return t, err
	}
	if !moved {
		cur, gerr := s.store.Get(ctx, t.Workspace, t.ID)
		if gerr != nil {
			return t, gerr
		}
		return cur, fmt.Errorf("%w: %s in %s (it changed meanwhile)", ErrBadTransition, ev, cur.State)
	}
	cur, err := s.store.Get(ctx, t.Workspace, t.ID)
	if err == nil && ev == EventCancelled && s.o.Dependencies != nil {
		if derr := s.o.Dependencies.Dropped(ctx, t.Workspace, cur); derr != nil {
			s.log.Warn("tables: the waits of a cancelled table were not ended", "table", t.ID, "err", derr)
		}
		if derr := s.o.Dependencies.Cancelled(ctx, t.Workspace, cur); derr != nil {
			s.log.Warn("tables: the tables that wait for a cancelled one were not told", "table", t.ID, "err", derr)
		}
	}
	return cur, err
}

// PROpened is told when `mestre pr open` (or `pr update`) concluded: a table
// in work goes to review; one already in review only learns the new head of
// the pull request.
func (s *Service) PROpened(ctx context.Context, workspace, id string, number int, head string) (Table, error) {
	defer s.lock(workspace, id)()
	t, err := s.store.Get(ctx, workspace, id)
	if err != nil {
		return Table{}, err
	}
	switch t.State {
	case StateWorking:
		if t, err = s.apply(ctx, t, EventPROpened); err != nil {
			return t, err
		}
	case StateInReview:
	default:
		return t, fmt.Errorf("%w: pr_opened in %s", ErrBadTransition, t.State)
	}
	if head == "" {
		head = t.PRHead // an unknown head does not erase the one known
	}
	if err := s.store.SetPR(ctx, workspace, id, s.clk.Now(), number, head); err != nil {
		return t, err
	}
	if err := s.Delivered(ctx, workspace, id); err != nil {
		s.log.Warn("tables: the delivery of the pull request was not recorded", "table", id, "err", err)
	}
	return s.store.Get(ctx, workspace, id)
}

// MissionStarted is told when a mission begins: a table in review goes back
// to work. In any other state that is not over it changes nothing.
func (s *Service) MissionStarted(ctx context.Context, workspace, id string) (Table, error) {
	defer s.lock(workspace, id)()
	t, err := s.store.Get(ctx, workspace, id)
	if err != nil {
		return Table{}, err
	}
	switch {
	case t.State == StateInReview:
		return s.apply(ctx, t, EventMission)
	case Over(t.State) || t.State == StateClosing || t.State == StateCancelling:
		return t, fmt.Errorf("%w: mission_started in %s", ErrBadTransition, t.State)
	}
	return t, nil
}

// MergeConfirmed is told when the merge of the pull request of the table is
// confirmed, by the result of `mestre gh merge` or by the poll of the events
// of GitHub. The table stays where it is; the engine marks it as merged and
// puts an item `github` in the inbox of the Grand Master, who starts the
// closing with `table close`. Telling it twice does nothing.
func (s *Service) MergeConfirmed(ctx context.Context, workspace, id, head string) (Table, error) {
	t, err := s.confirmMerge(ctx, workspace, id, head)
	// The tables that wait for this one are released outside its lock, and
	// again each time the merge is told (releasing is idempotent), so a
	// failure to release is made good by the next poll: what they deliver
	// to the characters may come back to this table.
	if err == nil && s.o.Dependencies != nil {
		if derr := s.o.Dependencies.Merged(ctx, workspace, t); derr != nil {
			s.log.Warn("tables: the tables that wait for this one were not released", "table", id, "err", derr)
		}
	}
	return t, err
}

// confirmMerge marks the table as merged.
func (s *Service) confirmMerge(ctx context.Context, workspace, id, head string) (Table, error) {
	defer s.lock(workspace, id)()
	t, err := s.store.Get(ctx, workspace, id)
	if err != nil {
		return Table{}, err
	}
	switch {
	case t.Merged:
		return t, nil
	case t.State != StateInReview && t.State != StateWorking:
		return t, fmt.Errorf("%w: merge confirmed in %s", ErrBadTransition, t.State)
	case t.PRNumber == 0:
		return t, codedErr(ErrBadTransition, "table.no_pr", "table", id)
	}
	if head == "" {
		head = t.PRHead
	}
	if err := s.store.SetMerged(ctx, workspace, id, s.clk.Now(), head); err != nil {
		return t, err
	}
	t, err = s.store.Get(ctx, workspace, id)
	if err != nil {
		return t, err
	}
	if err := s.tellGrandMaster(ctx, t, inbox.KindGitHub, fmt.Sprintf("PR da mesa %s mesclado", id)); err != nil {
		s.log.Warn("tables: the Grand Master was not told of the merge", "table", id, "err", err)
	}
	return t, nil
}

// tellGrandMaster puts an item in the inbox of the Grand Master of the
// workspace of the table.
func (s *Service) tellGrandMaster(ctx context.Context, t Table, kind inbox.Kind, text string) error {
	gm, ok, err := s.o.Registry.GrandMaster(ctx, t.Workspace)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("the workspace has no Grand Master")
	}
	_, err = s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: t.Workspace, Target: gm.ID, Kind: kind, Sender: "GitHub", Text: text, Refs: inbox.Refs{Table: t.ID}})
	return err
}

// tellMaster tells the table master of the table.
func (s *Service) tellMaster(ctx context.Context, t Table, text string) {
	if err := s.o.Notifier.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToTableMaster},
		Workspace: t.Workspace, Table: t.ID, Text: text}); err != nil {
		s.log.Warn("tables: the table master was not told", "table", t.ID, "err", err)
	}
}

// StateChanged implements characters.Observer: a table master that begins a
// turn has the request, and its table goes from open to work.
func (s *Service) StateChanged(ctx context.Context, c characters.Character, _ characters.State) error {
	if c.Role != characters.RoleTableMaster || c.Table == "" || c.State != characters.Busy {
		return nil
	}
	defer s.lock(c.Workspace, c.Table)()
	t, err := s.store.Get(ctx, c.Workspace, c.Table)
	if err != nil || t.State != StateOpen {
		return nil
	}
	_, err = s.apply(ctx, t, EventReady)
	return err
}

// Done implements characters.Observer: a character that finished has its
// commits in the worktree, and the engine registers the tip of the branch it
// sees (after the check of the refs).
func (s *Service) Done(ctx context.Context, c characters.Character, _ string) error {
	if c.Table == "" {
		return nil
	}
	t, err := s.store.Get(ctx, c.Workspace, c.Table)
	if err != nil {
		return nil
	}
	_, err = s.checkLocked(ctx, t)
	return err
}

// TurnEnded implements characters.Observer.
func (s *Service) TurnEnded(context.Context, characters.Character, string) error { return nil }

// PauseRequest is a pause or a resume.
type PauseRequest struct {
	Reason string
	// By is a user, a character, or "usage_limit" when the limit of use asked.
	By string
}

// Pause marks a table as paused (docs/orquestracao/mesas.md#pausa). Nothing
// is interrupted: the inbox stops delivering to the characters of the table,
// the missions do not begin new steps, and who is busy finishes the turn.
func (s *Service) Pause(ctx context.Context, workspace, id string, req PauseRequest) error {
	defer s.lock(workspace, id)()
	t, err := s.store.Get(ctx, workspace, id)
	if err != nil {
		return err
	}
	if Over(t.State) {
		return fmt.Errorf("%w: pause in %s", ErrBadTransition, t.State)
	}
	if t.Paused {
		return nil
	}
	return s.store.SetPaused(ctx, workspace, id, s.clk.Now(), true, req.Reason, req.By)
}

// Resume takes the mark off and delivers what waited, in the order of the
// queue.
func (s *Service) Resume(ctx context.Context, workspace, id string) error {
	unlock := s.lock(workspace, id)
	t, err := s.store.Get(ctx, workspace, id)
	if err != nil {
		unlock()
		return err
	}
	if !t.Paused {
		unlock()
		return nil
	}
	err = s.store.SetPaused(ctx, workspace, id, s.clk.Now(), false, "", "")
	unlock()
	if err != nil {
		return err
	}
	return s.o.Inbox.PumpAll(ctx)
}

// PauseWorkspace pauses the workspace: every table is paused and the Grand
// Master hears only the users.
func (s *Service) PauseWorkspace(ctx context.Context, workspace string) error {
	return s.o.Workspaces.Pause(ctx, workspace)
}

// ResumeWorkspace resumes the workspace and delivers what waited.
func (s *Service) ResumeWorkspace(ctx context.Context, workspace string) error {
	if err := s.o.Workspaces.Resume(ctx, workspace); err != nil {
		return err
	}
	return s.o.Inbox.PumpAll(ctx)
}

// Paused implements inbox.Pauses: the table (or, with table "", the
// workspace) is paused. A paused workspace pauses every table.
func (s *Service) Paused(ctx context.Context, workspace, table string) (bool, error) {
	w, err := s.o.Workspaces.Get(ctx, workspace)
	if err != nil {
		return false, err
	}
	if w.State == workspaces.StatePaused {
		return true, nil
	}
	if table == "" {
		return false, nil
	}
	t, err := s.store.Get(ctx, workspace, table)
	if err != nil {
		if errors.Is(err, ErrNoTable) {
			return false, nil
		}
		return false, err
	}
	if t.Paused {
		return true, nil
	}
	// A table that waits for another epic has not been handed its request yet.
	if t.State == StateOpen {
		return s.Held(ctx, workspace, table)
	}
	return false, nil
}

// Held says whether the table waits for the merge of another table: its
// epic depends on an epic that was not merged (or was cancelled and the Grand
// Master did not decide). A table in the state open is held from its first
// message; one already at work only starts no new mission
// (docs/orquestracao/dependencia-entre-mesas.md). A failure to ask counts as held.
func (s *Service) Held(ctx context.Context, workspace, table string) (bool, error) {
	if s.o.Dependencies == nil {
		return false, nil
	}
	t, err := s.store.Get(ctx, workspace, table)
	if err != nil {
		if errors.Is(err, ErrNoTable) {
			return false, nil
		}
		return true, err
	}
	if Over(t.State) {
		return false, nil
	}
	held, err := s.o.Dependencies.Held(ctx, workspace, t.Epic)
	if err != nil {
		return true, err
	}
	return held, nil
}
