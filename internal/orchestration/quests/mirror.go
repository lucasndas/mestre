package quests

import (
	"context"
	"fmt"
	"strings"
)

// mirrorAll goes over the quests whose mirror on GitHub has something to do.
func (s *Service) mirrorAll(ctx context.Context) error {
	if s.o.GitHub == nil {
		return nil
	}
	qs, err := s.st.WithGitHub(ctx)
	if err != nil {
		return err
	}
	for _, q := range qs {
		s.mirror(ctx, q.ID)
	}
	return nil
}

// mirror brings GitHub to what the quest is (docs/orquestracao/missoes.md#espelho-no-github):
// the issue under the epic with the steps as a checklist, the sub-issues of the
// steps that ask for one, a comment with the summary of each step that ended
// (written as the App of the class of its character) and the sub-issue closed
// as completed, a comment per round, and the issue and the open sub-issues
// closed as not planned when the quest is cancelled. Every thing done is
// recorded, so doing it again does only what is left; what fails stays for the
// next time, and the quest does not wait for GitHub.
func (s *Service) mirror(ctx context.Context, id string) {
	gh := s.o.GitHub
	if gh == nil {
		return
	}
	q, err := s.st.Quest(ctx, id)
	if err != nil || q.Dirty == 0 {
		return
	}
	if s.o.Gate != nil {
		if err := s.o.Gate(); err != nil {
			return // this Citadel may not act on GitHub now
		}
	}
	steps, err := s.st.Steps(ctx, id)
	if err != nil {
		return
	}
	ok := true
	fail := func(what string, err error) {
		ok = false
		s.log.Warn("quests: GitHub did not take "+what+", it is tried again", "quest", id, "err", err)
	}

	if q.Issue == 0 {
		t, err := s.o.Tables.Table(ctx, q.Workspace, q.Table)
		if err != nil {
			return
		}
		n, err := gh.CreateIssue(ctx, q.Workspace, t.Epic, q.Name, s.issueBody(q, steps))
		if err != nil {
			fail("the issue", err)
			return
		}
		q.Issue = n
		if err := s.updateQuest(ctx, id, func(x *Quest) { x.Issue = n }); err != nil {
			return
		}
	}
	for i, st := range steps {
		sd := q.Def.Steps[st.Pos]
		if !sd.SubIssue || st.SubIssue != 0 {
			continue
		}
		n, err := gh.CreateSubIssue(ctx, q.Workspace, q.Issue, fmt.Sprintf("%s — %s", q.Name, sd.Title()), subIssueBody(q, sd))
		if err != nil {
			fail("a sub-issue", err)
			continue
		}
		steps[i].SubIssue = n
		_ = s.updateStep(ctx, st.UID, func(x *Step) { x.SubIssue = n })
	}
	if q.Round > q.RoundCommented {
		if q.Round > 1 {
			body := fmt.Sprintf("Rodada %d da missão iniciada.", q.Round)
			if q.Input != "" {
				body += "\n\nEntrada: " + q.Input
			}
			if err := gh.Comment(ctx, q.Workspace, q.Issue, "", body); err != nil {
				fail("the comment of the round", err)
			}
		}
		if ok {
			round := q.Round
			_ = s.updateQuest(ctx, id, func(x *Quest) { x.RoundCommented = round })
		}
	}
	runs, _ := s.st.QuestRuns(ctx, id, q.Round)
	for _, st := range steps {
		if st.State != StepCompleted || st.CommentedRound >= q.Round {
			continue
		}
		var last Run
		for _, r := range runs {
			if r.Step == st.UID && r.Outcome == OutcomeCompleted {
				last = r
			}
		}
		target := q.Issue
		if st.SubIssue != 0 {
			target = st.SubIssue
		}
		sd := q.Def.Steps[st.Pos]
		body := fmt.Sprintf("**%s** (%s) terminou a etapa %s na rodada %d.\n\n%s", orNone(last.CharacterName), orNone(last.Class), sd.Title(), q.Round, resultText(last))
		if err := gh.Comment(ctx, q.Workspace, target, last.Class, body); err != nil {
			fail("the comment of a step", err)
			continue
		}
		closed := st.SubClosed
		if st.SubIssue != 0 && !closed {
			if err := gh.Close(ctx, q.Workspace, st.SubIssue, ClosedCompleted); err != nil {
				fail("the closing of a sub-issue", err)
			} else {
				closed = true
			}
		}
		round := q.Round
		_ = s.updateStep(ctx, st.UID, func(x *Step) { x.CommentedRound, x.SubClosed = round, closed })
	}
	if q.State == Cancelled && !q.IssueClosed {
		done := true
		for _, st := range steps {
			if st.SubIssue != 0 && !st.SubClosed {
				if err := gh.Close(ctx, q.Workspace, st.SubIssue, ClosedNotPlanned); err != nil {
					fail("the closing of a sub-issue", err)
					done = false
					continue
				}
				_ = s.updateStep(ctx, st.UID, func(x *Step) { x.SubClosed = true })
			}
		}
		if done {
			if err := gh.Close(ctx, q.Workspace, q.Issue, ClosedNotPlanned); err != nil {
				fail("the closing of the issue", err)
			} else {
				_ = s.updateQuest(ctx, id, func(x *Quest) { x.IssueClosed = true })
			}
		}
	}
	// The checklist, from the steps as they are now.
	if fresh, err := s.st.Steps(ctx, id); err == nil {
		if err := s.syncBody(ctx, q, fresh); err != nil {
			fail("the checklist", err)
		}
	}
	if ok {
		_ = s.st.Do(ctx, func(tx *Tx) error {
			cur, err := tx.Quest(id)
			if err != nil || cur.Dirty != q.Dirty {
				return err // something changed meanwhile: it goes over again
			}
			cur.Dirty = 0
			return tx.SaveQuest(cur, cur.UpdatedAt)
		})
	}
}

