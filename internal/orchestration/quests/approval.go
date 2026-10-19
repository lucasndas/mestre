package quests

import (
	"context"
	"fmt"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// point is the request of a point of approval of a step.
func (s *Service) point(q Quest, st Step, sd StepDef, when, summary string) MissionPoint {
	return MissionPoint{Workspace: q.Workspace, Table: q.Table, Quest: q.ID, QuestName: q.Name, Step: st.UID, StepID: sd.ID, StepName: sd.Title(),
		When: when, Character: st.Character, Designer: q.Designer, Summary: summary}
}

// askBefore opens the approval that comes before a step starts: the step waits,
// in preparing, until it is decided (docs/orquestracao/missoes.md#pontos-de-aprovação).
// Without the flow of approval (milestone 5) the quest pauses at the point.
func (s *Service) askBefore(ctx context.Context, q Quest, st Step, sd StepDef) {
	if st.Approval != 0 {
		return // asked: Decided goes on
	}
	if s.o.Approvals == nil {
		s.pauseAndTell(ctx, q, fmt.Sprintf("a etapa %s pede aprovação antes de começar e o fluxo de aprovação não está ligado", sd.Title()))
		return
	}
	req, err := s.o.Approvals.OpenMissionPoint(ctx, s.point(q, st, sd, ApprovalBefore, sd.Instructions))
	if err != nil {
		s.pauseAndTell(ctx, q, fmt.Sprintf("o pedido de aprovação da etapa %s não foi aberto: %v", sd.Title(), err))
		return
	}
	err = s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Step(st.UID)
		if err != nil || cur.State != StepPreparing {
			return err
		}
		cur.Approval, cur.Blocker = req, fmt.Sprintf("%s%d", blockApproval, req)
		return tx.SaveStep(cur, s.clk.Now())
	})
	if err != nil {
		s.log.Warn("quests: the request of approval of a step was not recorded", "step", st.UID, "request", req, "err", err)
	}
}

// askAfter opens the approval that comes before a finished step passes the
// baton, and puts its character in the wait for it.
func (s *Service) askAfter(ctx context.Context, q Quest, st Step, sd StepDef, c characters.Character, run Run) {
	if st.Approval != 0 {
		return
	}
	if s.o.Approvals == nil {
		s.pauseAndTell(ctx, q, fmt.Sprintf("a etapa %s pede aprovação antes de passar adiante e o fluxo de aprovação não está ligado", sd.Title()))
		return
	}
	st.Character = c.ID
	req, err := s.o.Approvals.OpenMissionPoint(ctx, s.point(q, st, sd, ApprovalAfter, resultText(run)))
	if err != nil {
		s.pauseAndTell(ctx, q, fmt.Sprintf("o pedido de aprovação da etapa %s não foi aberto: %v", sd.Title(), err))
		return
	}
	err = s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Step(st.UID)
		if err != nil || cur.State != StepApproving {
			return err
		}
		cur.Approval = req
		return tx.SaveStep(cur, s.clk.Now())
	})
	if err != nil {
		s.log.Warn("quests: the request of approval of a step was not recorded", "step", st.UID, "request", req, "err", err)
	}
	if s.o.Waits != nil {
		if _, err := s.o.Waits.WaitApproval(ctx, c, req, 0); err != nil {
			s.log.Warn("quests: the wait for the approval was not made", "step", st.UID, "request", req, "err", err)
		}
	}
}

// Decision is how the flow of approval answers the request of a point of a
// quest.
type Decision struct {
	Request  int64
	Approved bool
	// By is the character that decided ("" for a person): the designer of the
	// quest never does.
	By     string
	Reason string
}

