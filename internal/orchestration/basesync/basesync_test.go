package basesync_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/ghcli"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/basesync"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// CA-SYNC-01: a table with a PR open, and the base moved: the engine makes the
// step sync for the Dev, the table goes to working and the users hear nothing.
func TestSyncStepForTheDevWhenTheBaseMoves(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.devWorks()
	if got := w.tableNow().State; got != tables.StateInReview {
		t.Fatalf("the table is %s before the base moves; want in_review", got)
	}
	w.moveBase("login.go") // the file the Dev changed: a conflict

	sq := w.syncQuests()
	if len(sq) != 1 || sq[0].Name != "sync" || sq[0].State != quests.Running {
		t.Fatalf("missions of the step sync = %+v; want one running", sq)
	}
	steps, err := w.orc.Quests.Store().Steps(w.ctx, sq[0].ID)
	if err != nil || len(steps) != 1 || steps[0].ID != "sync" {
		t.Fatalf("steps = %+v, %v", steps, err)
	}
	if steps[0].Character != dev.ID {
		t.Errorf("the step is for %q; want the Dev %q", steps[0].Character, dev.ID)
	}
	if got := w.tableNow().State; got != tables.StateWorking {
		t.Errorf("the table is %s; want working", got)
	}
	found := false
	for _, x := range w.itemsOf(dev.ID, inbox.KindQuest) {
		found = found || strings.Contains(x, "git merge origin/main") && strings.Contains(x, "Nunca use rebase")
	}
	if !found {
		t.Errorf("the Dev did not get the instruction of the merge: %q", w.itemsOf(dev.ID, inbox.KindQuest))
	}
	if told := w.users.Told(); len(told) != 0 {
		t.Errorf("the Owner was told %q; a conflict is a task for the Dev, not a decision", told)
	}
	for _, kind := range []inbox.Kind{inbox.KindAgent, inbox.KindApproval} {
		if got := w.itemsOf(w.gm.ID, kind); len(got) != 0 {
			t.Errorf("the Grand Master got %v items: %q", kind, got)
		}
	}
	if got := w.itemsOf(w.master.ID, inbox.KindSystem); len(got) == 0 {
		t.Error("the Master of the table was not told that the step sync was made")
	}
}

// prHandlers are the commands `mestre pr` of the Citadel with the gate of the
// base from the orchestration, over what the test does not need to have real.
func (w *world) prHandlers() {
	w.t.Helper()
	h := &ghcli.Handlers{
		Characters: w.orc.Registry, Clients: stubClients{}, Repos: stubRepos{}, Tables: stubTables{key: w.slug + "/" + w.table.ID}, PRs: stubPRs{},
		Objects: &github.Objects{}, Lease: stubLease{}, Clock: w.clk, Send: github.Sending{Base: w.orc.BaseSync},
	}
	h.Register(w.fwd)
}

// CA-SYNC-04: the tip of the base changed by a fetch of the mirror, no event of
// GitHub; `mestre pr open` leaves with 7 conflict and the step sync is there;
// with one open, a new signal is ignored and no second step is born.
func TestPROpenIsRefusedWithConflictAndTheSyncIsMade(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.devWorks()
	w.prHandlers()
	w.orc.Workspaces.OnFetched(nil) // no fetch hook: `pr open` is what notices
	w.git.Handle(w.remote).Data().Commit("main", "feat: other", map[string]string{"other.go": "x\n"})
	if err := w.orc.Workspaces.Fetch(w.ctx, w.slug); err != nil {
		t.Fatal(err)
	}
	if got := len(w.syncQuests()); got != 0 {
		t.Fatalf("missions of sync before `pr open`: %d", got)
	}
	open := func() localchan.Response {
		return w.call(w.master.ID, "pr open", map[string]any{"title": "Login"})
	}
	r := open()
	if r.OK || r.Error == nil || r.Error.Code != "conflict" || r.ExitCode() != 7 {
		t.Fatalf("pr open = %+v; want exit 7 conflict", r)
	}
	if !strings.Contains(r.Error.Message, "sync") {
		t.Errorf("the refusal does not say the step sync: %q", r.Error.Message)
	}
	if got := len(w.syncQuests()); got != 1 {
		t.Fatalf("missions of sync = %d; want 1", got)
	}
	// the second signal, from a fetch or from the Master asking again
	if r := open(); r.OK || r.Error == nil || r.Error.Code != "conflict" {
		t.Errorf("a second pr open = %+v; want conflict again", r)
	}
	w.orc.BaseSync.AfterFetch(w.ctx, w.slug)
	if got := len(w.syncQuests()); got != 1 {
		t.Errorf("missions of sync = %d after the new signals; want still 1", got)
	}
}

