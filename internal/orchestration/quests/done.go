package quests

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// DoneRequest is the `mestre done` of a character that has a step.
type DoneRequest struct {
	Character characters.Character
	// Text is the summary; Return, when set, hands the work back to the step
	// the definition names, with that reason.
	Text, Return string
}

// DoneResult is what the done answers (docs/cli/comunicacao.md#mestre-done).
type DoneResult struct {
	// Task is the step, as "<quest>/<step>".
	Task  string `json:"task"`
	Quest string `json:"quest"`
	Step  string `json:"step"`
	// ReturnedTo is the step the work went back to; nil when it did not.
	ReturnedTo *string `json:"returned_to"`
	// QuestState is the state of the quest after the done.
	QuestState string   `json:"quest_state"`
	Warnings   []string `json:"warnings,omitempty"`
}

// maxKept is how many changed files a run keeps.
const maxKept = 2000

// uncommittedPrefix starts the warning of a Tower about the changes that were
// not committed (tables.Tower.Done).
const uncommittedPrefix = "alterações não commitadas não foram sincronizadas: "

// Finish registers the end of the step of a character and passes the baton
// (docs/orquestracao/missoes.md#execução): the result is the summary of the
// done and the files that changed since the commit the step started from,
// which the engine takes from git; it goes to the scroll of the quest and
// becomes the input of the next steps. A done with --return hands the work
// back, up to the limit of returns of the quest. The character with no step is
// ErrNoStep, and the caller treats its done as a task.
func (s *Service) Finish(ctx context.Context, r DoneRequest) (DoneResult, error) {
	c := r.Character
	st, ok, err := s.st.ActiveOf(ctx, c.ID)
	if err != nil {
		return DoneResult{}, err
	}
	if !ok {
		return DoneResult{}, ErrNoStep
	}
	q, err := s.st.Quest(ctx, st.Quest)
	if err != nil {
		return DoneResult{}, err
	}
	sd := q.Def.Steps[st.Pos]
	switch {
	case r.Return != "" && sd.CanReturnTo == "":
		return DoneResult{}, fmt.Errorf("%w: %s", ErrNoReturnStep, sd.Title())
	case utf8.RuneCountInString(r.Text) > MaxSummary:
		return DoneResult{}, fmt.Errorf("%w: %d characters at most", ErrTooLong, MaxSummary)
	}
	if err := s.o.Characters.Done(ctx, c.ID, r.Text); err != nil {
		return DoneResult{}, err
	}

	var warnings []string
	// A character on a Tower: its commits go to the Citadel before the result
	// is read from the branch.
	if c.Machine != s.o.Machine && s.o.Towers != nil {
		rep, err := s.o.Towers.Done(ctx, c.Machine, q.Workspace, q.Table)
		warnings = append(warnings, rep.Warnings...)
		if err != nil {
			warnings = append(warnings, "o envio dos commits para a Cidadela ficou pendente: "+err.Error())
		}
	}
	var (
		files       []string
		uncommitted []string
		end         string
	)
	run, open, err := s.openRun(ctx, st.UID)
	if err != nil {
		return DoneResult{}, err
	}
	if !open {
		return DoneResult{}, fmt.Errorf("%w: the step %s has no delivery", ErrNoStep, st.UID)
	}
	ch, err := s.o.Tables.Changes(ctx, q.Workspace, q.Table, c.Machine, run.StartCommit)
	if err != nil {
		warnings = append(warnings, "os arquivos alterados não puderam ser lidos: "+err.Error())
	} else {
		end = ch.Head
		for _, f := range ch.Files {
			if len(files) == maxKept {
				break
			}
			files = append(files, f.String())
		}
		uncommitted = ch.Uncommitted
	}
	for _, w := range warnings {
		if rest, ok := strings.CutPrefix(w, uncommittedPrefix); ok {
			uncommitted = append(uncommitted, strings.Split(rest, ", ")...)
		}
	}
	if len(uncommitted) > 0 && c.Machine == s.o.Machine {
		warnings = append(warnings, uncommittedPrefix+strings.Join(uncommitted, ", "))
	}

	out, err := s.finishRun(ctx, q, st, sd, c, r, run, end, files, uncommitted, warnings)
	if err != nil {
		return DoneResult{}, err
	}
	s.afterDone(ctx, out)
	res := DoneResult{Task: st.UID, Quest: q.Name, Step: st.ID, QuestState: string(out.questState), Warnings: warnings}
	if out.returnedTo != "" {
		res.ReturnedTo = &out.returnedTo
	}
	return res, nil
}

