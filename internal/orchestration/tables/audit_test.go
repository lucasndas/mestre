package tables_test

import (
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
)

// The regression tests of the audit of milestone 4 (git sync of the tables).

// transfersToCitadel counts the bundles the Citadel recorded as received.
func transfersToCitadel(t *testing.T, w *world) int {
	t.Helper()
	var n int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM bundle_transfers WHERE direction = 'to_citadel'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func branchOf(t *testing.T, w *world, branch string) gitx.Hash {
	t.Helper()
	h, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+branch)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// The bundle of a Tower that carries the commits of another table into the
// branch is not applied: the move is an alert and the table is not trusted.
func TestATowerBundleWithTheCommitsOfAnotherTableIsAnAlertAndNotApplied(t *testing.T) {
	t.Parallel()
	towerBundleOfAnotherTable(t, nil)
}

func towerBundleOfAnotherTable(t *testing.T, be backend) {
	t.Helper()
	w, tw, tbl := towerWorld(t, be)
	w.gh.add(43, "Outra", "epico")
	other := w.open(43).Table
	if _, err := w.svc.Worktree(w.ctx, w.slug(), other.ID, "torre-1"); err != nil {
		t.Fatal(err)
	}
	tw.character(other)
	foreign := commit(t, w.be, tw.git, tw.repo(other.ID), "alheio.go", "package alheio\n", "feat: alheio")
	if _, err := tw.half.Done(w.ctx, w.slug(), other.ID); err != nil {
		t.Fatal(err)
	}
	if got := branchOf(t, w, other.Branch); got != foreign {
		t.Fatalf("the branch of table 43 = %s", got.Short())
	}
	before := branchOf(t, w, tbl.Branch)
	// An agent on the Tower moves the branch of table 42 onto the commit of 43.
	if err := tw.replica().UpdateRef(w.ctx, "refs/heads/"+tbl.Branch, foreign); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	if got := branchOf(t, w, tbl.Branch); got != before {
		t.Errorf("the branch of table 42 moved to %s with the commit of table 43", got.Short())
	}
	if !w.svc.Untrusted(w.slug(), tbl.ID) {
		t.Error("table 42 is still trusted")
	}
	if got := w.notices.texts(characters.ToTableMaster); len(got) == 0 {
		t.Error("the table master was not told")
	}
	if wt, _ := w.svc.Store().Worktree(w.ctx, w.slug(), tbl.ID, w.machine); wt.LastCommit != string(before) {
		t.Errorf("the Citadel registered %s", wt.LastCommit)
	}
}

// A bundle that comes from a Tower where no character of the table is alive
// is not a continuation anybody could have made: it is an alert, it is not
// recorded as transferred (it is looked at again after the trust), and the
// branch stays.
func TestATowerBundleWithNoCharacterAliveThereIsAnAlertAndNotRecorded(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	if err := w.chars.finish(w.ctx, "tc-torre-1-"+tbl.ID); err != nil {
		t.Fatal(err)
	}
	before := branchOf(t, w, tbl.Branch)
	transfers := transfersToCitadel(t, w)
	commit(t, w.be, tw.git, tw.repo(tbl.ID), "torre.go", "package torre\n", "feat: da torre")
	if _, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	if got := branchOf(t, w, tbl.Branch); got != before {
		t.Errorf("the branch moved to %s", got.Short())
	}
	if !w.svc.Untrusted(w.slug(), tbl.ID) {
		t.Error("the table is still trusted")
	}
	if got := transfersToCitadel(t, w); got != transfers {
		t.Errorf("a blocked bundle was recorded as transferred (%d -> %d)", transfers, got)
	}
}

// The first worktree of a table on a Tower starts at the tip of the Citadel:
// a replica branch that is behind is moved forward, one with commits the
// Citadel lacks is refused.
func TestTheFirstWorktreeOnATowerStartsAtTheTipOfTheCitadel(t *testing.T) {
	t.Parallel()
	replicaBranchScenario(t, nil)
}

func replicaBranchScenario(t *testing.T, be backend) {
	t.Helper()
	w, tw, tbl := towerWorld(t, be)
	// The worktree goes away; the branch stays in the replica.
	if err := tw.half.RemoveWorktree(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := tw.replica().ResolveRef(w.ctx, tables.CitadelRef(tbl.Branch)); got != "" {
		t.Errorf("the ref of the Citadel stayed in the replica: %s", got)
	}
	c := commit(t, w.be, w.git, w.repoOf(tbl), "c.go", "package c\n", "feat: cidadela")
	if err := w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: tbl.ID}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Worktree(w.ctx, w.slug(), tbl.ID, "torre-1"); err != nil {
		t.Fatal(err)
	}
	if got := headOf(t, tw.repo(tbl.ID)); got != c {
		t.Errorf("the worktree of the Tower is at %s; want the tip of the Citadel %s", got.Short(), c.Short())
	}

	// Now the Tower has a commit nobody else has, and the worktree goes away.
	x := commit(t, w.be, tw.git, tw.repo(tbl.ID), "x.go", "package x\n", "feat: so da torre")
	if err := tw.half.RemoveWorktree(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	d := commit(t, w.be, w.git, w.repoOf(tbl), "d.go", "package d\n", "feat: cidadela 2")
	if err := w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: tbl.ID}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Worktree(w.ctx, w.slug(), tbl.ID, "torre-1"); err == nil {
		t.Fatal("a worktree was made over a branch of the replica with commits the Citadel lacks")
	}
	if got, _ := tw.replica().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); got != x {
		t.Errorf("the branch of the replica = %s; want it untouched at %s (the Citadel is at %s)", got.Short(), x.Short(), d.Short())
	}
	if len(tw.notes()) == 0 {
		t.Error("the table master was not told")
	}
	if _, err := tw.half.Inspect(w.ctx, w.slug(), tbl.ID); !errors.Is(err, tables.ErrNoWorktree) {
		t.Errorf("the Tower has a worktree: %v", err)
	}
}

