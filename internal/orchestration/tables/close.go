package tables

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// CloseRequest is `mestre table close`.
type CloseRequest struct {
	// By is the Grand Master that asked.
	By characters.Character
	// Table is the id of the table, or the number of its epic.
	Table string
	// Cancel is `--cancel`: the table is abandoned, not merged.
	Cancel bool
	Reason string
	// Keep is the decision of the Grand Master (after asking the users) to
	// keep the worktrees that do not fulfil the rules of the end, instead of
	// waiting for them to: the table ends and the worktrees stay, with their
	// branch.
	Keep bool
}

// CloseResult is what Close answers when the table ended.
type CloseResult struct {
	Table Table
	// Retained are the worktrees kept by decision.
	Retained []string
}

var engineCreator = characters.Creator{Kind: characters.CreatedByEngine}

// Close ends a table (docs/orquestracao/mesas.md#encerramento): the
// characters with the safe end, the table master last, the rituals, and every
// worktree that is integrated and clean; then the branch leaves the mirror.
// Without the merge confirmed it is refused. What is missing comes back as a
// *MissingError and the table stays `closing` until the decision: a new call
// resumes the steps. With Cancel it is the cancellation.
func (s *Service) Close(ctx context.Context, req CloseRequest) (CloseResult, error) {
	gm, err := s.grandMaster(ctx, req.By)
	if err != nil {
		return CloseResult{}, err
	}
	req.By = gm
	t, err := s.Find(ctx, gm.Workspace, req.Table)
	if err != nil {
		return CloseResult{}, err
	}
	defer s.lock(t.Workspace, t.ID)()
	t, err = s.store.Get(ctx, t.Workspace, t.ID) // under the lock
	if err != nil {
		return CloseResult{}, err
	}
	if err := s.gate("table close"); err != nil {
		return CloseResult{}, err
	}
	if req.Cancel {
		return s.cancel(ctx, t, req)
	}
	switch t.State {
	case StateClosing:
	case StateInReview:
		if !t.Merged {
			return CloseResult{}, fmt.Errorf("%w: o PR da mesa %s ainda não foi mesclado", ErrNotMerged, t.ID)
		}
		if t, err = s.apply(ctx, t, EventClose); err != nil {
			return CloseResult{}, err
		}
	case StateOpen, StateWorking:
		return CloseResult{}, fmt.Errorf("%w: a mesa %s está em %s, sem PR mesclado", ErrNotMerged, t.ID, t.State)
	default:
		return CloseResult{}, fmt.Errorf("%w: close in %s", ErrBadTransition, t.State)
	}
	if req.Keep && !t.KeepWorktrees {
		if err := s.store.SetKeep(ctx, t.Workspace, t.ID, s.clk.Now()); err != nil {
			return CloseResult{}, err
		}
		t.KeepWorktrees = true
	}
	return s.finish(ctx, t)
}

// cancel is the cancellation of a table. It is a destructive action: it
// runs only with the exception for exactly this action or the approval of
// type `acao_destrutiva`; until then it answers that it waits.
func (s *Service) cancel(ctx context.Context, t Table, req CloseRequest) (CloseResult, error) {
	var err error
	switch t.State {
	case StateCancelling:
	case StateOpen, StateWorking, StateInReview:
		retention := s.o.Settings.CancelRetention(t.Workspace)
		if retention <= 0 {
			return CloseResult{}, fmt.Errorf("a retenção da branch de uma mesa cancelada (%s) não é válida: precisa ser positiva", retention)
		}
		if s.o.Destructive == nil {
			return CloseResult{}, ErrApprovalRequired
		}
		ok, aerr := s.o.Destructive.Allowed(ctx, DestructiveAction{Workspace: t.Workspace, Table: t.ID, Action: "table.cancel", Reason: req.Reason, By: req.By.ID})
		if aerr != nil {
			return CloseResult{}, aerr
		}
		if !ok {
			return CloseResult{}, ErrApprovalRequired
		}
		if t, err = s.apply(ctx, t, EventCancel); err != nil {
			return CloseResult{}, err
		}
		until := s.clk.Now().Add(retention)
		if err := s.store.SetCancel(ctx, t.Workspace, t.ID, s.clk.Now(), req.Reason, until); err != nil {
			return CloseResult{}, err
		}
	default:
		return CloseResult{}, fmt.Errorf("%w: cancel in %s", ErrBadTransition, t.State)
	}
	if req.Keep && !t.KeepWorktrees {
		if err := s.store.SetKeep(ctx, t.Workspace, t.ID, s.clk.Now()); err != nil {
			return CloseResult{}, err
		}
		t.KeepWorktrees = true
	}
	return s.finish(ctx, t)
}