func (s *Service) openRun(ctx context.Context, step string) (Run, bool, error) {
	rs, err := s.st.Runs(ctx, step)
	if err != nil {
		return Run{}, false, err
	}
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].Outcome == "" {
			return rs[i], true, nil
		}
	}
	return Run{}, false, nil
}

// doneKind is what a done did to its step.
type doneKind int

const (
	doneCompleted doneKind = iota
	doneDiscarded          // the result is useless: a return, or the first of a join any
	doneReturned           // the work went back
	doneLimit              // the step ended and the limit of returns stopped the quest
	doneApproval           // the result waits for an approval after
	doneCancelled          // the quest was cancelled while the step ran
)

// outcome is what finishRun decided, for what happens after the transaction.
type outcome struct {
	kind       doneKind
	q          Quest
	st         Step
	sd         StepDef
	c          characters.Character
	run        Run
	returnedTo string
	reason     string
	questState State
	// stepDiscarded: the step is discarded, not completed.
	stepDiscarded bool
}

// finishRun applies a done to the steps in one transaction.
func (s *Service) finishRun(ctx context.Context, q Quest, st Step, sd StepDef, c characters.Character, r DoneRequest, run Run, end string, files, uncommitted, warnings []string) (outcome, error) {
	now := s.clk.Now()
	out := outcome{q: q, sd: sd, c: c}
	err := s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Quest(q.ID)
		if err != nil {
			return err
		}
		st, err := tx.Step(st.UID)
		if err != nil {
			return err
		}
		if !st.State.Active() || st.Character != c.ID {
			return ErrNoStep
		}
		steps, err := tx.Steps(q.ID)
		if err != nil {
			return err
		}
		run.EndCommit, run.EndedAt, run.Summary, run.Files, run.Uncommitted, run.Warnings = end, now, r.Text, files, uncommitted, warnings
		run.Character, run.CharacterName, run.Class = c.ID, c.Name, c.Class
		out.questState = cur.State
		switch {
		case cur.State == Cancelled:
			out.kind, run.Outcome, st.State = doneCancelled, OutcomeCompleted, StepCancelled
		case st.DiscardResult:
			out.kind, run.Outcome, st.State, st.DiscardResult = doneDiscarded, OutcomeDiscarded, StepPending, false
		case r.Return != "":
			if st.Returns < cur.MaxReturns {
				out.kind, run.Outcome, out.returnedTo, out.reason = doneReturned, OutcomeReturned, sd.CanReturnTo, r.Return
				st.Returns++
				if err := sendBack(tx, cur, steps, st, sd, r.Return, now); err != nil {
					return err
				}
				st.State = StepPending
				// the step was in steps too: the version saved below is this one
				break
			}
			// The limit is spent: the done is registered like any other, the
			// work does not go back, and the quest waits for the Master.
			out.kind, run.Outcome, st.State = doneLimit, OutcomeCompleted, StepCompleted
			out.reason = r.Return
			cur.State, cur.PausedReason = Paused, ReasonReturnLimit
			cur.Dirty++
			out.questState = Paused
			out.st = st
			if err := tx.SaveQuest(cur, now); err != nil {
				return err
			}
		case sd.Approval == ApprovalAfter:
			out.kind, run.Outcome, st.State, st.Approval = doneApproval, OutcomeCompleted, StepApproving, 0
		case discardable(cur, steps, st):
			out.kind, run.Outcome, st.State, out.stepDiscarded = doneDiscarded, OutcomeDiscarded, StepDiscarded, true
		default:
			out.kind, run.Outcome, st.State = doneCompleted, OutcomeCompleted, StepCompleted
		}
		if st.State == StepCompleted || st.State == StepDiscarded {
			st.Entry = ""
		}
		cur.Dirty++
		if err := tx.SaveQuest(cur, now); err != nil {
			return err
		}
		if err := tx.SaveStep(st, now); err != nil {
			return err
		}
		if err := tx.SaveRun(run); err != nil {
			return err
		}
		out.st, out.run, out.q = st, run, cur
		if out.kind == doneCompleted || out.kind == doneDiscarded && out.stepDiscarded {
			left, err := tx.Steps(q.ID)
			if err != nil {
				return err
			}
			if cur.State == Running && allFinished(left) {
				out.questState = Completed
			}
		}
		return nil
	})
	return out, err
}

