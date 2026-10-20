package approvals_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
)

// The engine rule behind CA-APROV-POL-09 (the criterion, through the command
// mestre gh approve, is in internal/ghcli/approve_flow_test.go).
func TestGhApproveOnlyMakesTheReviewWithAnApprovalThatStandsForTheCommit(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	a := l.approver(nil)
	// no approval for the PR: human_required and nothing sent
	if _, err := a.Approve(l.ctx, l.order()); !isHumanRequired(err) {
		t.Fatalf("Approve with no approval = %v; want human_required", err)
	}
	// an approval of another commit does not describe this one
	l.approved("user:"+l.owner.ID, strings.Repeat("b", 40))
	if _, err := a.Approve(l.ctx, l.order()); !isHumanRequired(err) {
		t.Fatalf("Approve with the approval of another commit = %v; want human_required", err)
	}
	if got := l.sent(); len(got) != 0 {
		t.Fatalf("reviews sent without an approval: %+v", got)
	}
	// the approval of a user for this commit: the review of the App goes out, naming her
	r := l.approved("user:"+l.owner.ID, l.sha)
	got, err := a.Approve(l.ctx, l.order())
	if err != nil || got.As != approvals.AsGrandMaster || got.Review == 0 {
		t.Fatalf("Approve = %+v, %v; want a review of the Grão-Mestre", got, err)
	}
	sent := l.sent()
	want := "Aprovado pelo Grão-Mestre após aprovação de Lucas (pedido " + strconv.FormatInt(r.ID, 10) + ")"
	if len(sent) != 1 || sent[0].By != "mestre-grao-mestre[bot]" || sent[0].State != "APPROVED" || sent[0].Commit != l.sha || sent[0].Body != want {
		t.Fatalf("reviews = %+v; want one APPROVED on %s by the App with %q", sent, l.sha, want)
	}
}

func TestGhApproveWithTheApprovalOfTheGrandMasterUsesHisReason(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	l.approved("character-gm", l.sha)
	if _, err := l.approver(nil).Approve(l.ctx, l.order()); err != nil {
		t.Fatal(err)
	}
	if sent := l.sent(); len(sent) != 1 || sent[0].Body != "tudo verde e a mudança é pequena" {
		t.Fatalf("reviews = %+v; want his reason as the text", sent)
	}
}

func TestGhApproveRefusesAPRThatMovedOrClosed(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	l.approved("character-gm", l.sha)
	a := l.approver(nil)
	l.gh.SetPull("lucas/meu-app", l.pr, strings.Repeat("c", 40), false, "clean")
	if _, err := a.Approve(l.ctx, l.order()); !errors.Is(err, approvals.ErrHeadChanged) {
		t.Fatalf("Approve after a push = %v; want ErrHeadChanged", err)
	}
	l.gh.SetPull("lucas/meu-app", l.pr, l.sha, false, "clean")
	l.gh.SetPullState("lucas/meu-app", l.pr, "closed", false, "")
	if _, err := a.Approve(l.ctx, l.order()); !errors.Is(err, approvals.ErrPRClosed) {
		t.Fatalf("Approve of a closed PR = %v; want ErrPRClosed", err)
	}
	if got := l.sent(); len(got) != 0 {
		t.Fatalf("reviews sent: %+v", got)
	}
}

// In the mode of human approval the Grão-Mestre alone approves nothing; the
// approval of a person is applied as usual.
func TestGhApproveInTheModeOfHumanApprovalNeedsAPerson(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	if err := l.modes.Set(l.ctx, "loja", approvals.ModeHuman, "o GitHub não aceita rulesets sem Pro"); err != nil {
		t.Fatal(err)
	}
	a := l.approver(nil)
	l.approved("character-gm", l.sha)
	if _, err := a.Approve(l.ctx, l.order()); !isHumanRequired(err) {
		t.Fatalf("Approve alone in the human mode = %v; want human_required", err)
	}
	l.approved("user:"+l.owner.ID, l.sha)
	// the review of the App does not count here: the person reviews, or gets the link
	got, err := a.Approve(l.ctx, l.order())
	if err != nil || got.As != approvals.AsLink {
		t.Fatalf("Approve with the approval of a person = %+v, %v; want the link, not a review of the App", got, err)
	}
	if sent := l.sent(); len(sent) != 0 {
		t.Fatalf("a review of the App was sent in the human mode: %+v", sent)
	}
}

