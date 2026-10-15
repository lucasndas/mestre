//go:build full

package tables_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/pkg/clock"
)

// realBackend is the real git of the machine: the same scenarios of the fast
// suite run over it, so the fake cannot drift from what the engine relies on.
type realBackend struct{}

func (realBackend) newGit(t *testing.T, clk clock.Clock) tables.Git {
	t.Helper()
	g, err := gitx.New(gitx.Options{Clock: clk, Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func (b realBackend) initRepo(t *testing.T, g tables.Git, dir string, files map[string]string) gitx.Repo {
	t.Helper()
	repo, err := g.Init(t.Context(), dir, gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range files {
		b.write(t, g, dir, rel, body)
	}
	if err := repo.Stage(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(t.Context(), gitx.CommitOptions{Message: "chore: initial commit", Author: gitx.Signature{Name: "Teste", Email: "teste@example.invalid", When: day0}}); err != nil {
		t.Fatal(err)
	}
	return repo
}

func (realBackend) write(t *testing.T, _ tables.Git, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Part of CA-MESAS-01: with the real git, the opening of the table of the epic
// 42 makes the branch epico/42-login-de-usuarios from origin/main, the worktree
// of the mirror, and the first message of the table master. (`gh epic create`
// and the real table master are milestone 5.)
func TestRealOpeningMakesTheBranchAndTheWorktree(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{be: realBackend{}})
	w.activate()
	w.gh.add(42, "Login de usuários", "epico")
	res := w.open(42)
	if res.Table.Branch != "epico/42-login-de-usuarios" {
		t.Fatalf("branch = %q", res.Table.Branch)
	}
	main, err := w.mirror().ResolveRef(w.ctx, "refs/remotes/origin/main")
	if err != nil {
		t.Fatal(err)
	}
	if head := headOf(t, w.repoOf(res.Table)); head != main {
		t.Errorf("the worktree is at %s; want origin/main %s", head.Short(), main.Short())
	}
	if _, err := os.Stat(filepath.Join(res.Worktree, "README.md")); err != nil {
		t.Errorf("the worktree has no checkout: %v", err)
	}
	// The engine opens it only by its record, and the record says what git says.
	wt, err := w.svc.Store().Worktree(w.ctx, w.slug(), res.Table.ID, w.machine)
	if err != nil || filepath.Base(wt.GitDir) != "42-login-de-usuarios" || !strings.HasSuffix(filepath.ToSlash(wt.Common), "espelho.git") {
		t.Errorf("record = %+v, %v", wt, err)
	}
}

// Part of CA-MESAS-03: the same scenario with the real git and real bundles.
func TestRealCommitsCrossBetweenTheTowerAndTheCitadel(t *testing.T) {
	t.Parallel()
	crossScenario(t, realBackend{})
}

// CA-MESAS-04: the commits diverge between the Citadel and the Tower, with the
// real git and a real bundle: when the Tower sends it, the branch of the
// Citadel does not change and the table master gets the notice.
func TestRealDivergentBundleLeavesTheBranchOfTheCitadelAndWarnsTheTableMaster(t *testing.T) {
	t.Parallel()
	divergenceScenario(t, realBackend{})
}

// A Tower that is ahead of the Citadel in a straight line is fast-forwarded in
// the worktree of the Citadel, and one whose worktree has changes that
// conflict with the commits leaves everything as it is and tells the table
// master.
func TestRealAnAdvanceThatConflictsWithTheWorktreeChangesNothing(t *testing.T) {
	t.Parallel()
	w, tw, tbl := towerWorld(t, realBackend{})
	citadel := w.repoOf(tbl)
	// An uncommitted change of the Citadel in the file the Tower will commit.
	w.be.write(t, w.git, citadel.Dir(), "login.go", "package pessoa\n")
	tip := commit(t, w.be, tw.git, tw.repo(tbl.ID), "login.go", "package torre\n", "feat: login")
	before := headOf(t, citadel)
	if rep, err := tw.half.Done(w.ctx, w.slug(), tbl.ID); err != nil || !rep.Sent {
		t.Fatalf("Done = %+v, %v", rep, err)
	}
	if got := headOf(t, citadel); got != before {
		t.Errorf("the worktree moved to %s although its changes conflict", got.Short())
	}
	b, _ := os.ReadFile(filepath.Join(citadel.Dir(), "login.go"))
	if string(b) != "package pessoa\n" {
		t.Errorf("the change in the worktree was lost: %q", b)
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); got != before {
		t.Errorf("the branch moved to %s", got.Short())
	}
	if notes := w.notices.texts(characters.ToTableMaster); len(notes) != 1 || !strings.Contains(notes[0], "conflitam") {
		t.Errorf("notices to the table master = %q", notes)
	}
	if got, _ := w.mirror().ResolveRef(w.ctx, tables.TowerRef("torre-1", tbl.Branch)); got != tip {
		t.Errorf("the tip of the Tower was not kept: %s", got.Short())
	}
}

// Part of CA-CLI-GM-07: with the real git, the merge of the pull request
// confirmed and a local commit that was not sent, `table close` leaves with 7
// unmerged_changes, the table stays closing and the worktree stays.
func TestRealCloseWithACommitThatIsNotInThePullRequest(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{be: realBackend{}})
	w.activate()
	tbl, _ := reviewed(t, w, 42, "Login")
	commit(t, w.be, w.git, w.repoOf(tbl), "depois.go", "package depois\n", "feat: depois do merge")
	r := w.run(w.gm.ID, "table close", nil, tbl.ID)
	if r.OK || errCode(r) != "unmerged_changes" || r.ExitCode() != 7 {
		t.Fatalf("close = %+v (exit %d)", r.Error, r.ExitCode())
	}
	got, _ := w.svc.Get(w.ctx, w.slug(), tbl.ID)
	if got.State != tables.StateClosing {
		t.Errorf("state = %s; want closing", got.State)
	}
	if _, err := os.Stat(filepath.Join(w.repoOf(tbl).Dir(), "depois.go")); err != nil {
		t.Errorf("the worktree is gone: %v", err)
	}
	// Integrated and clean: it goes, and the branch with it.
	w2 := newWorld(t, worldOpts{be: realBackend{}})
	w2.activate()
	tbl2, _ := reviewed(t, w2, 43, "Outro")
	if res, err := w2.svc.Close(w2.ctx, tables.CloseRequest{By: w2.gm, Table: tbl2.ID}); err != nil || res.Table.State != tables.StateClosed {
		t.Fatalf("close = %+v, %v", res, err)
	}
	if _, err := os.Stat(filepath.Join(w2.wss.Layout().Tables(w2.slug()), tbl2.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the worktree folder is still there: %v", err)
	}
	if _, err := w2.mirror().ResolveRef(w2.ctx, "refs/heads/"+tbl2.Branch); !errors.Is(err, gitx.ErrRefNotFound) {
		t.Errorf("the branch is still in the mirror: %v", err)
	}
}

// CA-WS-02: a workspace of a local repository is created, and until it has gone
// through its stages `mestre table open` is refused with 7
// workspace_not_active, saying the stage; after the last stage the command
// goes through and opens the table. The command itself is what is tested, with
// the real git.
func TestRealTheCommandOpenIsRefusedBeforeTheWorkspaceIsActive(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{be: realBackend{}})
	ws, err := w.wss.Create(w.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	// The Grand Master may already exist while the workspace prepares.
	w.insertGM(ws.Slug)
	w.gh.add(42, "Login", "epico")
	r := w.run(w.gm.ID, "table open", map[string]any{"epic": "42"})
	if r.OK || errCode(r) != "workspace_not_active" || r.ExitCode() != localchan.ExitConflict || !strings.Contains(r.Error.Message, "apps") {
		t.Fatalf("table open before the stages = %+v (exit %d)", r.Error, r.ExitCode())
	}
	if ts, _ := w.svc.List(w.ctx, ws.Slug); len(ts) != 0 {
		t.Errorf("a table was left behind: %+v", ts)
	}
	if err := w.wss.Prepare(w.ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	r = w.run(w.gm.ID, "table open", map[string]any{"epic": "42"})
	if !r.OK {
		t.Fatalf("table open after the stages = %+v", r.Error)
	}
	if _, err := w.svc.Get(w.ctx, ws.Slug, "42-login"); err != nil {
		t.Errorf("the table did not open: %v", err)
	}
}

// With the real git, a branch of a table that someone moves is an alert and
// the engine does not send it.
func TestRealARefMovedByAnotherTableIsAnAlert(t *testing.T) {
	t.Parallel()
	w, _, tbl := towerWorld(t, realBackend{})
	w.gh.add(43, "Outra", "epico")
	other := w.open(43).Table
	foreign := commit(t, w.be, w.git, w.repoOf(other), "alheio.go", "package alheio\n", "feat: alheio")
	if err := w.svc.Done(w.ctx, characters.Character{Workspace: w.slug(), Table: other.ID}, ""); err != nil {
		t.Fatal(err)
	}
	if err := w.mirror().UpdateRef(w.ctx, "refs/heads/"+tbl.Branch, foreign); err != nil {
		t.Fatal(err)
	}
	as, err := w.svc.CheckRefs(w.ctx, w.slug())
	if err != nil || len(as) != 1 || as[0].Table != tbl.ID {
		t.Fatalf("alerts = %+v, %v", as, err)
	}
	if err := w.svc.SyncTower(w.ctx, w.slug(), tbl.ID, "torre-1"); !errors.Is(err, tables.ErrRefMoved) {
		t.Errorf("SyncTower = %v", err)
	}
}

// With the real git, a bundle of a Tower with the commits of another table is
// an alert and is not applied.
func TestRealATowerBundleWithTheCommitsOfAnotherTableIsNotApplied(t *testing.T) {
	t.Parallel()
	towerBundleOfAnotherTable(t, realBackend{})
}

// With the real git, the first worktree of a table on a Tower starts at the
// tip of the Citadel.
func TestRealTheFirstWorktreeOnATowerStartsAtTheTipOfTheCitadel(t *testing.T) {
	t.Parallel()
	replicaBranchScenario(t, realBackend{})
}

// CA-REPL-08 (the mirror of the workspace) with the real git, real bundles and
// a Tower with a table checked out in its replica.
func TestRealTheMirrorOfAWorkspaceReachesATowerWithATableCheckedOut(t *testing.T) {
	t.Parallel()
	mirrorScenario(t, realBackend{})
}
