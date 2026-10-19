package quests

import (
	"context"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// StateChanged implements characters.Observer. The quests act on a character in
// four cases:
//   - it was made for a step (the vacancy opened, or it replaces another that
//     had the step): the step gets it;
//   - it is interrupted: its step goes back to preparing, to be delivered again
//     when it is back (docs/orquestracao/missoes.md#retomada);
//   - it is back: the delivery that waited for it;
//   - it is stuck, or has a turn again: the step is stuck, or running again
//     (docs/orquestracao/missoes.md#etapa-travada).
func (s *Service) StateChanged(ctx context.Context, c characters.Character, _ characters.State) error {
	if c.Step != "" && c.State.Alive() && c.State != characters.Stopping {
		s.bind(ctx, c)
	}
	steps, err := s.st.ByCharacter(ctx, c.ID)
	if err != nil {
		return err
	}
	for _, st := range steps {
		switch {
		case (st.State == StepRunning || st.State == StepStuck) && c.State == characters.Interrupted:
			if err := s.interrupted(ctx, st); err != nil {
				return err
			}
		case st.State == StepPreparing && st.Blocker == blockResume && c.State != characters.Interrupted && c.State.Alive():
			s.kick(ctx, st.Quest)
		case st.State == StepRunning && c.State == characters.Stuck:
			s.markStuck(ctx, st, "o personagem "+c.Name+" ficou travado")
		case st.State == StepStuck && (c.State == characters.Busy || c.State == characters.Blocked):
			_ = s.updateStep(ctx, st.UID, func(x *Step) {
				if x.State == StepStuck {
					x.State, x.StuckAt, x.Escalated = StepRunning, time.Time{}, false
				}
			})
		case st.State == StepPreparing && st.Blocker == blockBusy && c.State == characters.Idle:
			s.kick(ctx, st.Quest)
		}
	}
	return nil
}

// Done implements characters.Observer: the done of a step is the quests' own (Finish).
func (s *Service) Done(context.Context, characters.Character, string) error { return nil }

// TurnEnded implements characters.Observer.
func (s *Service) TurnEnded(context.Context, characters.Character, string) error { return nil }

// bind gives a step the character that was made for it, when it is not its
// character yet.
func (s *Service) bind(ctx context.Context, c characters.Character) {
	st, err := s.st.Step(ctx, c.Step)
	if err != nil || st.Character == c.ID {
		return
	}
	err = s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Step(c.Step)
		if err != nil || cur.Character == c.ID {
			return err
		}
		switch {
		case cur.State == StepPreparing && cur.Character == "":
			cur.Character, cur.Blocker = c.ID, ""
		case cur.State == StepRunning || cur.State == StepStuck:
			// It takes the place of the character the step had (recruit
			// --replace keeps the step): the work is delivered to it again.
			old, gerr := s.o.Characters.Get(ctx, cur.Character)
			if gerr == nil && old.State != characters.Stopped && old.State != characters.Stopping {
				return nil
			}
			cur.Character, cur.State, cur.Blocker, cur.ResumeNote, cur.StuckAt = c.ID, StepPreparing, blockResume, "troca de personagem", time.Time{}
		default:
			return nil
		}
		return tx.SaveStep(cur, s.clk.Now())
	})
	if err != nil {
		s.log.Warn("quests: the character was not given to its step", "character", c.ID, "step", c.Step, "err", err)
		return
	}
	s.kick(ctx, st.Quest)
}

// interrupted puts the step of an interrupted character back in preparing: it
// is delivered again, with a note, when the character is back.
func (s *Service) interrupted(ctx context.Context, st Step) error {
	err := s.updateStep(ctx, st.UID, func(x *Step) {
		if x.State.Active() {
			x.State, x.Blocker, x.ResumeNote, x.StuckAt, x.Escalated = StepPreparing, blockResume, "retomada após reinício", time.Time{}, false
		}
	})
	if err == nil {
		s.kick(ctx, st.Quest)
	}
	return err
}

