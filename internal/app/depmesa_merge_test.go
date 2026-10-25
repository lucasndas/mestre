package app

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/epicdeps"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// CA-DEPMESA-02: the wait of a table for another is released only by a merge
// whose commit is in the base. A `pr_mergeado` of GitHub (read by the real
// poller) whose commit is not in the mirror yet leaves the wait active and tells
// nobody; once the commit is in the base the wait becomes satisfied, the table
// is released and the Grand Master receives a wake that cites both epics.
func TestAMergeWithoutTheCommitInTheBaseKeepsTheWaitActiveAndWithItTheWaitIsSatisfiedAndTheGrandMasterWoken(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	poll, err := c.run.poller(c.orc)
	if err != nil {
		t.Fatal(err)
	}
	gm, _, err := c.orc.Registry.GrandMaster(c.ctx, c.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	const repo = "lucas/meu_app"
	on := c.table.Epic // the epic whose table is in review, with its PR
	dependent := c.gh.AddIssue(repo, "Checkout", "epico")
	if _, err := c.orc.EpicDeps.Depend(c.ctx, gm, dependent, on); err != nil {
		t.Fatal(err)
	}
	opened, err := c.orc.Tables.Open(c.ctx, tables.OpenRequest{By: gm, Epic: dependent})
	if err != nil {
		t.Fatal(err)
	}
	active := func() []waits.Wait {
		t.Helper()
		all, err := (&waits.Store{DB: c.db}).ActiveWaits(c.ctx)
		if err != nil {
			t.Fatal(err)
		}
		var out []waits.Wait
		for _, w := range all {
			if w.Kind == waits.KindEpic && w.Table == opened.Table.ID {
				out = append(out, w)
			}
		}
		return out
	}
	held := func() bool {
		t.Helper()
		h, err := c.orc.EpicDeps.Held(c.ctx, c.ws.Slug, dependent)
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	woken := func() bool {
		t.Helper()
		items, err := c.orc.Inbox.Store().All(c.ctx, gm.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			if strings.Contains(it.Text, "pode seguir") {
				return true
			}
		}
		return false
	}
	if !held() || len(active()) != 1 {
		t.Fatalf("before the merge: held=%v, %d active waits; want held and one wait", held(), len(active()))
	}

	waitID := active()[0].ID

	// merged on GitHub; the merge commit is on the remote but not in the mirror yet
	merge := c.remote.Handle(c.rdir).Data().Commit("main", "feat: login (merge)", map[string]string{"login.txt": "x\n"})
	c.clk.Advance(time.Minute)
	c.gh.SetPullMerge(repo, c.pr, string(merge))
	c.gh.SetPullState(repo, c.pr, "closed", true, "")
	if err := poll.Tick(c.ctx); err != nil {
		t.Fatal(err)
	}
	if !held() || len(active()) != 1 || woken() {
		t.Fatalf("merge without the commit in the base: held=%v, %d active waits, woken=%v; want held, the wait active and no wake", held(), len(active()), woken())
	}

	// the mirror fetches: the commit is in the base
	mirror, err := c.orc.Workspaces.OpenMirror(c.ctx, c.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if err := mirror.Fetch(c.ctx, "origin", workspaces.FetchRefspec); err != nil {
		t.Fatal(err)
	}
	c.clk.Advance(10 * time.Minute)
	if err := poll.Tick(c.ctx); err != nil {
		t.Fatal(err)
	}
	if held() || len(active()) != 0 {
		t.Fatalf("merge with the commit in the base: held=%v, %d active waits; want released and satisfied", held(), len(active()))
	}
	// the wait ended as satisfied (not cancelled or released) and so did the dependency
	end, err := (&waits.Store{DB: c.db}).GetWait(c.ctx, waitID)
	if err != nil {
		t.Fatal(err)
	}
	if end.State != waits.Satisfied {
		t.Errorf("the wait ended as %q, want %q", end.State, waits.Satisfied)
	}
	dep, ok, err := (epicdeps.Store{DB: c.db}).Get(c.ctx, c.ws.Slug, dependent, on)
	if err != nil || !ok || dep.State != epicdeps.Satisfied {
		t.Errorf("the dependency = %+v (found %v, err %v), want state %q", dep, ok, err, epicdeps.Satisfied)
	}
	items, err := c.orc.Inbox.Store().All(c.ctx, gm.ID)
	if err != nil {
		t.Fatal(err)
	}
	cited := false
	for _, it := range items {
		cited = cited || strings.Contains(it.Text, "pode seguir") && strings.Contains(it.Text, "#"+strconv.Itoa(on)) && strings.Contains(it.Text, "#"+strconv.Itoa(dependent))
	}
	if !cited {
		t.Errorf("the Grand Master got no wake that cites #%d and #%d", on, dependent)
	}
}