// Decided is told by the flow of approval (milestone 5) when a point of a
// quest is decided, at any level of the chain. The designer of the quest, the
// Master of the table that made it, never decides one: that decision is
// refused and the request goes on up the chain.
//
//   - before, approved: the step goes on to get its character;
//   - before, denied: the quest pauses and the Master of the table is told;
//   - after, approved: the step is done and the baton goes on;
//   - after, denied: the step goes back to preparing with the reason, and that
//     counts as a return; over the limit, the quest pauses.
func (s *Service) Decided(ctx context.Context, d Decision) error {
	st, ok, err := s.st.ByRequest(ctx, d.Request)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: #%d", ErrNoRequest, d.Request)
	}
	q, err := s.st.Quest(ctx, st.Quest)
	if err != nil {
		return err
	}
	if d.By != "" && d.By == q.Designer {
		return fmt.Errorf("%w: %s", ErrSelfApproval, q.Name)
	}
	sd := q.Def.Steps[st.Pos]
	text := fmt.Sprintf("pedido #%d da etapa %s %s", d.Request, sd.Title(), map[bool]string{true: "aprovado", false: "negado"}[d.Approved])
	if d.Reason != "" {
		// the reason is the words of who decided: more than one line comes quoted
		text += "\n" + inbox.Say("Motivo:", d.Reason, "|")
		if inbox.Multiline(d.Reason) {
			text += "\n" + inbox.QuoteNote
		}
	}
	if s.o.Waits != nil {
		s.o.Waits.ApprovalDecided(ctx, d.Request, text)
	}
	now := s.clk.Now()
	switch {
	case st.State == StepPreparing && d.Approved:
		err = s.updateStep(ctx, st.UID, func(x *Step) { x.Approved, x.Approval, x.Blocker = true, 0, "" })
		s.kick(ctx, q.ID)
	case st.State == StepPreparing:
		err = s.updateStep(ctx, st.UID, func(x *Step) { x.Approval, x.Blocker = 0, "" })
		s.pauseAndTell(ctx, q, fmt.Sprintf("a aprovação antes da etapa %s foi negada%s", sd.Title(), reasonSuffix(d.Reason)))
	case st.State == StepApproving && d.Approved:
		err = s.st.Do(ctx, func(tx *Tx) error {
			cur, err := tx.Step(st.UID)
			if err != nil || cur.State != StepApproving {
				return err
			}
			cur.State, cur.Approval, cur.Entry = StepCompleted, 0, ""
			return tx.SaveStep(cur, now)
		})
		if err == nil {
			run, _ := s.lastRun(ctx, st.UID)
			s.o.waitsStepDone(ctx, st.UID, resultText(run))
			s.kick(ctx, q.ID)
		}
	case st.State == StepApproving:
		err = s.deniedAfter(ctx, q, st, sd, d)
	default:
		return fmt.Errorf("%w: #%d", ErrNoRequest, d.Request)
	}
	return err
}

func reasonSuffix(reason string) string {
	if reason == "" {
		return ""
	}
	return ": " + reason
}

// deniedAfter sends a step whose approval after was refused back to preparing;
// the denial is a return of the step.
func (s *Service) deniedAfter(ctx context.Context, q Quest, st Step, sd StepDef, d Decision) error {
	var over bool
	err := s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Step(st.UID)
		if err != nil || cur.State != StepApproving {
			return err
		}
		cur.Returns++
		over = cur.Returns > q.MaxReturns
		cur.State, cur.Approval, cur.Entry = StepPreparing, 0, d.Reason
		if cur.Entry == "" {
			cur.Entry = "a aprovação foi negada"
		}
		st = cur
		return tx.SaveStep(cur, s.clk.Now())
	})
	if err != nil {
		return err
	}
	if over {
		s.pauseAndTell(ctx, q, ReasonReturnLimit)
		s.tellMaster(ctx, q, inbox.KindSystem, s.returnLimitText(q, sd, st, st.Entry))
		return nil
	}
	s.kick(ctx, q.ID)
	return nil
}

func (s *Service) lastRun(ctx context.Context, step string) (Run, bool) {
	rs, err := s.st.Runs(ctx, step)
	if err != nil || len(rs) == 0 {
		return Run{}, false
	}
	return rs[len(rs)-1], true
}

// updateStep changes the fields of a step that are not decided by a state
// machine of their own, in one transaction.
func (s *Service) updateStep(ctx context.Context, uid string, fn func(*Step)) error {
	return s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Step(uid)
		if err != nil {
			return err
		}
		fn(&cur)
		return tx.SaveStep(cur, s.clk.Now())
	})
}

// updateQuest is updateStep for a quest.
func (s *Service) updateQuest(ctx context.Context, id string, fn func(*Quest)) error {
	return s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Quest(id)
		if err != nil {
			return err
		}
		fn(&cur)
		return tx.SaveQuest(cur, s.clk.Now())
	})
}