// A delegation never lets the Grão-Mestre approve a PR the App opened.
func TestADelegationDoesNotLetTheGrandMasterApproveItsOwnPR(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	marina := l.person("Marina", "marina", users.Approver)
	l.gh.IssueUserToken("ghu_marina", "marina")
	l.gh.SetPullAuthor("lucas/meu-app", l.pr, "mestre-grao-mestre[bot]")
	l.approved("character-gm", l.sha)
	a := l.approver(func(a *approvals.Approver) {
		a.Personal = credentials{clients: map[string]approvals.ReviewClient{marina.ID: l.client("ghu_marina")}}
		a.Delegations = &delegations{author: marina.ID}
	})
	order := l.order()
	order.OnBehalf = "marina"
	if _, err := a.Approve(l.ctx, order); !isHumanRequired(err) {
		t.Fatalf("Approve = %v; want human_required", err)
	}
	if sent := l.sent(); len(sent) != 0 {
		t.Fatalf("reviews sent: %+v", sent)
	}
}

// CA-GH-APROV-04
func TestAPRTheGrandMasterOpenedIsAlwaysForAPerson(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	l.gh.SetPullAuthor("lucas/meu-app", l.pr, "mestre-grao-mestre[bot]")
	// the facts the policy reads say who opened it
	src := approvals.GitHubSource{Reader: func(_ context.Context, _ string) (approvals.PullReader, error) { return l.grand, nil },
		GrandMasterLogin: func(context.Context) (string, error) { return "mestre-grao-mestre[bot]", nil }}
	l.gh.AddPullFile("lucas/meu-app", l.pr, "docs/a.md", "@@ -1 +1 @@\n-x\n+y\n")
	l.gh.AddCheck("lucas/meu-app", l.sha, "build", "success")
	facts, err := src.PullRequest(l.ctx, "loja", chain.PRRef{Repo: "lucas/meu-app", Number: l.pr, SHA: l.sha})
	if err != nil || !facts.OpenedByGrandMaster {
		t.Fatalf("facts = %+v, %v; want OpenedByGrandMaster", facts, err)
	}
	rules := chain.DefaultRules()
	req := chain.Request{Type: classes.RequestMerge, Risk: classes.RiskResult{Risk: classes.RiskMedium}}
	if why := rules.Conditions(req, facts, true); len(why) == 0 || !strings.Contains(strings.Join(why, ";"), "Grão-Mestre") {
		t.Fatalf("conditions = %q; want the Grão-Mestre as a reason for a person", why)
	}
	// the same PR by another App: the policy may let the Grão-Mestre alone
	l.gh.SetPullAuthor("lucas/meu-app", l.pr, "mestre-dev[bot]")
	facts, err = src.PullRequest(l.ctx, "loja", chain.PRRef{Repo: "lucas/meu-app", Number: l.pr, SHA: l.sha})
	if err != nil || facts.OpenedByGrandMaster {
		t.Fatalf("facts of another author = %+v, %v", facts, err)
	}
	// and the Approver itself does not make the review of the App on a PR the App opened
	l.gh.SetPullAuthor("lucas/meu-app", l.pr, "mestre-grao-mestre[bot]")
	l.approved("character-gm", l.sha)
	if _, err := l.approver(nil).Approve(l.ctx, l.order()); !isHumanRequired(err) {
		t.Fatalf("Approve of a PR the Grão-Mestre opened = %v; want human_required", err)
	}
	if got := l.sent(); len(got) != 0 {
		t.Fatalf("reviews sent: %+v", got)
	}
}

// CA-GH-APROV-05
func TestACodeOwnerIsNotApprovedForWithoutHerDelegation(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	marina := l.person("Marina", "marina", users.Approver)
	l.gh.IssueUserToken("ghu_marina", "marina")
	l.approved("character-gm", l.sha)
	a := l.approver(func(a *approvals.Approver) {
		a.Required = owners{logins: []string{"marina"}}
		a.Personal = credentials{clients: map[string]approvals.ReviewClient{marina.ID: l.client("ghu_marina")}}
		a.Delegations = &delegations{}
	})
	order := l.order()
	order.OnBehalf = "marina"
	_, err := a.Approve(l.ctx, order)
	if !isHumanRequired(err) {
		t.Fatalf("Approve on behalf without a decree = %v; want human_required", err)
	}
	var ref *chain.Refusal
	if !errors.As(err, &ref) || ref.Code != approvals.CodeNoDelegation {
		t.Fatalf("Approve on behalf without a decree = %v; want the code no_delegation", err)
	}
	if got := l.sent(); len(got) != 0 {
		t.Fatalf("a review was sent without the delegation: %+v", got)
	}
	made := l.open.all()
	if len(made) != 1 || made[0].Type != classes.RequestMerge || len(made[0].Require) != 1 || made[0].Require[0] != marina.ID || made[0].PR == nil || made[0].PR.SHA != l.sha {
		t.Fatalf("requests = %+v; want one for Marina on the commit", made)
	}
	// without --on-behalf the Grão-Mestre alone does not stand for the code owner either
	if _, err := a.Approve(l.ctx, l.order()); !isHumanRequired(err) {
		t.Fatalf("Approve for a PR that needs a code owner = %v; want human_required", err)
	}
	if got := l.sent(); len(got) != 0 {
		t.Fatalf("a review of the App was sent for a PR that needs a code owner: %+v", got)
	}
}

