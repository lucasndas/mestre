package tables_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/protocol"
)

// towerWorld is a table with a worktree on a Tower: the Tower got its first
// character, so the Citadel sent the branch and the Tower made the worktree.
func towerWorld(t *testing.T, be backend) (*world, *tower, tables.Table) {
	t.Helper()
	w := newWorld(t, worldOpts{be: be})
	w.activate()
	tw := w.addTower("torre-1")
	w.gh.add(42, "Login", "epico")
	tbl := w.open(42).Table
	dir, err := w.svc.Worktree(w.ctx, w.slug(), tbl.ID, "torre-1")
	if err != nil {
		t.Fatal(err)
	}
	if dir != tw.dir(w.slug(), tbl.ID) {
		t.Fatalf("the Tower's worktree is %q; want %q", dir, tw.dir(w.slug(), tbl.ID))
	}
	return w, tw, tbl
}

func headOf(t *testing.T, r gitx.Repo) gitx.Hash {
	t.Helper()
	h, err := r.ResolveRef(t.Context(), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return h
}

func hasFile(t *testing.T, r gitx.Repo, rel string) bool {
	t.Helper()
	_, err := r.ReadFile(t.Context(), "HEAD", rel)
	return err == nil
}

// Part of CA-MESAS-03: the first character of the table on a Tower makes the
// Citadel send the branch and the Tower makes the worktree; a character there
// commits and gives `done`, and the commit appears in the branch of the
// worktree of the Citadel; the next step on the Tower receives the commits of
// the Citadel before it begins. (Two engines over the real connection are
// milestone 5; here the bundles cross the same receivers the connection calls.)
func TestTheCommitsCrossBetweenTheTowerAndTheCitadel(t *testing.T) {
	t.Parallel()
	crossScenario(t, nil)
}

// crossScenario is the scenario above over the git of a backend.
func crossScenario(t *testing.T, be backend) {
	t.Helper()
	w, tw, tbl := towerWorld(t, be)
	towerRepo := tw.repo(tbl.ID)
	citadel := w.repoOf(tbl)
	if headOf(t, towerRepo) != headOf(t, citadel) {
		t.Fatalf("the Tower's worktree is at %s; the Citadel's at %s", headOf(t, towerRepo).Short(), headOf(t, citadel).Short())
	}
	if rec, err := w.svc.Store().Worktree(w.ctx, w.slug(), tbl.ID, "torre-1"); err != nil || rec.Dir != tw.dir(w.slug(), tbl.ID) {
		t.Errorf("the record of the Tower's worktree = %+v, %v", rec, err)
	}
	// The Tower's session zero ran in its worktree.
	if got := tw.files.list(&tw.files.prepared); !slices.Equal(got, []string{tw.dir(w.slug(), tbl.ID)}) {
		t.Errorf("the session zero of the Tower ran in %v", got)
	}

	// A character on the Tower commits and gives done.
	tip := commit(t, w.be, tw.git, towerRepo, "torre.go", "package torre\n", "feat: da torre")
	rep, err := tw.half.Done(w.ctx, w.slug(), tbl.ID)
	if err != nil || !rep.Sent || rep.Tip != string(tip) || len(rep.Warnings) != 0 {
		t.Fatalf("Done = %+v, %v", rep, err)
	}
	if got := headOf(t, citadel); got != tip {
		t.Fatalf("the worktree of the Citadel is at %s; want the Tower's commit %s", got.Short(), tip.Short())
	}
	if !hasFile(t, citadel, "torre.go") {
		t.Error("the file of the Tower's commit is not in the worktree of the Citadel")
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); got != tip {
		t.Errorf("the branch of the mirror = %s; want %s", got.Short(), tip.Short())
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, tables.TowerRef("torre-1", tbl.Branch)); got != tip {
		t.Errorf("refs/mestre/torres/... = %s; want %s", got.Short(), tip.Short())
	}
	if wt, _ := w.svc.Store().Worktree(w.ctx, w.slug(), tbl.ID, w.machine); wt.LastCommit != string(tip) {
		t.Errorf("the engine registered %s for the Citadel; want %s", wt.LastCommit, tip)
	}

	// The Citadel moves ahead (a character there commits); the next step on
	// the Tower gets the commits before it starts, with an incremental bundle.
	cip := commit(t, w.be, w.git, citadel, "cidadela.go", "package cidadela\n", "feat: da cidadela")
	if err := w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: tbl.ID}, ""); err != nil { // the engine registers the commit
		t.Fatal(err)
	}
	before := w.peers.sent["torre-1"]
	if err := w.svc.SyncTower(w.ctx, w.slug(), tbl.ID, "torre-1"); err != nil {
		t.Fatal(err)
	}
	if got := headOf(t, towerRepo); got != cip {
		t.Fatalf("the Tower is at %s after the sync; want %s", got.Short(), cip.Short())
	}
	if !hasFile(t, towerRepo, "cidadela.go") || !hasFile(t, towerRepo, "torre.go") {
		t.Error("the worktree of the Tower lacks the files of the commits")
	}
	if got := w.peers.sent["torre-1"] - before; got != 1 {
		t.Errorf("bundles sent in the sync = %d", got)
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, tables.CitadelRef(tbl.Branch)); got != "" {
		t.Errorf("the Citadel keeps a ref meant for the Towers: %s", got)
	}
	// Nothing new: nothing is sent.
	before = w.peers.sent["torre-1"]
	if err := w.svc.SyncTower(w.ctx, w.slug(), tbl.ID, "torre-1"); err != nil || w.peers.sent["torre-1"] != before {
		t.Errorf("a sync with nothing new sent a bundle: %v", err)
	}
	if rep, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err != nil || rep.Sent {
		t.Errorf("a done with nothing new = %+v, %v", rep, err)
	}
}

