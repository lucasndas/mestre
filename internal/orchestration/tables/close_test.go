package tables_test

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// reviewed opens the table of the epic and takes it to a merged pull request
// whose head is the commit made in the worktree.
func reviewed(t *testing.T, w *world, epic int, title string) (tables.Table, gitx.Hash) {
	t.Helper()
	w.gh.add(epic, title, "epico")
	tbl := w.open(epic).Table
	head := commit(t, w.be, w.git, w.repoOf(tbl), "feature.go", "package feature\n", "feat: feature")
	if err := w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: tbl.ID}, "done"); err != nil {
		t.Fatal(err)
	}
	return w.merged(tbl, head), head
}

// CA-MESAS-06: the fake git reports an uncommitted file in the worktree of the
// table and the merge is confirmed. The Grand Master closes the table: the
// worktree is not removed, the table stays closing and the Grand Master gets
// what is missing.
func TestAWorktreeWithAnUncommittedFileIsNotRemoved(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	tbl, _ := reviewed(t, w, 42, "Login")
	w.be.write(t, w.git, w.repoOf(tbl).Dir(), "rascunho.txt", "meu rascunho\n")

	res, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID})
	var miss *tables.MissingError
	if !errors.As(err, &miss) || len(miss.Decide) != 1 || !strings.Contains(miss.Decide[0], "rascunho.txt") {
		t.Fatalf("Close = %+v, %v; want what is missing, with the file", res, err)
	}
	if !errors.Is(err, tables.ErrUnmergedChanges) {
		t.Errorf("the error is not unmerged_changes: %v", err)
	}
	got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID)
	if got.State != tables.StateClosing {
		t.Errorf("state = %s; want closing", got.State)
	}
	if removed, _ := w.spy.asked(); len(removed) != 0 {
		t.Errorf("the engine asked git to remove worktrees: %v", removed)
	}
	wts, _ := w.mirror().ListWorktrees(w.ctx)
	if !slices.ContainsFunc(wts, func(x gitx.Worktree) bool { return strings.HasSuffix(x.Path, tbl.ID) }) {
		t.Errorf("the worktree was removed: %+v", wts)
	}
	if rel := w.files.list(&w.files.released); len(rel) != 0 {
		t.Errorf("the trust of the worktree was taken back: %v", rel)
	}
	if _, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); err != nil {
		t.Errorf("the branch was deleted: %v", err)
	}
	// The users decide to keep it: the table ends, the worktree and the branch stay.
	res, err = w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Keep: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Table.State != tables.StateClosed || len(res.Retained) != 1 {
		t.Errorf("after keep: %+v", res)
	}
	if _, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); err != nil {
		t.Errorf("the branch of a retained worktree was deleted: %v", err)
	}
}

// A worktree whose branch has a commit the pull request lacks is not
// integrated: it stays too.
func TestACommitThatIsNotInThePullRequestKeepsTheWorktree(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	tbl, _ := reviewed(t, w, 42, "Login")
	commit(t, w.be, w.git, w.repoOf(tbl), "depois.go", "package depois\n", "feat: depois do merge")
	_, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID})
	var miss *tables.MissingError
	if !errors.As(err, &miss) || len(miss.Decide) != 1 || !strings.Contains(miss.Decide[0], "não está integrada") {
		t.Fatalf("Close = %v; want the commit that is not integrated", err)
	}
}

// The trust of the folder is taken back even when the worktree can no longer
// be opened by the time it is removed.
func TestTheTrustIsReleasedEvenWhenTheWorktreeCannotBeOpened(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	tbl, _ := reviewed(t, w, 42, "Login")
	dir := w.repoOf(tbl).Dir()
	w.spy.mu.Lock()
	w.spy.failOpen = w.spy.opens + 2 // the inspection opens it, the request to release does not
	w.spy.mu.Unlock()
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID}); err != nil {
		t.Fatal(err)
	}
	if rel := w.files.list(&w.files.released); !slices.Equal(rel, []string{dir}) {
		t.Errorf("released = %v; want %s", rel, dir)
	}
}

