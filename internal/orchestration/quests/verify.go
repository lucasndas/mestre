package quests

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Verifier runs the verification of the workspace for the done of a class
// with `verify: true` (docs/orquestracao/prova-e-devolucao.md#o-done-que-a-classe-exige):
// the confirmed commands of the workspace, in an ephemeral worktree of the
// table at the tip of its branch in the Citadel, as the account of the agents
// and under the lock of the repository. Nil: no workspace has a command
// confirmed, so the step ends unverified.
type Verifier interface {
	// Confirmed says whether the workspace has verification commands
	// confirmed.
	Confirmed(ctx context.Context, workspace string) (bool, error)
	// Verify runs them. A command that fails is a result, not an error.
	Verify(ctx context.Context, r VerifyRequest) (VerifyResult, error)
}

// VerifyRequest is a verification to run.
type VerifyRequest struct {
	Workspace, Table, Branch string
	// Base is a commit of the main branch to integrate (a merge, never kept)
	// into the ephemeral worktree before the commands run: the gate of
	// freshness (docs/github/acoes-por-nivel.md#merge) proves what the merge
	// will be, not only the head. Empty: the commands run at the tip of
	// Branch. A base that cannot be integrated is a red result, never an
	// error and never a green one. The commands are the ones the main clone
	// of the workspace confirmed, whatever Branch changes
	// (docs/sessao-zero/workspace.md#comandos-de-verificação).
	Base string
	// Head is the commit Branch must be at (empty: the tip it has). The gate
	// proves the head of the PR; a branch that moved is a red result.
	Head string
}

// VerifyResult is what a verification gave.
type VerifyResult struct {
	OK bool
	// Command is the one that failed; Output the end of what it printed
	// (stdout and stderr).
	Command, Output string
	// TimedOut says the command ran out of time: it counts as red.
	TimedOut bool
	// Places are the `<path>:<line>` places of the output whose path exists in
	// the worktree, relative to it (the first three are the print).
	Places []string
}

// VerifyTimeout is the longest a verification may take before the engine gives
// it up (the commands have their own limits; this is the bound of the whole).
const VerifyTimeout = 30 * time.Minute

// MaxVerifyOutput is how much of the end of the output goes to the character.
const MaxVerifyOutput = 4 << 10

type doneMode int

const (
	modeNormal     doneMode = iota
	modeVerified            // the verification passed: the done ends the step
	modeUnverified          // there is no command to verify with: the step ends unverified
)

func (s *Service) verifying(uid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.verifs[uid]
}

// WaitVerifications waits for the verifications under way to be taken in.
func (s *Service) WaitVerifications() { s.vwg.Wait() }

// uncommittedOf lists what the character left not committed, after its
// commits reached the Citadel; a Tower whose commits did not arrive is an
// error, and the verification does not start.
func (s *Service) uncommittedOf(ctx context.Context, q Quest, st Step, c characters.Character) ([]string, error) {
	var warnings []string
	if c.Machine != s.o.Machine && s.o.Towers != nil {
		rep, err := s.o.Towers.Done(ctx, c.Machine, q.Workspace, q.Table)
		warnings = rep.Warnings
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrNotIntegrated, err)
		}
	}
	run, open, err := s.openRun(ctx, st.UID)
	if err != nil {
		return nil, err
	}
	if !open {
		return nil, fmt.Errorf("%w: the step %s has no delivery", ErrNoStep, st.UID)
	}
	var un []string
	ch, err := s.o.Tables.Changes(ctx, q.Workspace, q.Table, c.Machine, run.StartCommit)
	if err != nil {
		return nil, fmt.Errorf("the changes of the branch could not be read: %w", err) // not knowing is not "nothing left"
	}
	un = ch.Uncommitted
	for _, w := range warnings {
		if rest, ok := strings.CutPrefix(w, uncommittedPrefix); ok {
			un = append(un, strings.Split(rest, ", ")...)
		}
	}
	return un, nil
}