// Part of CA-MESAS-04: commits that diverge between the Citadel and the Tower:
// when the Tower sends the bundle the branch of the Citadel does not change,
// the worktree of the Citadel stays where it was, and the table master gets the
// notice of divergence with the commits of each side.
func TestADivergentBundleLeavesTheBranchOfTheCitadelAndWarnsTheTableMaster(t *testing.T) {
	t.Parallel()
	divergenceScenario(t, nil)
}

// divergenceScenario is the scenario above over the git of a backend.
func divergenceScenario(t *testing.T, be backend) {
	t.Helper()
	w, tw, tbl := towerWorld(t, be)
	citadel := w.repoOf(tbl)
	cip := commit(t, w.be, w.git, citadel, "cidadela.go", "package cidadela\n", "feat: so da cidadela")
	if err := w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: tbl.ID}, ""); err != nil {
		t.Fatal(err)
	}
	tip := commit(t, w.be, tw.git, tw.repo(tbl.ID), "torre.go", "package torre\n", "feat: so da torre")

	rep, err := tw.half.Done(w.ctx, w.slug(), tbl.ID)
	if err != nil || !rep.Sent {
		t.Fatalf("Done = %+v, %v", rep, err)
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); got != cip {
		t.Errorf("the branch of the Citadel moved to %s; want it at %s", got.Short(), cip.Short())
	}
	if got := headOf(t, citadel); got != cip {
		t.Errorf("the worktree of the Citadel moved to %s; want %s", got.Short(), cip.Short())
	}
	if hasFile(t, citadel, "torre.go") {
		t.Error("the commit of the Tower reached the Citadel's worktree")
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, tables.TowerRef("torre-1", tbl.Branch)); got != tip {
		t.Errorf("the tip of the Tower is not kept in refs/mestre/torres: %s", got.Short())
	}
	notes := w.notices.texts(characters.ToTableMaster)
	if len(notes) != 1 || !strings.Contains(notes[0], "Divergência") || !strings.Contains(notes[0], "so da cidadela") || !strings.Contains(notes[0], "so da torre") ||
		!strings.Contains(notes[0], cip.Short()) || !strings.Contains(notes[0], tip.Short()) {
		t.Fatalf("notices to the table master = %q", notes)
	}
}