// A clean, integrated worktree goes with its branch; the characters end first
// with the safe end, the table master last, the rituals are deactivated and the
// trust of the folder is taken back.
func TestClosingEndsTheCharactersTheRitualsTheWorktreeAndTheBranch(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	tbl, _ := reviewed(t, w, 42, "Login")
	for _, name := range []string{"Dev 1", "Dev 2"} {
		if _, err := w.dev(tbl, name); err != nil {
			t.Fatal(err)
		}
	}
	master, _, _ := w.reg.TableMaster(w.ctx, w.slug(), tbl.ID)
	dir := w.repoOf(tbl).Dir()

	res, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID})
	if err != nil {
		t.Fatal(err)
	}
	if res.Table.State != tables.StateClosed || res.Table.ClosedAt.IsZero() {
		t.Fatalf("table = %+v", res.Table)
	}
	d := w.chars.dismissed()
	if len(d) != 3 || d[2] != master.ID {
		t.Errorf("dismissed %v; want the table master last (%s)", d, master.ID)
	}
	for _, req := range w.chars.dismisses {
		if req.By.Kind != characters.CreatedByEngine {
			t.Errorf("dismissed by %+v", req.By)
		}
	}
	if !slices.Equal(w.rituals.deactivated, []string{tbl.ID}) {
		t.Errorf("rituals deactivated = %v", w.rituals.deactivated)
	}
	if rel := w.files.list(&w.files.released); !slices.Equal(rel, []string{dir}) {
		t.Errorf("released = %v; want %s", rel, dir)
	}
	wts, _ := w.mirror().ListWorktrees(w.ctx)
	if slices.ContainsFunc(wts, func(x gitx.Worktree) bool { return x.Path == dir }) {
		t.Errorf("the worktree is still there: %+v", wts)
	}
	if _, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); !errors.Is(err, gitx.ErrRefNotFound) {
		t.Errorf("the branch is still in the mirror: %v", err)
	}
	if _, err := w.svc.Store().Worktree(w.ctx, w.slug(), tbl.ID, w.machine); !errors.Is(err, tables.ErrNoWorktree) {
		t.Errorf("the record of the worktree is still there: %v", err)
	}
	// A closed table never reopens and takes no new character.
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID}); !errors.Is(err, tables.ErrBadTransition) {
		t.Errorf("closing a closed table = %v", err)
	}
	if _, err := w.svc.PROpened(w.ctx, w.slug(), tbl.ID, 1, "x"); !errors.Is(err, tables.ErrBadTransition) {
		t.Errorf("an event in a closed table = %v", err)
	}
	if info, _ := w.svc.Table(w.ctx, w.slug(), tbl.ID); info.State.Accepts() {
		t.Errorf("a closed table takes characters: %+v", info)
	}
}

// A character that is not at a safe point ends when it gets there: the table
// stays closing, tells the Grand Master what it waits for, and the sweep of the
// engine finishes it, telling the Grand Master.
func TestAClosingTableWaitsForTheSafePointAndTheSweepFinishesIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	tbl, _ := reviewed(t, w, 42, "Login")
	dev, err := w.dev(tbl, "Dev")
	if err != nil {
		t.Fatal(err)
	}
	w.chars.schedule[dev.Character.ID] = true

	_, err = w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID})
	var miss *tables.MissingError
	if !errors.As(err, &miss) || len(miss.Decide) != 0 || len(miss.Pending) != 1 || !strings.Contains(miss.Pending[0], "Dev") {
		t.Fatalf("Close = %v; want it to wait for Dev", err)
	}
	if got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); got.State != tables.StateClosing {
		t.Fatalf("state = %s", got.State)
	}
	if m, _, _ := w.reg.TableMaster(w.ctx, w.slug(), tbl.ID); m.State == characters.Stopped {
		t.Fatal("the table master ended before the characters")
	}
	if err := w.svc.Sweep(w.ctx); err != nil { // nothing changed yet
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); got.State != tables.StateClosing {
		t.Fatalf("state after a useless sweep = %s", got.State)
	}
	// Dev reaches its safe point and ends.
	if err := w.chars.finish(w.ctx, dev.Character.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID)
	if got.State != tables.StateClosed {
		t.Fatalf("state after the sweep = %s; want closed", got.State)
	}
	items, _ := w.inb.Store().All(w.ctx, w.gm.ID)
	if n := len(items); n < 2 || !strings.Contains(items[n-1].Text, "encerrada") || items[n-1].Kind != inbox.KindSystem {
		t.Errorf("inbox of the Grand Master = %+v", items)
	}
}