// The mark of a ref that moved without the engine survives a restart: the
// alerted move is not registered as the table's own after it, even with a
// character of the table alive again.
func TestTheUntrustedMarkSurvivesARestart(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(1, "Um", "epico")
	tbl := w.open(1).Table
	repo := w.repoOf(tbl)
	master, _, _ := w.reg.TableMaster(w.ctx, w.slug(), tbl.ID)
	if err := w.chars.finish(w.ctx, master.ID); err != nil {
		t.Fatal(err)
	}
	moved := commit(t, w.be, w.git, repo, "b.go", "package b\n", "feat: b")
	if as, err := w.svc.CheckRefs(w.ctx, w.slug()); err != nil || len(as) != 1 {
		t.Fatalf("alerts = %+v, %v", as, err)
	}
	svc2, err := tables.New(tables.Options{DB: w.db, Git: w.git, Workspaces: w.wss, Clock: w.clk, Machine: w.machine, GitHub: w.gh, Characters: w.chars, Registry: w.reg,
		Inbox: w.inb, Notifier: w.notices, Alerts: w.alerts, Files: w.files, Destructive: w.dest, Rituals: w.rituals, Peers: w.peers, TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !svc2.Untrusted(w.slug(), tbl.ID) {
		t.Fatal("the new Service trusts a table that was alerted")
	}
	if _, err := w.chars.Create(w.ctx, characters.CreateRequest{Sheet: "x", Workspace: w.slug(), Table: tbl.ID, Role: characters.RoleTableMaster,
		Creator: characters.Creator{Kind: characters.CreatedByEngine}}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc2.CheckRefs(w.ctx, w.slug()); err != nil {
		t.Fatal(err)
	}
	if wt, _ := svc2.Store().Worktree(w.ctx, w.slug(), tbl.ID, w.machine); wt.LastCommit == string(moved) {
		t.Error("after a restart the alerted move was registered as the table's own tip")
	}
	if !svc2.Untrusted(w.slug(), tbl.ID) {
		t.Error("the table became trusted by itself")
	}
	if err := svc2.TrustRef(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	if svc2.Untrusted(w.slug(), tbl.ID) {
		t.Error("TrustRef did not clear the mark")
	}
}

// An alert on a ref of a Tower stays when the branch of the Citadel is
// checked in the same pass, whatever the order of the machine ids.
func TestATowerRefAlertIsNotClearedByTheCheckOfTheBranch(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"torre-1", "a-torre"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			w := newWorld(t, worldOpts{})
			w.activate()
			tw := w.addTower(id)
			w.gh.add(42, "Login", "epico")
			tbl := w.open(42).Table
			if _, err := w.svc.Worktree(w.ctx, w.slug(), tbl.ID, id); err != nil {
				t.Fatal(err)
			}
			tw.character(tbl)
			commit(t, w.be, tw.git, tw.repo(tbl.ID), "torre.go", "package torre\n", "feat: da torre")
			if _, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err != nil {
				t.Fatal(err)
			}
			w.gh.add(43, "Outra", "epico")
			other := w.open(43).Table
			foreign := commit(t, w.be, w.git, w.repoOf(other), "alheio.go", "package alheio\n", "feat: alheio")
			if err := w.mirror().UpdateRef(w.ctx, tables.TowerRef(id, tbl.Branch), foreign); err != nil {
				t.Fatal(err)
			}
			if as, err := w.svc.CheckRefs(w.ctx, w.slug()); err != nil || len(as) == 0 {
				t.Fatalf("alerts = %+v, %v", as, err)
			}
			if !w.svc.Untrusted(w.slug(), tbl.ID) {
				t.Fatal("the alert of the ref of the Tower was cleared by the check of the branch")
			}
			if err := w.svc.SyncTower(w.ctx, w.slug(), tbl.ID, id); !errors.Is(err, tables.ErrRefMoved) {
				t.Errorf("SyncTower = %v; want ref_moved", err)
			}
		})
	}
}

// The check of the refs runs under the lock of the table, so it does not see
// the move of an operation in the middle.
func TestTheCheckOfTheRefsTakesTheLockOfTheTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	for name, run := range map[string]func(){
		"CheckRefs": func() { _, _ = w.svc.CheckRefs(w.ctx, w.slug()) },
		"Done":      func() { _ = w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: tbl.ID}, "") },
	} {
		unlock := w.svc.LockTable(w.slug(), tbl.ID)
		done := make(chan struct{})
		go func() { run(); close(done) }()
		for range 500 {
			runtime.Gosched()
		}
		select {
		case <-done:
			t.Errorf("%s ran while another operation held the lock of the table", name)
		default:
		}
		unlock()
		<-done
	}
}

