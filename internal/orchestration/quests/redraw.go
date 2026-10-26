package quests

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Redraw replaces the steps of a draft with the ones of def, as the canvas
// does when a user moves an arrow or renumbers a step. The name of the quest
// stays: only the steps, the order and the returns change. Only a draft is
// redrawn; one that already runs changes in the steps that are still pending,
// which the canvas does not offer yet (docs/painel/canvas.md).
func (s *Service) Redraw(ctx context.Context, id string, def Definition) (View, error) {
	cur, err := s.st.Quest(ctx, id)
	if err != nil {
		return View{}, err
	}
	if cur.State != Draft {
		return View{}, fmt.Errorf("%w: %s is %s", ErrInvalidState, cur.Name, cur.State)
	}
	def.Name = cur.Name
	if def.Issue == 0 {
		def.Issue = cur.Def.Issue
	}
	if err := def.Validate(); err != nil {
		return View{}, err
	}
	if err := s.check(ctx, cur.Workspace, cur.Table, def); err != nil {
		return View{}, err
	}
	now := s.clk.Now()
	err = s.st.Do(ctx, func(tx *Tx) error {
		q, err := tx.Quest(id)
		if err != nil {
			return err
		}
		if q.State != Draft {
			return fmt.Errorf("%w: %s is %s", ErrInvalidState, q.Name, q.State)
		}
		return tx.replaceSteps(q, def, now)
	})
	if err != nil {
		return View{}, err
	}
	q, err := s.st.Quest(ctx, id)
	if err != nil {
		return View{}, err
	}
	if s.o.Connections != nil {
		if err := s.o.Connections.EndQuest(ctx, id); err != nil {
			s.log.Warn("quests: the connections of the redrawn quest were not removed", "quest", id, "err", err)
		}
	}
	s.ensureReview(ctx, q)
	s.link(ctx, q)
	s.mirror(ctx, id)
	return s.Status(ctx, q)
}

// replaceSteps writes the new definition and its pending steps in place of the
// old ones; the quest is marked as having something to mirror to GitHub.
func (t *Tx) replaceSteps(q Quest, def Definition, now time.Time) error {
	b, err := json.Marshal(def)
	if err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, `UPDATE quests SET definition = ?, max_returns = ?, issue_dirty = issue_dirty + 1, updated_at = ? WHERE id = ?`, string(b), def.Max(), ms(now), q.ID); err != nil {
		return err
	}
	if _, err := t.tx.ExecContext(t.ctx, `DELETE FROM quest_steps WHERE quest = ?`, q.ID); err != nil {
		return err
	}
	for i, sd := range def.Steps {
		st := Step{UID: q.ID + "/" + sd.ID, Quest: q.ID, ID: sd.ID, Pos: i, State: StepPending, UpdatedAt: now}
		if _, err := t.tx.ExecContext(t.ctx, `INSERT INTO quest_steps (`+stepColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			st.UID, st.Quest, st.ID, st.Pos, string(st.State), st.Character, st.Returns, st.Entry, st.Blocker, b2i(st.Approved), st.Approval, st.ResumeNote,
			b2i(st.DiscardResult), ms(st.StuckAt), b2i(st.Escalated), st.SubIssue, b2i(st.SubClosed), st.CommentedRound, ms(st.UpdatedAt)); err != nil {
			return err
		}
	}
	return nil
}