// A pinned character keeps the table from ending until a user decides.
func TestAPinnedCharacterKeepsTheTableClosing(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	tbl, _ := reviewed(t, w, 42, "Login")
	dev, _ := w.dev(tbl, "Revisor")
	if err := w.reg.SetPinned(w.ctx, dev.Character.ID, true); err != nil {
		t.Fatal(err)
	}
	_, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID})
	var miss *tables.MissingError
	if !errors.As(err, &miss) || len(miss.Decide) != 1 || !strings.Contains(miss.Decide[0], "Revisor está fixado") {
		t.Fatalf("Close = %v; want the pinned character listed", err)
	}
	if err := w.reg.SetPinned(w.ctx, dev.Character.ID, false); err != nil {
		t.Fatal(err)
	}
	if res, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID}); err != nil || res.Table.State != tables.StateClosed {
		t.Fatalf("Close after the decision = %+v, %v", res, err)
	}
}

// CA-MESAS-05: a table cancelled with the fake git and the fake clock: the
// engine asks git to take the branch out of the mirror after 30 days, and
// not before.
func TestTheBranchOfACancelledTableLeavesTheMirrorAfterThirtyDays(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	w.dest.allowed = true
	res, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "o usuário desistiu"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Table.State != tables.StateCancelled || res.Table.CancelBranchUntil != day0.Add(30*24*time.Hour) {
		t.Fatalf("table = %+v", res.Table)
	}
	has := func() bool {
		_, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch)
		return err == nil
	}
	asked := func() bool {
		_, deleted := w.spy.asked()
		return slices.Contains(deleted, tbl.Branch)
	}
	for _, d := range []time.Duration{0, 24 * time.Hour, 29*24*time.Hour + 23*time.Hour + 59*time.Minute} {
		w.clk.Set(day0.Add(d))
		if err := w.svc.Sweep(w.ctx); err != nil {
			t.Fatal(err)
		}
		if !has() || asked() {
			t.Fatalf("the branch left the mirror, or git was asked to delete it, %v after the cancellation", d)
		}
	}
	w.clk.Set(day0.Add(30 * 24 * time.Hour))
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if has() || !asked() {
		t.Fatalf("after 30 days: the branch is in the mirror %v, git was asked to delete it %v", has(), asked())
	}
	if got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); !got.BranchDeleted {
		t.Errorf("the table does not say the branch is gone: %+v", got)
	}
}