// The commits that were not made do not travel: the done reports the files
// that were not committed.
func TestUncommittedChangesAreReportedAndNotSent(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	towerRepo := tw.repo(tbl.ID)
	w.be.write(t, tw.git, towerRepo.Dir(), "rascunho.go", "package x\n")
	rep, err := tw.half.Done(w.ctx, w.slug(), tbl.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Sent || len(rep.Warnings) != 1 || !strings.Contains(rep.Warnings[0], "alterações não commitadas não foram sincronizadas") || !strings.Contains(rep.Warnings[0], "rascunho.go") {
		t.Errorf("report = %+v", rep)
	}
	if hasFile(t, w.repoOf(tbl), "rascunho.go") {
		t.Error("an uncommitted file reached the Citadel")
	}
}

// A Tower that is disconnected leaves the sending pending: the step that
// depends on it waits, and the sending is done again when it is back.
func TestADisconnectedTowerLeavesTheSendingPending(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	tip := commit(t, w.be, tw.git, tw.repo(tbl.ID), "torre.go", "package torre\n", "feat: da torre")
	tw.mu.Lock()
	tw.sendErr = errors.New("the Citadel cannot be reached")
	tw.mu.Unlock()
	if _, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err == nil {
		t.Fatal("Done succeeded without the Citadel")
	}
	if headOf(t, w.repoOf(tbl)) == tip {
		t.Fatal("the commit arrived without a connection")
	}
	tw.mu.Lock()
	tw.sendErr = nil
	tw.mu.Unlock()
	if err := tw.half.Resend(w.ctx); err != nil {
		t.Fatal(err)
	}
	if headOf(t, w.repoOf(tbl)) != tip {
		t.Error("the sending was not done again")
	}
	// And the other way: the Citadel cannot sync a Tower that is down.
	commit(t, w.be, w.git, w.repoOf(tbl), "c.go", "package c\n", "feat: c")
	w.peers.setDown("torre-1", true)
	if err := w.svc.SyncTower(w.ctx, w.slug(), tbl.ID, "torre-1"); !errors.Is(err, tables.ErrMachineUnreachable) {
		t.Errorf("SyncTower to a Tower that is down = %v", err)
	}
}

// Each transfer is idempotent by the hash of the bundle: the same bundle
// arriving again changes nothing and is not applied twice.
func TestTheSameBundleIsAppliedOnce(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	tip := commit(t, w.be, tw.git, tw.repo(tbl.ID), "torre.go", "package torre\n", "feat: da torre")
	rcv := w.svc.ReceiverFor("torre-1")
	base, err := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch)
	if err != nil {
		t.Fatal(err)
	}
	meta := tables.BundleMeta{Workspace: w.slug(), Table: tbl.ID, Branch: tbl.Branch, Tip: string(tip), Base: string(base)}.Object()
	src := bundleSourceOf(t, tw.replica(), tbl.Branch, string(base))
	for i := range 2 {
		if err := deliver(w.ctx, t, rcv, meta, src, nil); err != nil {
			t.Fatalf("delivery %d: %v", i+1, err)
		}
	}
	if got := headOf(t, w.repoOf(tbl)); got != tip {
		t.Fatalf("the Citadel is at %s; want %s", got.Short(), tip.Short())
	}
	if n := len(w.files.list(&w.files.synced)); n != 1 {
		t.Errorf("the worktree of the Citadel was moved %d times; want once", n)
	}
}

// A Tower can only send a branch of a table that exists and is not over, and
// only the branch of that table.
func TestABundleOfAnotherBranchIsRefused(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	rcv := w.svc.ReceiverFor("torre-1")
	repo := tw.replica()
	tip, _ := repo.ResolveRef(w.ctx, "refs/heads/"+tbl.Branch)
	for name, meta := range map[string]tables.BundleMeta{
		"another branch": {Workspace: w.slug(), Table: tbl.ID, Branch: "main", Tip: string(tip)},
		"another table":  {Workspace: w.slug(), Table: "9-nada", Branch: "epico/9-nada", Tip: string(tip)},
	} {
		err := deliver(w.ctx, t, rcv, meta.Object(), bundleSourceOf(t, repo, tbl.Branch, ""), nil)
		var pe *protocol.Error
		if !asProto(err, &pe) || pe.Code != protocol.CodeInvalidPayload {
			t.Errorf("%s: %v; want invalid_payload", name, err)
		}
	}
	if err := deliver(w.ctx, t, rcv, protocol.Object{"mirror": []byte(`"meu-app"`)}, bundleSourceOf(t, repo, tbl.Branch, ""), nil); err == nil {
		t.Error("the bundle of a mirror was taken as the bundle of a table")
	}
}