// finish runs the steps and answers with the table or with what is missing.
func (s *Service) finish(ctx context.Context, t Table) (CloseResult, error) {
	t, miss, retained, _, err := s.advance(ctx, t)
	if err != nil {
		return CloseResult{Table: t}, err
	}
	if miss != nil {
		return CloseResult{Table: t}, miss
	}
	return CloseResult{Table: t, Retained: retained}, nil
}

// advance takes a closing or cancelling table as far as it goes: characters,
// rituals, the closing of the pull request and of the epic (cancelling), the
// worktrees, the branch. It is safe to call again.
func (s *Service) advance(ctx context.Context, t Table) (out Table, miss *MissingError, retained []string, completed bool, err error) {
	m := &MissingError{Table: t.ID}
	cancelling := t.State == StateCancelling
	if !cancelling && t.State != StateClosing {
		return t, nil, nil, false, fmt.Errorf("%w: %s", ErrBadTransition, t.State)
	}
	if err := s.endCharacters(ctx, t, m); err != nil {
		return t, nil, nil, false, err
	}
	if len(m.Pending) > 0 || len(m.Decide) > 0 {
		return t, m, nil, false, nil
	}
	if s.o.Rituals != nil {
		if err := s.o.Rituals.Deactivate(ctx, t.Workspace, t.ID); err != nil {
			return t, nil, nil, false, fmt.Errorf("the rituals of the table were not deactivated: %w", err)
		}
	}
	if cancelling && !t.CancelRemoteDone {
		if t.PRNumber > 0 {
			if err := s.o.GitHub.ClosePR(ctx, t.Workspace, t.PRNumber); err != nil {
				return t, nil, nil, false, fmt.Errorf("the pull request was not closed: %w", err)
			}
		}
		if err := s.o.GitHub.CloseEpic(ctx, t.Workspace, t.Epic); err != nil {
			return t, nil, nil, false, fmt.Errorf("the epic was not closed: %w", err)
		}
		if err := s.store.SetRemoteClosed(ctx, t.Workspace, t.ID, s.clk.Now()); err != nil {
			return t, nil, nil, false, err
		}
		t.CancelRemoteDone = true
	}
	retained, removed, err := s.endWorktrees(ctx, t, m)
	if err != nil {
		return t, nil, nil, false, err
	}
	if len(m.Decide) > 0 || len(m.Pending) > 0 {
		return t, m, nil, false, nil
	}
	if !cancelling && removed && len(retained) == 0 {
		if err := s.deleteBranch(ctx, t); err != nil {
			return t, nil, nil, false, err
		}
	}
	ev := EventClosed
	if cancelling {
		ev = EventCancelled
	}
	t, err = s.apply(ctx, t, ev)
	if err != nil {
		return t, nil, nil, false, err
	}
	return t, nil, retained, true, nil
}