// The cancellation is destructive: without the exception or the approval it
// waits and nothing happens; once allowed, the characters end, the PR and the
// epic are closed on GitHub, the worktree is removed when clean and the
// branch stays.
func TestCancellingATableNeedsTheApprovalOfADestructiveAction(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	if _, err := w.svc.PROpened(w.ctx, w.slug(), tbl.ID, 9, "abc"); err != nil {
		t.Fatal(err)
	}
	req := tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "mudou de ideia"}
	if _, err := w.svc.Close(w.ctx, req); !errors.Is(err, tables.ErrApprovalRequired) {
		t.Fatalf("cancel without the approval = %v", err)
	}
	if len(w.dest.asked) != 1 || w.dest.asked[0].Action != "table.cancel" || w.dest.asked[0].Table != tbl.ID || w.dest.asked[0].Reason != "mudou de ideia" {
		t.Errorf("asked = %+v", w.dest.asked)
	}
	if got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); got.State != tables.StateInReview {
		t.Fatalf("state = %s; nothing may change before the approval", got.State)
	}
	if len(w.chars.dismissed()) != 0 || len(w.gh.closedEp) != 0 {
		t.Fatalf("something ran before the approval: %v %v", w.chars.dismissed(), w.gh.closedEp)
	}
	w.dest.allowed = true
	res, err := w.svc.Close(w.ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if res.Table.State != tables.StateCancelled || res.Table.CancelReason != "mudou de ideia" {
		t.Errorf("table = %+v", res.Table)
	}
	if !slices.Equal(w.gh.closedPR, []int{9}) || !slices.Equal(w.gh.closedEp, []int{42}) {
		t.Errorf("closed on GitHub: PR %v, epic %v", w.gh.closedPR, w.gh.closedEp)
	}
	if len(w.chars.dismissed()) != 1 {
		t.Errorf("dismissed %v; want the table master", w.chars.dismissed())
	}
	wts, _ := w.mirror().ListWorktrees(w.ctx)
	if len(wts) != 2 {
		t.Errorf("worktrees = %+v; want the clean worktree of the table removed", wts)
	}
	if _, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); err != nil {
		t.Errorf("the branch left the mirror at once: %v", err)
	}
	if err := w.svc.Pause(w.ctx, w.slug(), tbl.ID, tables.PauseRequest{}); !errors.Is(err, tables.ErrBadTransition) {
		t.Errorf("pausing a cancelled table = %v", err)
	}
}

// A cancellation with an uncommitted file asks what to do, like the closing,
// and a cancelled table that is closing cannot be cancelled again.
func TestCancellingKeepsAWorktreeWithUncommittedFiles(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	w.dest.allowed = true
	w.be.write(t, w.git, w.repoOf(tbl).Dir(), "meio.txt", "x")
	_, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "x"})
	var miss *tables.MissingError
	if !errors.As(err, &miss) || len(miss.Decide) != 1 || !strings.Contains(miss.Decide[0], "meio.txt") {
		t.Fatalf("Close = %v", err)
	}
	if got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); got.State != tables.StateCancelling {
		t.Fatalf("state = %s; want cancelling", got.State)
	}
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID}); !errors.Is(err, tables.ErrBadTransition) {
		t.Errorf("closing a table that is cancelling = %v", err)
	}
	if len(w.gh.closedEp) != 1 {
		t.Errorf("the epic must be closed once, and it was %v", w.gh.closedEp)
	}
	res, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Keep: true})
	if err != nil || res.Table.State != tables.StateCancelled || len(res.Retained) != 1 {
		t.Fatalf("keep = %+v, %v", res, err)
	}
	if len(w.gh.closedEp) != 1 {
		t.Errorf("the epic was closed again: %v", w.gh.closedEp)
	}
	// The branch is checked out in a kept worktree: the sweep leaves it.
	w.clk.Advance(31 * 24 * time.Hour)
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); err != nil {
		t.Errorf("the branch of a kept worktree was deleted: %v", err)
	}
}

// Only the Grand Master of the workspace closes a table.
func TestOnlyTheGrandMasterCloses(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	tbl, _ := reviewed(t, w, 42, "Login")
	master, _, _ := w.reg.TableMaster(w.ctx, w.slug(), tbl.ID)
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: master, Table: tbl.ID}); !errors.Is(err, tables.ErrNotGrandMaster) {
		t.Errorf("a table master closing = %v", err)
	}
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: "99-nada"}); !errors.Is(err, tables.ErrNoTable) {
		t.Errorf("an unknown table = %v", err)
	}
}