func TestACodeOwnerWhoDelegatedIsApprovedForByTheGrandMasterWithHerCredential(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	marina := l.person("Marina", "marina", users.Approver)
	l.gh.IssueUserToken("ghu_marina", "marina")
	r := l.approved("character-gm", l.sha)
	del := &delegations{author: marina.ID}
	a := l.approver(func(a *approvals.Approver) {
		a.Required = owners{logins: []string{"marina"}}
		a.Personal = credentials{clients: map[string]approvals.ReviewClient{marina.ID: l.client("ghu_marina")}}
		a.Delegations = del
	})
	order := l.order()
	order.OnBehalf = "marina"
	got, err := a.Approve(l.ctx, order)
	if err != nil || got.As != approvals.AsDelegate {
		t.Fatalf("Approve with the delegation = %+v, %v; want a review in her name", got, err)
	}
	sent := l.sent()
	if len(sent) != 1 || sent[0].By != "marina" || sent[0].Body != "Aprovado pelo Grão-Mestre em nome de Marina, por delegação" {
		t.Fatalf("reviews = %+v; want hers, by delegation", sent)
	}
	if len(del.used) != 1 || del.used[0] != r.ID {
		t.Fatalf("the decree was used for %v; want the request %d", del.used, r.ID)
	}
	// the decree of another person covers nothing of hers
	del.author = l.owner.ID
	if _, err := a.Approve(l.ctx, order); !isHumanRequired(err) {
		t.Fatalf("Approve with a decree of another user = %v; want human_required", err)
	}
}

func TestACodeOwnerWhoApprovedHerselfReviewsWithHerCredentialOrGetsTheLink(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	marina := l.person("Marina", "marina", users.Approver)
	l.gh.IssueUserToken("ghu_marina", "marina")
	l.approved("user:"+marina.ID, l.sha)
	req := func(a *approvals.Approver) { a.Required = owners{logins: []string{"marina"}} }
	// with a credential: the review is a real one of hers
	a := l.approver(func(a *approvals.Approver) {
		req(a)
		a.Personal = credentials{clients: map[string]approvals.ReviewClient{marina.ID: l.client("ghu_marina")}}
	})
	got, err := a.Approve(l.ctx, l.order())
	if err != nil || got.As != approvals.AsPerson {
		t.Fatalf("Approve = %+v, %v; want her review", got, err)
	}
	if sent := l.sent(); len(sent) != 1 || sent[0].By != "marina" || sent[0].State != "APPROVED" || !strings.Contains(sent[0].Body, "Marina") {
		t.Fatalf("reviews = %+v; want her APPROVED", sent)
	}
	// without one: she gets the link and the events conclude when she reviews
	l2 := newGHLab(t)
	marina2 := l2.person("Marina", "marina", users.Approver)
	l2.approved("user:"+marina2.ID, l2.sha)
	got, err = l2.approver(req).Approve(l2.ctx, l2.order())
	if err != nil || got.As != approvals.AsLink {
		t.Fatalf("Approve with no credential = %+v, %v; want the link", got, err)
	}
	said := l2.tell.said(marina2.ID)
	if len(said) != 1 || !strings.Contains(said[0], "https://github.com/lucas/meu-app/pull/") {
		t.Fatalf("told to Marina: %q; want the link of the PR", said)
	}
	if sent := l2.sent(); len(sent) != 0 {
		t.Fatalf("a review was sent for her: %+v", sent)
	}
}

// What GitHub refuses (a rule the review can not satisfy) goes to a person and
// is not hidden.
func TestAReviewGitHubRefusesIsHumanRequired(t *testing.T) {
	t.Parallel()
	l := newGHLab(t)
	l.approved("character-gm", l.sha)
	l.gh.SetPullAuthor("lucas/meu-app", l.pr, "mestre-grao-mestre[bot]")
	// the login of the App is not known to the engine: GitHub itself says no
	a := l.approver(func(a *approvals.Approver) {
		a.GrandMaster = func(context.Context, string) (approvals.ReviewClient, string, error) {
			return l.grand, "outro[bot]", nil
		}
	})
	if _, err := a.Approve(l.ctx, l.order()); !isHumanRequired(err) || !strings.Contains(err.Error(), "recusou") {
		t.Fatalf("Approve = %v; want human_required with the refusal of GitHub", err)
	}
}