// A cancellation does not lose the commits of a Tower that the Citadel did
// not take into the branch: the worktree stays, and so does the ref the
// Tower's bundle made, until the branch leaves the mirror.
func TestCancellingKeepsTheTowerCommitsThatDivergedUntilTheBranchGoes(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	commit(t, w.be, w.git, w.repoOf(tbl), "c.go", "package c\n", "feat: cidadela")
	if err := w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: tbl.ID}, ""); err != nil {
		t.Fatal(err)
	}
	tip := commit(t, w.be, tw.git, tw.repo(tbl.ID), "t.go", "package t\n", "feat: torre")
	if _, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	ref := tables.TowerRef("torre-1", tbl.Branch)
	if got, _ := w.mirror().ResolveRef(w.ctx, ref); got != tip {
		t.Fatalf("tower ref = %s", got.Short())
	}
	w.dest.allowed = true
	_, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "x"})
	var miss *tables.MissingError
	if !errors.As(err, &miss) || len(miss.Decide) != 1 || !strings.Contains(miss.Decide[0], "torre-1") {
		t.Fatalf("Close = %v; want the worktree of the Tower to be decided on", err)
	}
	if _, err := tw.half.Inspect(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Errorf("the worktree of the Tower was removed: %v", err)
	}
	// The Grand Master decides to keep it: the table ends, the commits are
	// in the ref of the Tower for the whole retention.
	res, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Keep: true})
	if err != nil || res.Table.State != tables.StateCancelled {
		t.Fatalf("keep = %+v, %v", res, err)
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, ref); got != tip {
		t.Errorf("the ref of the Tower is %q after the cancellation; the commit would be lost", got)
	}
	w.clk.Advance(29 * 24 * time.Hour)
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, ref); got != tip {
		t.Errorf("the ref of the Tower went before the 30 days")
	}
	w.clk.Advance(2 * 24 * time.Hour)
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, ref); got != "" {
		t.Errorf("the ref of the Tower stayed after the branch went: %s", got.Short())
	}
}