// The gate by itself, without a fetch hook before it: the first signal makes
// the step.
func TestGateMakesTheStepWhenTheFetchWasNotTold(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.devWorks()
	w.orc.Workspaces.OnFetched(nil)
	w.moveBase("a.go")
	if got := len(w.syncQuests()); got != 0 {
		t.Fatalf("a mission of sync before any signal: %d", got)
	}
	var moved *github.BaseMovedError
	if err := w.orc.BaseSync.Check(w.ctx, w.slug+"/"+w.table.ID); !errors.As(err, &moved) {
		t.Fatalf("the gate said %v; want the base moved", err)
	}
	if got := len(w.syncQuests()); got != 1 {
		t.Errorf("missions of sync = %d; want 1", got)
	}
}

// A table whose branch already has the tip of the base is not touched, and a
// base that is behind the branch is no news.
func TestNothingToDoWhenTheBranchHasTheBase(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.devWorks()
	r, err := w.orc.BaseSync.Ensure(w.ctx, w.slug, w.table.ID)
	if err != nil || r.Outcome != basesync.UpToDate {
		t.Fatalf("Ensure = %+v, %v; want up to date", r, err)
	}
	if got := len(w.syncQuests()); got != 0 {
		t.Errorf("missions of sync = %d; want 0", got)
	}
}

// Without a Dev alive that changed code, the Master of the table is asked to
// choose, once for the same tip of the base, and the gate still refuses.
func TestNoDevAsksTheMasterOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.moveBase("a.go")
	if _, err := w.orc.Tables.PROpened(w.ctx, w.slug, w.table.ID, 7, "head"); err != nil {
		t.Fatal(err)
	}
	r, err := w.orc.BaseSync.Ensure(w.ctx, w.slug, w.table.ID)
	if err != nil || r.Outcome != basesync.NoDev {
		t.Fatalf("Ensure = %+v, %v; want no dev", r, err)
	}
	if _, err := w.orc.BaseSync.Ensure(w.ctx, w.slug, w.table.ID); err != nil {
		t.Fatal(err)
	}
	asks := 0
	for _, x := range w.itemsOf(w.master.ID, inbox.KindSystem) {
		if strings.Contains(x, "Escolha quem integra a base") {
			asks++
		}
	}
	if asks != 1 {
		t.Errorf("the Master was asked %d times; want 1", asks)
	}
	var moved *github.BaseMovedError
	if err := w.orc.BaseSync.Check(w.ctx, w.slug+"/"+w.table.ID); !errors.As(err, &moved) {
		t.Errorf("the gate said %v; want the base moved", err)
	}
}

// A paused table does not get a step, and the gate still refuses (fail closed).
func TestPausedTableGetsNoStepButTheGateRefuses(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.devWorks()
	w.orc.Workspaces.OnFetched(nil)
	w.moveBase("a.go")
	if err := w.orc.Tables.Pause(w.ctx, w.slug, w.table.ID, tables.PauseRequest{By: "engine", Reason: "teste"}); err != nil {
		t.Fatal(err)
	}
	var moved *github.BaseMovedError
	if err := w.orc.BaseSync.Check(w.ctx, w.slug+"/"+w.table.ID); !errors.As(err, &moved) {
		t.Fatalf("the gate said %v; want the base moved", err)
	}
	if got := len(w.syncQuests()); got != 0 {
		t.Errorf("a paused table got %d missions of sync", got)
	}
}

type stubClients struct{}

func (stubClients) For(context.Context, characters.Character, string) (github.PullAPI, error) {
	return nil, nil
}

type stubRepos struct{}

func (stubRepos) Repo(context.Context, string) (string, error) { return "lucas/meu-app", nil }

type stubTables struct{ key string }

func (s stubTables) Epic(_ context.Context, table string) (github.TableRef, bool, error) {
	return github.TableRef{Repo: "lucas/meu-app", Epic: 42}, table == s.key, nil
}

type stubPRs struct{}

func (stubPRs) PRNumber(context.Context, string) (int, bool, error) { return 0, false, nil }

type stubLease struct{}

func (stubLease) Allow() error { return nil }

func (stubLease) Do(ctx context.Context, _ string, fn func(context.Context) error) (bool, error) {
	return false, fn(ctx)
}