// verifyFirst is what a done of a class with verify: true does before it ends
// the step. handled says the answer is final (the verification started, or it
// was refused); otherwise the done goes on in the mode given.
func (s *Service) verifyFirst(ctx context.Context, q Quest, st Step, c characters.Character, r DoneRequest, proofs []Proof) (res DoneResult, handled bool, mode doneMode, err error) {
	forced := forcesVerify(q, st)
	if !forced {
		if s.o.Done == nil {
			return DoneResult{}, false, modeNormal, nil
		}
		need, err := s.o.Done.DoneRequires(ctx, q.Workspace, q.Table, c.Class)
		if err != nil {
			return DoneResult{}, true, modeNormal, fmt.Errorf("done_requires of the class %s: %w", c.Class, err)
		}
		if !need.Verify {
			return DoneResult{}, false, modeNormal, nil
		}
	}
	un, err := s.uncommittedOf(ctx, q, st, c)
	if err != nil {
		if errors.Is(err, ErrNotIntegrated) {
			s.tellMaster(ctx, q, inbox.KindSystem, fmt.Sprintf("O `done` de %s não pôde ser verificado: os commits dele não chegaram à Cidadela (%v).", c.Name, err))
		}
		return DoneResult{}, true, modeNormal, err
	}
	if len(un) > 0 {
		return DoneResult{}, true, modeNormal, fmt.Errorf("%w: %s", ErrUncommitted, capList(un, maxListed))
	}
	confirmed := false
	if s.o.Verifier != nil {
		if confirmed, err = s.o.Verifier.Confirmed(ctx, q.Workspace); err != nil {
			return DoneResult{}, true, modeNormal, fmt.Errorf("the verification commands of %s: %w", q.Workspace, err)
		}
	}
	if !confirmed {
		if forced {
			res, err := s.noCommand(ctx, q, st, c)
			return res, true, modeNormal, err
		}
		return DoneResult{}, false, modeUnverified, nil
	}
	if forced && s.o.BaseMoved != nil {
		// the gate before the run: a done without the base integrated is not worth a verification
		moved, err := s.o.BaseMoved(ctx, q.Workspace, q.Table)
		if err != nil {
			return DoneResult{}, true, modeNormal, fmt.Errorf("the base of the table could not be read: %w", err)
		}
		if moved {
			return DoneResult{}, true, modeNormal, ErrBaseMoved
		}
	}
	t, err := s.o.Tables.Table(ctx, q.Workspace, q.Table)
	if err != nil {
		return DoneResult{}, true, modeNormal, err
	}
	s.mu.Lock()
	s.verifs[st.UID] = true
	s.mu.Unlock()
	req := VerifyRequest{Workspace: q.Workspace, Table: q.Table, Branch: t.Branch}
	s.vwg.Add(1)
	go func() {
		defer s.vwg.Done()
		s.verified(context.WithoutCancel(ctx), q, st, c, r, req)
	}()
	return DoneResult{Task: st.UID, Quest: q.Name, Step: st.ID, QuestState: string(q.State), Warnings: []string{}, Verification: VerificationPending}, true, modeNormal, nil
}

// The values of the verification in the answer of a done.
const (
	VerificationPending    = "pending"
	VerificationUnverified = "unverified"
	// VerificationNoCommand: the step sync cannot prove its arrival without a
	// command; the quest is paused and the Master of the table was asked.
	VerificationNoCommand = "no_command"
)

// ReasonNoVerifyCommand is why a quest is paused when its step sync has no
// verification command to arrive with.
const ReasonNoVerifyCommand = "sem comando de verificação"

// SyncStepID is the id of the step of the mission `sync`
// (docs/orquestracao/sincronia-com-a-base.md): its done always goes through
// the verification of the engine, whatever the class of the character.
const SyncStepID = "sync"