// The worktree of the Citadel whose HEAD is not in the branch of the table
// (an agent switched it) is not removed by a cancellation.
func TestCancellingKeepsAWorktreeOfTheCitadelWhoseHeadIsNotInTheBranch(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	repo := w.repoOf(tbl)
	if err := repo.CreateBranch(w.ctx, "outra", tbl.Branch); err != nil {
		t.Fatal(err)
	}
	w.git.(*spyGit).Git.(*fakegit.Git).Handle(repo.Dir()).SetHead("outra")
	commit(t, w.be, w.git, repo, "o.go", "package o\n", "feat: na outra")
	w.dest.allowed = true
	_, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "x"})
	var miss *tables.MissingError
	if !errors.As(err, &miss) || len(miss.Decide) != 1 {
		t.Fatalf("Close = %v; want the worktree to be decided on", err)
	}
}

// The merge of the Tower's commits into the worktree of the Citadel goes only
// into the branch of the table: a worktree that has another branch checked
// out is left alone.
func TestABundleIsNotMergedIntoAnotherBranchTheWorktreeHasCheckedOut(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	repo := w.repoOf(tbl)
	if err := repo.CreateBranch(w.ctx, "outra", tbl.Branch); err != nil {
		t.Fatal(err)
	}
	other, _ := w.mirror().ResolveRef(w.ctx, "refs/heads/outra")
	w.git.(*spyGit).Git.(*fakegit.Git).Handle(repo.Dir()).SetHead("outra")
	commit(t, w.be, tw.git, tw.repo(tbl.ID), "torre.go", "package torre\n", "feat: da torre")
	if _, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, "refs/heads/outra"); got != other {
		t.Errorf("the other branch was moved to %s by the merge of the bundle", got.Short())
	}
	if got := w.notices.texts(characters.ToTableMaster); len(got) == 0 {
		t.Error("the table master was not told")
	}
}

// The same on the Tower: the tip of the Citadel is merged only into the
// branch of the table.
func TestTheTipOfTheCitadelIsNotMergedIntoAnotherBranchOfTheTower(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	towerRepo := tw.repo(tbl.ID)
	if err := towerRepo.CreateBranch(w.ctx, "outra", tbl.Branch); err != nil {
		t.Fatal(err)
	}
	other, _ := tw.replica().ResolveRef(w.ctx, "refs/heads/outra")
	tw.git.(*fakegit.Git).Handle(towerRepo.Dir()).SetHead("outra")
	commit(t, w.be, w.git, w.repoOf(tbl), "c.go", "package c\n", "feat: cidadela")
	if err := w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: tbl.ID}, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.SyncTower(w.ctx, w.slug(), tbl.ID, "torre-1"); err != nil {
		t.Fatal(err)
	}
	if got, _ := tw.replica().ResolveRef(w.ctx, "refs/heads/outra"); got != other {
		t.Errorf("the other branch of the Tower was moved to %s", got.Short())
	}
	if len(tw.notes()) == 0 {
		t.Error("the table master was not told")
	}
}

