package ghcli_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/internal/users"
)

type approveOpener struct{}

func (approveOpener) Request(context.Context, chain.OpenRequest) (int64, error) { return 1, nil }

// CA-APROV-POL-09: through the command `mestre gh approve 12` over the real
// Approver and the fake GitHub, with no approval standing for the PR at the
// current commit the Grão-Mestre gets human_required and no review is sent;
// once a user approved that commit, the review goes out.
func TestGhApproveByTheCommandSendsNoReviewWithoutAnApprovalAndOneWithTheApprovalOfAUser(t *testing.T) {
	t.Parallel()
	r, heads, pr := reviewRig(t)
	ctx := r.ctx
	if _, err := r.db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('meu-app', 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	svc, err := users.New(users.Options{DB: r.db, Clock: r.clk, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateOwner(ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	r.gh.SetPull(repoFull, pr, headA, false, "clean")
	r.gh.SetPullAuthor(repoFull, pr, "mestre-dev[bot]")
	heads.got[pr] = headA
	grand := &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(r.gh)},
		Token: func(context.Context, string) (string, error) { return "tok_gm", nil }}
	store := &chain.Store{DB: r.db}
	r.h.Approver = &approvals.Approver{Approved: store, Opener: approveOpener{}, Users: svc, Modes: &approvals.Modes{DB: r.db, Clock: r.clk},
		GrandMaster: func(context.Context, string) (approvals.ReviewClient, string, error) {
			return grand, "mestre-grao-mestre[bot]", nil
		}}
	reviews := func() int { return len(r.gh.ReviewsOf(repoFull, pr)) }

	got := r.run("gm", "gh approve", map[string]any{"reason": "testes verdes"}, itoa(pr))
	if got.OK || got.Code != "human_required" || reviews() != 0 {
		t.Fatalf("approve with no approval: %+v, %d reviews; want human_required and none sent", got, reviews())
	}
	// the approval of a user for this commit
	if _, err := store.Insert(ctx, chain.Request{Workspace: "meu-app", Table: "42-login", Type: classes.RequestMerge, State: chain.Approved, Level: chain.Level{Kind: chain.LevelPeople},
		PR: &chain.PRRef{Repo: repoFull, Number: pr, SHA: headA}, CreatedAt: r.clk.Now(), ClosedAt: r.clk.Now(),
		Decision: &chain.Closing{State: chain.Approved, By: "user:" + owner.ID, Answer: chain.AnswerApprove}}); err != nil {
		t.Fatal(err)
	}
	got = r.run("gm", "gh approve", map[string]any{"reason": "testes verdes"}, itoa(pr))
	if !got.OK || got.Data["head_sha"] != headA {
		t.Fatalf("approve with the approval of a user: %+v", got)
	}
	rs := r.gh.ReviewsOf(repoFull, pr)
	if len(rs) != 1 || rs[0].State != "APPROVED" || rs[0].Commit != headA || !strings.Contains(github.HideMarkers(rs[0].Body), "Lucas") {
		t.Fatalf("reviews = %+v; want one APPROVED on the commit naming the user", rs)
	}
}