// syncBody brings the body of the issue to what the quest is. An issue the
// engine made is written whole. One the Master of the table made keeps its
// text: the checklist of the steps goes between its markers, and the
// criteria with a proof are checked, with the line of the proof.
func (s *Service) syncBody(ctx context.Context, q Quest, steps []Step) error {
	gh := s.o.GitHub
	if q.Def.Issue == 0 {
		return gh.UpdateBody(ctx, q.Workspace, q.Issue, s.issueBody(q, steps))
	}
	cur, err := gh.IssueBody(ctx, q.Workspace, q.Issue)
	if err != nil {
		return err
	}
	crit, err := s.st.Criteria(ctx, q.ID)
	if err != nil {
		return err
	}
	proofs, err := s.st.Proofs(ctx, q.ID, q.Round)
	if err != nil {
		return err
	}
	body := mergeSteps(cur, s.stepsChecklist(q, steps))
	body, checked := markCriteria(body, crit, proofs)
	if body != cur {
		if err := gh.UpdateBody(ctx, q.Workspace, q.Issue, body); err != nil {
			return err
		}
	}
	for _, c := range crit {
		if c.Marked != checked[c.ID] {
			if err := s.st.SetMarked(ctx, q.ID, c.ID, checked[c.ID]); err != nil {
				return err
			}
		}
	}
	return nil
}

// issueBody is the body of the issue of the quest: the steps as a checklist.
func (s *Service) issueBody(q Quest, steps []Step) string {
	return fmt.Sprintf("Missão **%s** da mesa `%s`, conduzida pelo Mestre.\n\n", q.Name, q.Table) + s.stepsChecklist(q, steps)
}

// stepsChecklist is the section `## Etapas`: the steps as a checklist.
func (s *Service) stepsChecklist(q Quest, steps []Step) string {
	var b strings.Builder
	b.WriteString("## Etapas\n")
	for i, sd := range q.Def.Steps {
		mark := " "
		sub := ""
		if i < len(steps) {
			if steps[i].State == StepCompleted || steps[i].State == StepDiscarded {
				mark = "x"
			}
			if steps[i].SubIssue != 0 {
				sub = fmt.Sprintf(" (#%d)", steps[i].SubIssue)
			}
		}
		fmt.Fprintf(&b, "- [%s] %d. %s%s\n", mark, i+1, sd.Title(), sub)
	}
	return b.String()
}

func subIssueBody(q Quest, sd StepDef) string {
	body := fmt.Sprintf("Etapa **%s** da missão **%s**.", sd.Title(), q.Name)
	if sd.Instructions != "" {
		body += "\n\n" + sd.Instructions
	}
	return body
}
