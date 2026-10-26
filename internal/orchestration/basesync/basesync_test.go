package basesync_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/ghcli"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/basesync"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

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