// discardable says whether the result of a step is useless because every step
// that follows it waits only for the first of its previous steps, and has
// already started with another.
func discardable(q Quest, steps []Step, st Step) bool {
	sd := q.Def.Steps[st.Pos]
	found := false
	for _, d := range q.Def.Steps {
		if !slices.Contains(d.After, sd.ID) {
			continue
		}
		found = true
		i := slices.IndexFunc(steps, func(x Step) bool { return x.ID == d.ID })
		if !d.JoinAny() || i < 0 || steps[i].State == StepPending {
			return false
		}
	}
	return found
}

// sendBack makes the return: the step it returns to is prepared again with the
// reason, and what depends on it (the step that returns included) goes back to
// pending; a step of those that still runs ends, and its result is discarded.
func sendBack(tx *Tx, q Quest, steps []Step, from Step, sd StepDef, reason string, now time.Time) error {
	target := q.Def.Index(sd.CanReturnTo)
	affected := q.Def.Dependents(sd.CanReturnTo)
	for _, x := range steps {
		switch {
		case x.UID == from.UID:
			continue // the caller saves it
		case x.Pos == target:
			x.State, x.Entry, x.Blocker, x.ResumeNote, x.Approval = StepPreparing, reason, "", "", 0
		case slices.Contains(affected, x.ID):
			switch {
			case x.State.Active():
				x.DiscardResult = true
			case x.State == StepCompleted || x.State == StepDiscarded || x.State == StepPreparing:
				x.State, x.Entry, x.Blocker, x.Approval, x.Approved = StepPending, "", "", 0, false
			default:
				continue
			}
		default:
			continue
		}
		if err := tx.SaveStep(x, now); err != nil {
			return err
		}
	}
	return nil
}

// afterDone does what follows a done outside the transaction: the scroll, the
// waits, the approval after, the warning of the limit, and the next steps.
func (s *Service) afterDone(ctx context.Context, o outcome) {
	q, st, sd, run := o.q, o.st, o.sd, o.run
	switch o.kind {
	case doneDiscarded:
		if !o.stepDiscarded {
			break // a result made useless by a return: nothing is told
		}
		s.o.waitsStepDone(ctx, st.UID, "(descartado: outra etapa anterior já bastou)")
	case doneCancelled:
		s.cancelStepDone(ctx, q)
	default:
		s.appendScroll(ctx, q, s.stepSection(q, st, sd, run, o))
	}
	switch o.kind {
	case doneCompleted:
		s.o.waitsStepDone(ctx, st.UID, resultText(run))
	case doneLimit:
		s.o.waitsStepDone(ctx, st.UID, resultText(run))
		s.tellMaster(ctx, q, inbox.KindSystem, s.returnLimitText(q, sd, st, o.reason))
	case doneApproval:
		s.askAfter(ctx, q, st, sd, o.c, run)
	}
	s.kick(ctx, q.ID)
}

// waitsStepDone tells the waits, if there are any, that a step ended.
func (o Options) waitsStepDone(ctx context.Context, uid, result string) {
	if o.Waits != nil {
		o.Waits.StepDone(ctx, uid, result)
	}
}

// resultText is what a step passes on: the summary and the files.
func resultText(r Run) string {
	var b strings.Builder
	b.WriteString(r.Summary)
	if len(r.Files) > 0 {
		b.WriteString("\nArquivos: " + capList(r.Files, maxListed))
	}
	return b.String()
}

// returnLimitText is the event return_limit: the step, the returns made and
// the last reason.
func (s *Service) returnLimitText(q Quest, sd StepDef, st Step, reason string) string {
	return fmt.Sprintf("[return_limit] A etapa %s da missão %s chegou ao limite de voltas (%d de %d). Último motivo: %s. A missão está pausada: decida entre `mestre quest resume`, mudar o plano ou `mestre quest cancel`.",
		sd.Title(), q.Name, st.Returns, q.MaxReturns, reason)
}

// cancelStepDone ends a cancelled quest when its last step under way ended.
func (s *Service) cancelStepDone(ctx context.Context, q Quest) {
	steps, err := s.st.Steps(ctx, q.ID)
	if err != nil {
		return
	}
	if !slices.ContainsFunc(steps, func(x Step) bool { return x.State == StepRunning || x.State == StepStuck }) {
		s.finalizeCancel(ctx, q)
	}
}