// endCharacters ends the characters of the table with the safe end, the
// table master only after all the others stopped.
func (s *Service) endCharacters(ctx context.Context, t Table, m *MissingError) error {
	cs, err := s.o.Registry.AliveOf(ctx, t.Workspace)
	if err != nil {
		return err
	}
	var others, masters []characters.Character
	for _, c := range cs {
		switch {
		case c.Table != t.ID:
		case c.Role == characters.RoleTableMaster:
			masters = append(masters, c)
		default:
			others = append(others, c)
		}
	}
	end := func(c characters.Character) error {
		if c.State == characters.Stopping {
			m.Pending = append(m.Pending, c.Name+" está encerrando")
			return nil
		}
		if c.Pinned {
			m.Decide = append(m.Decide, c.Name+" está fixado: só um usuário desafixa")
			return nil
		}
		res, err := s.o.Characters.Dismiss(ctx, characters.DismissRequest{ID: c.ID, By: engineCreator, Reason: "table " + string(t.State)})
		switch {
		case errors.Is(err, characters.ErrPinned):
			m.Decide = append(m.Decide, c.Name+" está fixado: só um usuário desafixa")
			return nil
		case errors.Is(err, characters.ErrNoCharacter):
			return nil
		case err != nil:
			return fmt.Errorf("end %s: %w", c.Name, err)
		case res.Status == characters.Scheduled:
			m.Pending = append(m.Pending, c.Name+" termina no próximo ponto seguro")
			return nil
		}
		if now, err := s.o.Registry.Get(ctx, c.ID); err == nil && now.State.Alive() {
			m.Pending = append(m.Pending, c.Name+" está encerrando")
		}
		return nil
	}
	for _, c := range others {
		if err := end(c); err != nil {
			return err
		}
	}
	for _, c := range others {
		if now, err := s.o.Registry.Get(ctx, c.ID); err != nil || now.State.Alive() {
			return nil // the table master is last
		}
	}
	for _, c := range masters {
		// The table master is pinned for the life of its table; the engine lets it go.
		if err := s.o.Registry.SetPinned(ctx, c.ID, false); err != nil {
			return err
		}
		c.Pinned = false
		if err := end(c); err != nil {
			return err
		}
	}
	return nil
}

// inspect looks at the worktree of the table on a machine.
func (s *Service) inspect(ctx context.Context, wt Worktree) (Inspection, error) {
	if wt.Machine != s.o.Machine {
		if s.o.Peers == nil || !s.o.Peers.Connected(wt.Machine) {
			return Inspection{}, fmt.Errorf("%w: %s", ErrMachineUnreachable, wt.Machine)
		}
		return s.o.Peers.Inspect(ctx, wt.Machine, wt.Workspace, wt.Table)
	}
	repo, err := s.worktreeRepo(ctx, wt)
	if err != nil {
		return Inspection{}, err
	}
	tip, err := repo.ResolveRef(ctx, "HEAD")
	if err != nil {
		return Inspection{}, err
	}
	st, err := repo.Status(ctx)
	if err != nil {
		return Inspection{}, err
	}
	in := Inspection{Tip: string(tip)}
	for _, e := range st {
		in.Dirty = append(in.Dirty, e.Path)
	}
	return in, nil
}

// endWorktrees removes the worktrees that may go: with the table closing,
// the ones whose branch is integrated (the pull request merged and the tip
// there the head of the pull request or an ancestor of it) and clean; with it
// cancelling, the ones with no commit the mirror lacks and clean. The others
// are listed for the Grand Master to take to the users. removed says whether
// any worktree was removed (or none was left).
func (s *Service) endWorktrees(ctx context.Context, t Table, m *MissingError) (retained []string, removed bool, err error) {
	wts, err := s.store.Worktrees(ctx, t.Workspace, t.ID)
	if err != nil {
		return nil, false, err
	}
	mirror, err := s.o.Workspaces.OpenMirror(ctx, t.Workspace)
	if err != nil {
		return nil, false, err
	}
	removed = true
	for _, wt := range wts {
		label := wt.Machine
		if wt.Machine == s.o.Machine {
			label = "Cidadela"
		}
		if t.KeepWorktrees {
			retained = append(retained, fmt.Sprintf("%s: %s", label, wt.Dir))
			removed = false
			continue
		}
		in, err := s.inspect(ctx, wt)
		if err != nil {
			m.Decide = append(m.Decide, fmt.Sprintf("%s: o worktree não pôde ser examinado (%v)", label, err))
			removed = false
			continue
		}
		if why := s.whyKept(ctx, mirror, t, wt, in); why != "" {
			m.Decide = append(m.Decide, fmt.Sprintf("%s (%s): %s", label, wt.Dir, why))
			removed = false
			continue
		}
		if err := s.removeWorktree(ctx, mirror, t, wt); err != nil {
			if errors.Is(err, gitx.ErrWorktreeBusy) {
				// Files kept open (Windows: an editor, the antivirus) stop
				// the removal; it is tried again at the next sweep.
				m.Pending = append(m.Pending, fmt.Sprintf("%s: o worktree tem arquivos em uso; a remoção será tentada de novo", label))
				removed = false
				continue
			}
			return retained, false, err
		}
	}
	return retained, removed, nil
}