// A ref that moved without passing through the engine is an alert to the table
// master and to the Administrators, and nothing of the table is sent or
// synchronised until someone trusts it again.
func TestARefThatMovedWithoutTheEngineIsAnAlertAndNotTrusted(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	w.gh.add(43, "Outra", "epico")
	other := w.open(43).Table
	// A character of the other table (a Codex with the write of every ref of the
	// mirror) points the branch of the first table at its own work.
	foreign := commit(t, w.be, w.git, w.repoOf(other), "alheio.go", "package alheio\n", "feat: alheio")
	if err := w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: other.ID}, ""); err != nil { // the engine registers it for its table
		t.Fatal(err)
	}
	if err := w.mirror().UpdateRef(w.ctx, "refs/heads/"+tbl.Branch, foreign); err != nil {
		t.Fatal(err)
	}
	alerts, err := w.svc.CheckRefs(w.ctx, w.slug())
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].Table != tbl.ID || alerts[0].Current != string(foreign) {
		t.Fatalf("alerts = %+v", alerts)
	}
	if got := w.notices.texts(characters.ToTableMaster); len(got) != 1 || !strings.Contains(got[0], tbl.Branch) {
		t.Errorf("the table master was told %q", got)
	}
	if as := w.alerts.all(); len(as) != 1 || as[0].Kind != workspaces.AlertRefMoved {
		t.Errorf("operational alerts = %+v", as)
	}
	if !w.svc.Untrusted(w.slug(), tbl.ID) || w.svc.Untrusted(w.slug(), other.ID) {
		t.Error("the table that was moved must be untrusted, and only that one")
	}
	// Not sent, not synchronised, not applied.
	before := w.peers.sent["torre-1"]
	if err := w.svc.SyncTower(w.ctx, w.slug(), tbl.ID, "torre-1"); !errors.Is(err, tables.ErrRefMoved) {
		t.Errorf("SyncTower = %v; want ref_moved", err)
	}
	if w.peers.sent["torre-1"] != before {
		t.Error("a bundle of an untrusted table was sent")
	}
	commit(t, w.be, tw.git, tw.repo(tbl.ID), "torre.go", "package torre\n", "feat: da torre")
	if _, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); got != foreign {
		t.Errorf("the branch of an untrusted table was moved by a bundle: %s", got.Short())
	}
	// The same alert is not repeated.
	if _, err := w.svc.CheckRefs(w.ctx, w.slug()); err != nil {
		t.Fatal(err)
	}
	if got := w.notices.texts(characters.ToTableMaster); len(got) != 1 {
		t.Errorf("the alert was repeated: %q", got)
	}
	// An Administrator looks and trusts the ref again.
	if err := w.svc.TrustRef(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	if w.svc.Untrusted(w.slug(), tbl.ID) {
		t.Error("still untrusted after TrustRef")
	}
	if err := w.svc.SyncTower(w.ctx, w.slug(), tbl.ID, "torre-1"); err != nil {
		t.Errorf("SyncTower after trusting = %v", err)
	}
}

// The tip a Tower sent is registered by the engine: the ref
// refs/mestre/torres/... moved by someone else is an alert too.
func TestTheRefOfATowerThatMovedIsAnAlert(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, nil)
	commit(t, w.be, tw.git, tw.repo(tbl.ID), "torre.go", "package torre\n", "feat: da torre")
	if _, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err != nil {
		t.Fatal(err)
	}
	if as, err := w.svc.CheckRefs(w.ctx, w.slug()); err != nil || len(as) != 0 {
		t.Fatalf("alerts after a good sync = %+v, %v", as, err)
	}
	w.gh.add(43, "Outra", "epico")
	other := w.open(43).Table
	foreign := commit(t, w.be, w.git, w.repoOf(other), "alheio.go", "package alheio\n", "feat: alheio")
	if err := w.mirror().UpdateRef(w.ctx, tables.TowerRef("torre-1", tbl.Branch), foreign); err != nil {
		t.Fatal(err)
	}
	as, err := w.svc.CheckRefs(w.ctx, w.slug())
	if err != nil || len(as) != 1 || as[0].Ref != tables.TowerRef("torre-1", tbl.Branch) {
		t.Fatalf("alerts = %+v, %v", as, err)
	}
}

// A branch that moved ahead in a table with no character alive to have done it
// is an alert; in a table with characters it is their commits, registered.
func TestAnAdvanceIsRegisteredOnlyWhereACharacterCouldHaveMadeIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(1, "Um", "epico")
	tbl := w.open(1).Table
	repo := w.repoOf(tbl)
	tip := commit(t, w.be, w.git, repo, "a.go", "package a\n", "feat: a")
	if as, err := w.svc.CheckRefs(w.ctx, w.slug()); err != nil || len(as) != 0 {
		t.Fatalf("alerts = %+v, %v", as, err)
	}
	if wt, _ := w.svc.Store().Worktree(w.ctx, w.slug(), tbl.ID, w.machine); wt.LastCommit != string(tip) {
		t.Errorf("the commit of a character was not registered: %s", wt.LastCommit)
	}
	// Now nobody is alive in the table.
	master, _, _ := w.reg.TableMaster(w.ctx, w.slug(), tbl.ID)
	if err := w.chars.finish(w.ctx, master.ID); err != nil {
		t.Fatal(err)
	}
	commit(t, w.be, w.git, repo, "b.go", "package b\n", "feat: b")
	if as, err := w.svc.CheckRefs(w.ctx, w.slug()); err != nil || len(as) != 1 {
		t.Fatalf("alerts = %+v, %v; want the advance of a table with nobody alive", as, err)
	}
}

// bundleSourceOf builds the sources of a bundle for a test.
func bundleSourceOf(t *testing.T, repo gitx.Repo, branch, base string) protocol.BundleSource {
	t.Helper()
	return tables.BundleSources(repo, t.TempDir(), branch, base)
}