// markStuck makes the step stuck and tells the Master of the table.
func (s *Service) markStuck(ctx context.Context, st Step, why string) {
	q, err := s.st.Quest(ctx, st.Quest)
	if err != nil {
		return
	}
	won := false
	err = s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Step(st.UID)
		if err != nil || cur.State != StepRunning {
			return err
		}
		cur.State, cur.StuckAt, cur.Escalated = StepStuck, s.clk.Now(), false
		won = true
		return tx.SaveStep(cur, s.clk.Now())
	})
	if err != nil || !won {
		return
	}
	sd := q.Def.Steps[st.Pos]
	s.tellMaster(ctx, q, inbox.KindSystem, fmt.Sprintf("A etapa %s da missão %s está travada: %s. Use `mestre check`/`mestre ask`, troque o personagem com `mestre recruit --replace` ou cancele a missão.", sd.Title(), q.Name, why))
}

// Sweep is the periodic work of the quests (the engine runs it once a minute):
// the steps whose character is idle for too long without a done are stuck, a
// step stuck for too long is told to the Grand Master and the users, the
// quests of a table that ended are cancelled, and every quest goes over what it
// has to advance and to mirror on GitHub.
func (s *Service) Sweep(ctx context.Context) error {
	qs, err := s.st.Live(ctx)
	if err != nil {
		return err
	}
	now := s.clk.Now()
	for _, q := range qs {
		if t, err := s.o.Tables.Table(ctx, q.Workspace, q.Table); err == nil && !t.State.Accepts() && q.State != Cancelled {
			if _, err := s.Cancel(ctx, q.ID, "a mesa terminou"); err != nil {
				s.log.Warn("quests: a quest of a table that ended was not cancelled", "quest", q.ID, "err", err)
			}
			continue
		}
		steps, err := s.st.Steps(ctx, q.ID)
		if err != nil {
			return err
		}
		for _, st := range steps {
			switch st.State {
			case StepRunning:
				if s.verifying(st.UID) {
					continue // the time of a verification is not the time of an idle step
				}
				if why, stuck := s.idleTooLong(ctx, q, st, now); stuck {
					s.markStuck(ctx, st, why)
				}
			case StepStuck:
				if !st.Escalated && now.Sub(st.StuckAt) >= s.o.EscalateAfter {
					s.escalateStep(ctx, q, st)
				}
			}
		}
		if q.State == Cancelled {
			s.cancelStepDone(ctx, q)
		}
		s.kick(ctx, q.ID)
	}
	return s.mirrorAll(ctx)
}

// idleTooLong says whether the character of a running step has been idle, with
// no wait, for longer than the step allows without a done; or ended.
func (s *Service) idleTooLong(ctx context.Context, q Quest, st Step, now time.Time) (string, bool) {
	c, err := s.o.Characters.Get(ctx, st.Character)
	if err != nil {
		return "", false
	}
	sd := q.Def.Steps[st.Pos]
	switch {
	case c.State == characters.Stopped:
		return "o personagem " + c.Name + " foi encerrado", true
	case c.State == characters.Idle && !c.IdleSince.IsZero() && now.Sub(c.IdleSince) >= sd.StuckTime():
		return fmt.Sprintf("o personagem %s está ocioso há %s sem dar `done`", c.Name, now.Sub(c.IdleSince).Round(time.Minute)), true
	}
	return "", false
}

// escalateStep tells the Grand Master and the users of a step that stayed stuck.
func (s *Service) escalateStep(ctx context.Context, q Quest, st Step) {
	won := false
	err := s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Step(st.UID)
		if err != nil || cur.State != StepStuck || cur.Escalated {
			return err
		}
		cur.Escalated = true
		won = true
		return tx.SaveStep(cur, s.clk.Now())
	})
	if err != nil || !won {
		return
	}
	sd := q.Def.Steps[st.Pos]
	s.escalate(ctx, q, fmt.Sprintf("A etapa %s da missão %s (mesa %s) segue travada há %s e o Mestre da mesa não a resolveu.", sd.Title(), q.Name, q.Table, s.o.EscalateAfter))
}

// Loop is the loop of the engine: it makes what was asked for (the advance of a
// quest, in the background) and does the sweep every `every` until the context
// ends.
func (s *Service) Loop(ctx context.Context, every time.Duration) error {
	if every <= 0 {
		every = time.Minute
	}
	s.async.Store(true)
	defer s.async.Store(false)
	tk := s.clk.NewTicker(every)
	defer tk.Stop()
	if err := s.Sweep(ctx); err != nil { // what the engine had before it went down
		s.log.Warn("quests: sweep", "err", err)
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case id := <-s.work:
			s.process(ctx, id)
		case <-tk.C():
			if err := s.Sweep(ctx); err != nil {
				s.log.Warn("quests: sweep", "err", err)
			}
		}
	}
}