// whyKept says why a worktree may not be removed ("" if it may).
func (s *Service) whyKept(ctx context.Context, mirror gitx.Repo, t Table, wt Worktree, in Inspection) string {
	var reasons []string
	if len(in.Dirty) > 0 {
		reasons = append(reasons, "alterações não commitadas em "+strings.Join(in.Dirty, ", "))
	}
	if t.State == StateCancelling {
		// What the worktree has must be in the branch of the mirror, which
		// stays for the retention: the HEAD of the Citadel's worktree (it may
		// be detached), and what a Tower brought only if the Citadel has it
		// and the branch contains it (a diverged, conflicting or blocked
		// bundle is in refs/mestre/torres/, not in the branch).
		ok, err := mirror.IsAncestor(ctx, in.Tip, "refs/heads/"+t.Branch)
		if in.Tip == "" || err != nil || !ok {
			reasons = append(reasons, fmt.Sprintf("commits que a branch %s do espelho não tem (ponta %s)", t.Branch, gitx.Hash(in.Tip).Short()))
		}
	} else {
		head := t.MergedHead
		if head == "" {
			head = t.PRHead
		}
		integrated := t.Merged && head != ""
		if integrated && in.Tip != head {
			ok, err := mirror.IsAncestor(ctx, in.Tip, head)
			integrated = err == nil && ok
		}
		if !integrated {
			reasons = append(reasons, fmt.Sprintf("a ponta %s não está integrada ao PR mesclado", gitx.Hash(in.Tip).Short()))
		}
	}
	return strings.Join(reasons, "; ")
}

// removeWorktree removes the worktree of the table on a machine, takes the
// trust it gave out of the account profiles, and forgets it.
func (s *Service) removeWorktree(ctx context.Context, mirror gitx.Repo, t Table, wt Worktree) error {
	if wt.Machine == s.o.Machine {
		tr, err := s.tableRequest(ctx, t, gitx.WorktreeRecord{Dir: wt.Dir, GitDir: wt.GitDir, Common: wt.Common})
		if err != nil {
			// The worktree cannot be opened (it is broken, or already gone),
			// but the trust it was given must still be taken back: Release
			// needs only where it was and the profiles.
			s.log.Warn("tables: the worktree cannot be opened; its trust is released without it", "table", t.ID, "err", err)
			tr = sessionzero.TableRequest{Workspace: t.Workspace, Table: t.ID, Dir: wt.Dir, RepoRoot: wt.Common}
			if s.o.Profiles != nil {
				tr.Profiles = s.o.Profiles.Profiles(ctx, s.o.Machine)
			}
		}
		if rerr := s.o.Files.Release(tr); rerr != nil {
			s.log.Warn("tables: the trust of the worktree was not taken back", "table", t.ID, "err", rerr)
		}
		if err := mirror.RemoveWorktree(ctx, wt.Dir, false); err != nil && !errors.Is(err, gitx.ErrNoSuchWorktree) {
			return fmt.Errorf("remove the worktree of %s: %w", t.ID, err)
		}
	} else {
		if err := s.o.Peers.RemoveWorktree(ctx, wt.Machine, t.Workspace, t.ID); err != nil {
			return fmt.Errorf("remove the worktree of %s on %s: %w", t.ID, wt.Machine, err)
		}
		if t.State != StateCancelling {
			_ = mirror.DeleteRef(ctx, TowerRef(wt.Machine, t.Branch))
		} // a cancelled table keeps what the Towers sent until the branch goes
	}
	return s.store.DropWorktree(ctx, t.Workspace, t.ID, wt.Machine)
}

// deleteBranch takes the branch of the table out of the mirror.
func (s *Service) deleteBranch(ctx context.Context, t Table) error {
	mirror, err := s.o.Workspaces.OpenMirror(ctx, t.Workspace)
	if err != nil {
		return err
	}
	if err := mirror.DeleteBranch(ctx, t.Branch); err != nil && !errors.Is(err, gitx.ErrRefNotFound) {
		return fmt.Errorf("delete the branch %s: %w", t.Branch, err)
	}
	s.dropBundleRefs(ctx, mirror, t)
	return s.store.SetBranchDeleted(ctx, t.Workspace, t.ID, s.clk.Now())
}