// IsSyncName says whether a quest name is one of the missions of the step sync
// ("sync", "sync-2", ...).
func IsSyncName(name string) bool {
	rest, ok := strings.CutPrefix(strings.ToLower(name), SyncStepID)
	if !ok {
		return false
	}
	if rest == "" {
		return true
	}
	n, ok := strings.CutPrefix(rest, "-")
	if !ok || n == "" {
		return false
	}
	for _, r := range n {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func forcesVerify(q Quest, st Step) bool {
	return IsSyncName(q.Name) && st.ID == SyncStepID
}

// noCommand is the done of a step sync in a workspace with no command of
// verification confirmed: the quest pauses and the Master of the table takes
// the question of which commands to use to the Grand Master. The step stays
// as it is, and the done is given again after the resume.
func (s *Service) noCommand(ctx context.Context, q Quest, st Step, c characters.Character) (DoneResult, error) {
	paused, err := s.pause(ctx, q.ID, ReasonNoVerifyCommand)
	if err != nil {
		return DoneResult{}, err
	}
	cur, err := s.st.Quest(ctx, q.ID)
	if err != nil {
		return DoneResult{}, err
	}
	state := cur.State
	if paused { // told once: a done again before the resume does not ask again
		s.tellMaster(ctx, q, inbox.KindSystem, fmt.Sprintf("A etapa %s da missão %s não pode provar a chegada: o workspace não tem comando de verificação confirmado. A missão está pausada por \"%s\". Leve ao Grão-Mestre a pergunta de quais comandos usar (build e testes do projeto), confirme-os e retome com `mestre quest resume`.",
			st.ID, q.Name, ReasonNoVerifyCommand))
	}
	return DoneResult{Task: st.UID, Quest: q.Name, Step: st.ID, QuestState: string(state), Warnings: []string{}, Verification: VerificationNoCommand}, nil
}

// verified runs the verification and takes the result in: green ends the step
// as the done asked, with the proofs of the done verified by the engine; red
// leaves it running with the command and the end of the output.
func (s *Service) verified(ctx context.Context, q Quest, st Step, c characters.Character, r DoneRequest, req VerifyRequest) {
	defer func() {
		s.mu.Lock()
		delete(s.verifs, st.UID)
		s.mu.Unlock()
	}()
	failed := func(why any) {
		s.log.Warn("quests: the verification did not run", "step", st.UID, "err", why)
		if cur, err := s.st.Step(ctx, st.UID); err != nil || !cur.State.Active() || cur.Character != c.ID {
			return // the step moved on while it ran
		}
		s.tellCharacter(ctx, q, c.ID, "A verificação não pôde rodar agora (veja o log do motor). O `done` não foi concluído: tente de novo em instantes.")
		s.tellMaster(ctx, q, inbox.KindSystem, fmt.Sprintf("A verificação do `done` de %s na etapa %s não pôde rodar; a etapa segue em andamento.", c.Name, st.ID))
	}
	defer func() {
		if p := recover(); p != nil {
			failed(fmt.Sprint("panic: ", p))
		}
	}()
	vctx, cancel := clock.WithTimeout(ctx, s.clk, VerifyTimeout)
	defer cancel()
	res, err := s.o.Verifier.Verify(vctx, req)
	if err != nil {
		failed(err)
		return
	}
	cur, err := s.st.Step(ctx, st.UID)
	if err != nil || !cur.State.Active() || cur.Character != c.ID {
		return // the step moved on while it ran
	}
	if res.OK {
		if forcesVerify(q, cur) && s.o.BaseMoved != nil {
			// the gate again, before the arrival: the base may have moved while it ran
			moved, err := s.o.BaseMoved(ctx, q.Workspace, q.Table)
			if err != nil {
				failed(err)
				return
			}
			if moved {
				s.tellCharacter(ctx, q, c.ID, "A verificação passou, mas a branch principal andou de novo enquanto ela rodava. A etapa segue em andamento: integre a base outra vez (`git merge`), rode a verificação, commite e dê `mestre done` de novo.")
				return
			}
		}
		r.Where = nil
		r.expectStep = st.UID
		if _, err := s.finish(ctx, r, modeVerified); err != nil {
			s.log.Warn("quests: the done that was verified was not registered", "step", st.UID, "err", err)
		}
		return
	}
	s.red(ctx, q, cur, c, res)
}

// red takes a verification that failed: a volta of the step, counted like a
// return, with the same failure again pausing the quest before the limit.
func (s *Service) red(ctx context.Context, q Quest, st Step, c characters.Character, res VerifyResult) {
	sd := q.Def.Steps[st.Pos]
	reason := "tempo esgotado"
	if !res.TimedOut {
		reason = "a verificação falhou"
	}
	var (
		kind  doneKind
		fail  Failure
		pause string
	)
	err := s.st.Do(ctx, func(tx *Tx) error {
		cur, err := tx.Quest(q.ID)
		if err != nil {
			return err
		}
		fresh, err := tx.Step(st.UID)
		if err != nil {
			return err
		}
		if cur.State != Running || !fresh.State.Active() || fresh.Character != c.ID {
			kind = doneCancelled
			return nil
		}
		fail, err = s.countFailureOf(tx, fresh, sd, "verify", res.Places)
		if err != nil {
			return err
		}
		switch {
		case fresh.Returns < cur.MaxReturns && fail.Fingerprint != "" && fail.Count >= cur.Def.SameMax():
			kind, pause = doneSameFailure, ReasonSameFailure
			if err := tx.SetFailure(fresh.UID, Failure{}); err != nil {
				return err
			}
		case fresh.Returns >= cur.MaxReturns:
			kind, pause = doneLimit, ReasonReturnLimit
		default:
			fresh.Returns++
			if err := tx.SaveStep(fresh, s.clk.Now()); err != nil {
				return err
			}
			st = fresh
			return nil
		}
		st = fresh
		cur.State, cur.PausedReason = Paused, pause
		cur.Dirty++
		return tx.SaveQuest(cur, s.clk.Now())
	})
	if err != nil {
		s.log.Warn("quests: a red verification was not counted", "step", st.UID, "err", err)
		return
	}
	if kind == doneCancelled {
		return
	}
	s.tellCharacter(ctx, q, c.ID, s.redText(res, reason))
	o := outcome{q: q, st: st, sd: sd, reason: reason + ": " + res.Command, fail: fail}
	switch kind {
	case doneSameFailure:
		s.tellMaster(ctx, q, inbox.KindSystem, s.sameFailureText(q, sd, o))
	case doneLimit:
		s.tellMaster(ctx, q, inbox.KindSystem, s.returnLimitText(q, sd, st, o.reason))
	}
}

// redText is the item the character gets when its verification failed: the
// command and the end of the output, which are text of the project, not of the
// engine.
func (s *Service) redText(res VerifyResult, reason string) string {
	out := res.Output
	if len(out) > MaxVerifyOutput {
		cut := len(out) - MaxVerifyOutput
		for cut < len(out) && !utf8.RuneStart(out[cut]) {
			cut++
		}
		out = "…" + out[cut:]
	}
	head := fmt.Sprintf("A verificação do workspace falhou (%s) no comando %s. A etapa segue em andamento: corrija, commite e rode `mestre done` de novo.", reason, inbox.OneLine(res.Command))
	return inbox.Say(head+" Fim da saída (texto do projeto, não verificado):", out, "|")
}

// tellCharacter puts a system item in the inbox of a character.
func (s *Service) tellCharacter(ctx context.Context, q Quest, id, text string) {
	if _, err := s.o.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: q.Workspace, Target: id, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: text,
		Refs: inbox.Refs{Quest: q.ID, Table: q.Table}}); err != nil {
		s.log.Warn("quests: a character was not told", "quest", q.ID, "err", err)
	}
}
