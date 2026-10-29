package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/progress"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
)

type noRequests struct{}

func (noRequests) OpenOfTable(context.Context, string, string) ([]chain.Request, error) {
	return nil, nil
}

type oneWorkspace string

func (w oneWorkspace) Active(context.Context) ([]string, error) { return []string{string(w)}, nil }

type noPeople struct{}

func (noPeople) Recipients(context.Context, string) ([]string, error) { return nil, nil }

type noSender struct{}

func (noSender) Send(context.Context, string, string, i18n.Msg) error { return nil }

// Part of CA-GM-15 (the second half, with the real engine; the first is in internal/orchestration/progress): a `pr_mergeado` whose
// commit is not in the base of the mirror (read by the real poller over the
// double of GitHub) does not mark the table merged, so the message of progress
// does not count it; once the commit is in the base, the table is marked and the
// message counts it.
func TestAMergedPRWithoutTheCommitInTheBaseIsNotCountedAsMergedInTheProgressMessage(t *testing.T) {
	t.Parallel()
	c := newGHCompose(t)
	poll, err := c.run.poller(c.orc)
	if err != nil {
		t.Fatal(err)
	}
	prog, err := progress.New(progress.Options{Clock: c.clk, Tables: tables.Store{DB: c.db}, Requests: noRequests{}, Workspaces: oneWorkspace(c.ws.Slug), Recipients: noPeople{}, Sender: noSender{}})
	if err != nil {
		t.Fatal(err)
	}
	const repo = "lucas/meu_app"
	compose := func() string {
		t.Helper()
		m, ok, err := prog.Compose(c.ctx, c.ws.Slug)
		if err != nil || !ok {
			t.Fatalf("Compose = %v, %v", ok, err)
		}
		return i18n.Render(i18n.PtBR, m.Msg)
	}

	// merged on GitHub (the merge commit is on the remote), but not in the mirror yet
	merge := c.remote.Handle(c.rdir).Data().Commit("main", "feat: login (merge)", map[string]string{"login.txt": "x\n"})
	c.clk.Advance(time.Minute)
	c.gh.SetPullMerge(repo, c.pr, string(merge))
	c.gh.SetPullState(repo, c.pr, "closed", true, "")
	if err := poll.Tick(c.ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := c.orc.Tables.Get(c.ctx, c.ws.Slug, c.table.ID); err != nil || got.Merged {
		t.Fatalf("table merged = %v (%v) with the commit out of the base; want it unmarked", got.Merged, err)
	}
	if text := compose(); strings.Contains(text, "mescladas") || strings.Contains(text, "mesclada ") {
		t.Errorf("the table without the commit in the base was counted as merged:\n%s", text)
	}

	// the mirror fetches: the commit is in the base and the same PR now counts
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
	if got, err := c.orc.Tables.Get(c.ctx, c.ws.Slug, c.table.ID); err != nil || !got.Merged {
		t.Fatalf("table merged = %v (%v) with the commit in the base; want it marked", got.Merged, err)
	}
	if text := compose(); !strings.Contains(text, "1 mesclada") {
		t.Errorf("the table with the commit in the base was not counted as merged:\n%s", text)
	}
}