// dropBundleRefs takes out of the mirror the refs of the bundles of a table:
// what the Towers sent and the tip meant for them.
func (s *Service) dropBundleRefs(ctx context.Context, mirror gitx.Repo, t Table) {
	_ = mirror.DeleteRef(ctx, CitadelRef(t.Branch))
	refs, err := mirror.ListRefs(ctx, TowerRefPrefix)
	if err != nil {
		s.log.Warn("tables: the refs of the Towers were not listed", "table", t.ID, "err", err)
		return
	}
	for _, r := range refs {
		rest := strings.TrimPrefix(r.Name, TowerRefPrefix)
		if _, branch, ok := strings.Cut(rest, "/"); ok && branch == t.Branch {
			if err := mirror.DeleteRef(ctx, r.Name); err != nil {
				s.log.Warn("tables: a ref of a Tower was not removed", "table", t.ID, "ref", r.Name, "err", err)
			}
		}
	}
}

// Sweep is the periodic work of the tables: it takes the tables that are
// closing or cancelling forward (a character reached its safe point), takes
// out of the mirror the branch of a table cancelled long enough ago, and
// checks the refs of the workspaces that have tables.
func (s *Service) Sweep(ctx context.Context) error {
	live, err := s.store.Live(ctx)
	if err != nil {
		return err
	}
	var errs []error
	seen := map[string]bool{}
	for _, t := range live {
		if t.State != StateClosing && t.State != StateCancelling {
			continue
		}
		if err := s.sweepEnding(ctx, t); err != nil {
			errs = append(errs, err)
		}
	}
	all, err := s.store.List(ctx, "")
	if err != nil {
		return err
	}
	now := s.clk.Now()
	for _, t := range all {
		if t.State == StateCancelled && !t.BranchDeleted && !t.CancelBranchUntil.IsZero() && !now.Before(t.CancelBranchUntil) {
			if err := s.sweepBranch(ctx, t); err != nil {
				errs = append(errs, err)
			}
		}
		if !Over(t.State) && !seen[t.Workspace] {
			seen[t.Workspace] = true
			if _, err := s.CheckRefs(ctx, t.Workspace); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (s *Service) sweepEnding(ctx context.Context, t Table) error {
	defer s.lock(t.Workspace, t.ID)()
	cur, err := s.store.Get(ctx, t.Workspace, t.ID)
	if err != nil || (cur.State != StateClosing && cur.State != StateCancelling) {
		return err
	}
	if err := s.gate("sweep"); err != nil {
		return nil // the Citadel does not act now
	}
	done, miss, _, completed, err := s.advance(ctx, cur)
	switch {
	case err != nil:
		return err
	case miss != nil:
		return nil // waits for characters, or for the decision of the Grand Master
	case completed:
		verb := "encerrada"
		if done.State == StateCancelled {
			verb = "cancelada"
		}
		if err := s.tellGrandMaster(ctx, done, inbox.KindSystem, fmt.Sprintf("Mesa %s %s.", done.ID, verb)); err != nil {
			s.log.Warn("tables: the Grand Master was not told that a table ended", "table", done.ID, "err", err)
		}
	}
	return nil
}

// sweepBranch deletes the branch of a cancelled table from the mirror, once
// the retention is over. A branch still checked out (a worktree kept by
// decision) stays.
func (s *Service) sweepBranch(ctx context.Context, t Table) error {
	defer s.lock(t.Workspace, t.ID)()
	mirror, err := s.o.Workspaces.OpenMirror(ctx, t.Workspace)
	if err != nil {
		return err
	}
	if err := mirror.DeleteBranch(ctx, t.Branch); err != nil {
		switch {
		case errors.Is(err, gitx.ErrBranchInUse):
			return nil
		case !errors.Is(err, gitx.ErrRefNotFound):
			return fmt.Errorf("delete the branch %s: %w", t.Branch, err)
		}
	}
	s.dropBundleRefs(ctx, mirror, t)
	return s.store.SetBranchDeleted(ctx, t.Workspace, t.ID, s.clk.Now())
}

// Run calls Sweep every interval until the context ends.
func (s *Service) Run(ctx context.Context, every time.Duration) error {
	if every <= 0 {
		every = time.Minute
	}
	tk := s.clk.NewTicker(every)
	defer tk.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tk.C():
			if err := s.Sweep(ctx); err != nil {
				s.log.Warn("tables: sweep", "err", err)
			}
		}
	}
}