// Reopening the epic of a table that was cancelled gives a new table.
func TestTheEpicOfACancelledTableCanBeOpenedAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	first := w.open(42).Table
	w.dest.allowed = true
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: first.ID, Cancel: true, Reason: "x"}); err != nil {
		t.Fatal(err)
	}
	w.gh.add(42, "Login", "epico") // reopened on GitHub
	again, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 42})
	if err != nil {
		t.Fatalf("opening the epic again = %v", err)
	}
	if again.Table.ID == first.ID || again.Table.Branch == first.Branch || again.Table.State == tables.StateCancelled {
		t.Errorf("the new table = %+v; the old one %+v", again.Table, first)
	}
}

type prefixSettings struct {
	tables.DefaultSettings
	prefix    string
	retention time.Duration
}

func (p prefixSettings) BranchPrefix(string) string { return p.prefix }
func (p prefixSettings) CancelRetention(string) time.Duration {
	return p.retention
}

// The settings that make a branch name or a retention are checked (one bad
// prefix and one bad retention; the whole list is in the full suite).
func TestABadBranchPrefixAndABadRetentionOfTheWorkspaceAreRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{settings: prefixSettings{prefix: "a..b/", retention: time.Hour}})
	w.activate()
	w.gh.add(42, "Login", "epico")
	if _, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 42}); err == nil {
		t.Error("the prefix a..b/ was accepted")
	}
	if ts, _ := w.svc.List(w.ctx, w.slug()); len(ts) != 0 {
		t.Errorf("the prefix a..b/ left a table: %+v", ts)
	}
	w = newWorld(t, worldOpts{settings: prefixSettings{prefix: "epico/", retention: 0}})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	w.dest.allowed = true
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "x"}); err == nil {
		t.Error("a retention of 0 was accepted")
	}
}

// A table that is cancelling (or closing, or over) is not synchronised to a
// Tower.
func TestATableThatIsEndingIsNotSynchronisedToATower(t *testing.T) {
	t.Parallel()
	w, _, tbl := towerWorld(t, nil)
	w.dest.allowed = true
	w.be.write(t, w.git, w.repoOf(tbl).Dir(), "meio.txt", "x")
	var miss *tables.MissingError
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "x"}); !errors.As(err, &miss) {
		t.Fatalf("Close = %v", err)
	}
	if err := w.svc.SyncTower(w.ctx, w.slug(), tbl.ID, "torre-1"); !errors.Is(err, tables.ErrBadTransition) {
		t.Errorf("SyncTower of a cancelling table = %v", err)
	}
}

// The Grand Master that closes or opens is read again: one that stopped since
// the request was made does nothing.
func TestAGrandMasterThatStoppedCannotOpenOrClose(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	if err := w.chars.finish(w.ctx, w.gm.ID); err != nil {
		t.Fatal(err)
	}
	w.dest.allowed = true
	if _, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "x"}); !errors.Is(err, tables.ErrNotGrandMaster) {
		t.Errorf("Close = %v", err)
	}
	w.gh.add(43, "Outra", "epico")
	if _, err := w.svc.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: 43}); !errors.Is(err, tables.ErrNotGrandMaster) {
		t.Errorf("Open = %v", err)
	}
}

// A worktree whose files are in use (Windows) is not an error of the sweep:
// the table keeps closing and the removal is tried again.
func TestAWorktreeWithFilesInUseIsRetriedNotAnError(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	w.dest.allowed = true
	w.spy.mu.Lock()
	w.spy.busy = true
	w.spy.mu.Unlock()
	_, err := w.svc.Close(w.ctx, tables.CloseRequest{By: w.gm, Table: tbl.ID, Cancel: true, Reason: "x"})
	var miss *tables.MissingError
	if !errors.As(err, &miss) || len(miss.Pending) == 0 {
		t.Fatalf("Close = %v; want pending", err)
	}
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Errorf("the sweep failed: %v", err)
	}
	w.spy.mu.Lock()
	w.spy.busy = false
	w.spy.mu.Unlock()
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID); got.State != tables.StateCancelled {
		t.Errorf("state = %s; want cancelled once the files are free", got.State)
	}
}
