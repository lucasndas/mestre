package ghcli_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/ghcli"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
)

const (
	headA = "aaaabbbbccccddddeeeeffff00001111aaaabbbb"
	headB = "bbbbccccddddeeeeffff00001111aaaabbbbcccc"
)

// approverFake is the Approver: it keeps the order it got and answers what the test says.
type approverFake struct {
	mu    sync.Mutex
	got   []approvals.ApproveOrder
	reply error
	as    approvals.Approval
}

func (a *approverFake) Approve(_ context.Context, o approvals.ApproveOrder) (approvals.Approval, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.got = append(a.got, o)
	return a.as, a.reply
}

// mergesFake is the merge of the run: the REST of the Grão-Mestre over the double, and the
// requests of the chain.
type mergesFake struct {
	r  *rig
	mu sync.Mutex
	// approved is the request approved for a commit and a base (0: none); open the one that waits.
	approved, open int64
	// stands are the arguments Standing was asked with.
	stands []string
	asked  []chain.OpenRequest
	askErr error
	// approveOnAsk makes the request approved at once, as a policy that lets the Grand Master decide.
	approveOnAsk bool
}

func (m *mergesFake) Merger(context.Context, string) (ghcli.PullMerger, error) {
	return &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(m.r.gh)},
		Token: func(context.Context, string) (string, error) { return "tok_gm", nil }}, nil
}

func (m *mergesFake) Standing(_ context.Context, ws, repo string, n int, sha, base string) (int64, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stands = append(m.stands, strings.Join([]string{ws, repo, itoa(n), sha, base}, "|"))
	return m.approved, m.open, nil
}

func (m *mergesFake) Ask(_ context.Context, o chain.OpenRequest) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.askErr != nil {
		return 0, m.askErr
	}
	m.asked = append(m.asked, o)
	if m.approveOnAsk {
		m.approved = 90
	} else {
		m.open = 90
	}
	return 90, nil
}

func (m *mergesFake) Table(_ context.Context, _, headRef string) string {
	if headRef == "42-login" {
		return "42-login"
	}
	return ""
}

// rulesetsFake is the settings of the Owner's approval.
type rulesetsFake struct {
	mu        sync.Mutex
	ws        []string
	proposers []string
	body      []map[string]any
	err       error
}

func (s *rulesetsFake) ProposeRuleset(_ context.Context, ws, proposer string, body map[string]any) (int64, error) {
	s.proposers = append(s.proposers, proposer)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.err != nil {
		return 0, s.err
	}
	s.ws, s.body = append(s.ws, ws), append(s.body, body)
	return 55, nil
}

func approveRig(t *testing.T) (*rig, *approverFake, *mergesFake, *rulesetsFake, int) {
	t.Helper()
	r, heads, pr := reviewRig(t)
	ap, mg, rs := &approverFake{as: approvals.Approval{As: approvals.AsGrandMaster, Review: 12}}, &mergesFake{r: r}, &rulesetsFake{}
	r.h.Approver, r.h.Merges, r.h.Rulesets = ap, mg, rs
	r.gh.SetPull(repoFull, pr, headA, false, "clean")
	heads.got[pr] = headA
	return r, ap, mg, rs, pr
}

// Part of CA-CLI-GM-04: `gh approve` is for the commit `gh review` read, in the workspace
// of who asks and the repository of that workspace, with the reason that becomes the
// review; the answers of the policy are told apart.
func TestGhApproveIsForTheCommitReadTheWorkspaceOfWhoAsksAndTellsTheRefusalsApart(t *testing.T) {
	t.Parallel()
	r, ap, _, _, pr := approveRig(t)
	// there is no flag to name the workspace or the repository
	for _, f := range []string{"workspace", "repo"} {
		if got := r.run("gm", "gh approve", map[string]any{"reason": "x", f: "outro"}, itoa(pr)); got.OK || got.Exit != 2 || len(ap.got) != 0 {
			t.Fatalf("approve with --%s: %+v", f, got)
		}
	}
	got := r.run("gm", "gh approve", map[string]any{"reason": "testes verdes"}, itoa(pr))
	if !got.OK || got.Data["head_sha"] != headA || got.Data["review"] != float64(12) {
		t.Fatalf("approve: %+v", got)
	}
	if len(ap.got) != 1 || ap.got[0].Workspace != "meu-app" || ap.got[0].Repo != repoFull || ap.got[0].SHA != headA || ap.got[0].Number != pr || ap.got[0].Reason != "testes verdes" {
		t.Errorf("the Approver got %+v; want the workspace and the repository of who asks, whatever the flags say", ap.got)
	}
	// without the reason
	if got = r.run("gm", "gh approve", nil, itoa(pr)); got.OK || got.Exit != 2 {
		t.Errorf("approve without a reason: %+v", got)
	}
	// the policy does not let the Grand Master approve alone
	ap.reply = &chain.Refusal{Code: chain.CodeHumanRequired, Message: "o PR precisa da aprovação de uma pessoa"}
	// even with --on-behalf, a refusal that is not about the delegation stays human_required
	for _, flags := range []map[string]any{{"reason": "x"}, {"reason": "x", "on-behalf": "Ana"}} {
		if got = r.run("gm", "gh approve", flags, itoa(pr)); got.OK || got.Code != "human_required" || got.Exit != 3 {
			t.Errorf("human required (%v): %+v", flags, got)
		}
	}
	ap.reply = &chain.Refusal{Code: approvals.CodeNoDelegation, Message: "Ana não delegou"}
	if got = r.run("gm", "gh approve", map[string]any{"reason": "x", "on-behalf": "Ana"}, itoa(pr)); got.OK || got.Code != "no_delegation" || got.Exit != 3 {
		t.Errorf("on behalf without a delegation: %+v", got)
	}
	ap.reply = approvals.ErrHeadChanged
	if got = r.run("gm", "gh approve", map[string]any{"reason": "x"}, itoa(pr)); got.OK || got.Code != "head_changed" || got.Exit != 7 {
		t.Errorf("head changed: %+v", got)
	}
	ap.reply = approvals.ErrPRClosed
	if got = r.run("gm", "gh approve", map[string]any{"reason": "x"}, itoa(pr)); got.OK || got.Code != "not_open" || got.Exit != 7 {
		t.Errorf("closed: %+v", got)
	}
	// a pull request that was never read
	other := r.gh.AddPR(repoFull, "feat: outra", "outra", repoFull)
	ap.reply = nil
	n := len(ap.got)
	if got = r.run("gm", "gh approve", map[string]any{"reason": "x"}, itoa(other)); got.OK || got.Code != "head_changed" || len(ap.got) != n {
		t.Errorf("approve of a PR that was not read: %+v (%d calls)", got, len(ap.got)-n)
	}
	// only the Grand Master
	if got = r.run("tm", "gh approve", map[string]any{"reason": "x"}, itoa(pr)); got.OK || got.Exit != 3 {
		t.Errorf("approve by the Mestre da mesa: %+v", got)
	}
}

// `gh merge` opens the request of the merge when none is approved, bound
// to the PR, the commit and the base, and waits (9); once approved it merges with the
// commit that was approved and the method asked.
func TestGhMergeAsksFirstAndThenMergesTheApprovedCommitWithTheMethod(t *testing.T) {
	t.Parallel()
	r, _, mg, _, pr := approveRig(t)
	got := r.run("gm", "gh merge", map[string]any{"method": "squash"}, itoa(pr))
	if got.OK || got.Code != "approval_pending" || got.Exit != 9 {
		t.Fatalf("merge without an approved request: %+v", got)
	}
	if len(mg.asked) != 1 || mg.asked[0].Type != classes.RequestMerge || mg.asked[0].Workspace != "meu-app" || mg.asked[0].Table != "42-login" || mg.asked[0].Requester != "" || mg.asked[0].Proposer != "gm" ||
		mg.asked[0].PR == nil || mg.asked[0].PR.Number != pr || mg.asked[0].PR.SHA != headA || string(mg.asked[0].Target) != string(approvals.PRTarget(repoFull, pr, headA, "main")) {
		t.Fatalf("the request = %+v", mg.asked)
	}
	if len(r.gh.Merges(repoFull)) != 0 {
		t.Fatal("merged before the approval")
	}
	// the same command again does not open another request: it waits for the one that is open
	if got = r.run("gm", "gh merge", map[string]any{"method": "squash"}, itoa(pr)); got.OK || got.Code != "approval_pending" || len(mg.asked) != 1 || !strings.Contains(got.Error, "#90") {
		t.Fatalf("the command again: %+v with %d requests; want the same one waiting", got, len(mg.asked))
	}
	if want := "meu-app|" + repoFull + "|" + itoa(pr) + "|" + headA + "|main"; mg.stands[0] != want {
		t.Errorf("the approvals were looked up with %q; want %q (workspace of who asks, repository of the workspace, commit and base of the PR)", mg.stands[0], want)
	}
	mg.approved = 90 // the people approved
	got = r.run("gm", "gh merge", map[string]any{"method": "squash"}, itoa(pr))
	if !got.OK || got.Data["merged"] != true || got.Data["method"] != "squash" || got.Data["head_sha"] != headA {
		t.Fatalf("merge: %+v", got)
	}
	if ms := r.gh.Merges(repoFull); len(ms) != 1 || ms[0].SHA != headA || ms[0].Method != "squash" || ms[0].Number != pr {
		t.Errorf("GitHub merged %+v; want the approved commit with squash", ms)
	}
	// a PR from a fork whose branch has the name of a table is not of that table
	fork := r.gh.AddPR(repoFull, "feat: fork", "42-login", "someone/fork")
	r.gh.SetPull(repoFull, fork, headB, false, "clean")
	mg.approved, mg.open = 0, 0
	if got = r.run("gm", "gh merge", nil, itoa(fork)); got.OK || got.Code != "approval_pending" || mg.asked[len(mg.asked)-1].Table != "" {
		t.Errorf("merge of a PR from a fork: %+v; want a request with no table", got)
	}
	// a policy that approves at once: ask and merge in one command
	r2, _, mg2, _, pr2 := approveRig(t)
	mg2.approveOnAsk = true
	if got = r2.run("gm", "gh merge", nil, itoa(pr2)); !got.OK || len(mg2.asked) != 1 || len(r2.gh.Merges(repoFull)) != 1 {
		t.Errorf("merge approved at once: %+v", got)
	}
}

// The refusals of the merge are told apart and nothing is worked around.
func TestGhMergeTellsApartWhatGitHubRefusesAndWhatTheChainRefuses(t *testing.T) {
	t.Parallel()
	r, _, mg, _, pr := approveRig(t)
	mg.approved = 90
	for _, c := range []struct {
		state string
		draft bool
		code  string
		exit  int
	}{{"blocked", false, "protected", 3}, {"dirty", false, "not_mergeable", 7}, {"behind", false, "not_mergeable", 7}, {"clean", true, "not_mergeable", 7}} {
		r.gh.SetPull(repoFull, pr, headA, c.draft, c.state)
		if got := r.run("gm", "gh merge", nil, itoa(pr)); got.OK || got.Code != c.code || got.Exit != c.exit {
			t.Errorf("state %q draft %v: %+v; want %s (%d)", c.state, c.draft, got, c.code, c.exit)
		}
	}
	r.gh.SetPull(repoFull, pr, headA, false, "clean")
	r.gh.DisallowMergeMethod(repoFull, "merge")
	if got := r.run("gm", "gh merge", nil, itoa(pr)); got.OK || got.Code != "not_mergeable" || !strings.Contains(got.Error, "allow") {
		t.Errorf("method not allowed: %+v", got)
	}
	r.gh.FailMerges(repoFull, 403, "Resource not accessible by integration", nil)
	if got := r.run("gm", "gh merge", nil, itoa(pr)); got.OK || got.Code != "forbidden" || got.Exit != 3 {
		t.Errorf("permission: %+v", got)
	}
	r.gh.FailMerges(repoFull, 0, "", nil)
	if got := r.run("gm", "gh merge", map[string]any{"method": "fast-forward"}, itoa(pr)); got.OK || got.Exit != 2 {
		t.Errorf("a method that does not exist: %+v", got)
	}
	if len(r.gh.Merges(repoFull)) != 0 {
		t.Fatal("something was merged although every call was refused")
	}
	// the chain says the people decide: nothing is merged
	mg.approved = 0
	mg.askErr = &chain.Refusal{Code: chain.CodeHumanRequired, Message: "o gate de frescor não passou"}
	if got := r.run("gm", "gh merge", nil, itoa(pr)); got.OK || got.Code != "human_required" || got.Exit != 3 {
		t.Errorf("the chain refuses: %+v", got)
	}
	for _, c := range []struct {
		err  error
		code string
		exit int
	}{
		{&chain.FreshnessError{Reason: "a base andou e a verificação falhou"}, "not_mergeable", 7},
		{chain.ErrWrongRepo, "wrong_repo", 3},
		{errors.New("chain: the gate of freshness could not be checked: the request has no table"), "no_table", 3},
		{&chain.Refusal{Code: "above_ceiling", Message: "acima do teto"}, "above_ceiling", 3},
	} {
		mg.askErr = c.err
		if got := r.run("gm", "gh merge", nil, itoa(pr)); got.OK || got.Code != c.code || got.Exit != c.exit {
			t.Errorf("the chain answers %v: %+v; want %s (%d)", c.err, got, c.code, c.exit)
		}
	}
	mg.askErr = errors.New("boom")
	if got := r.run("gm", "gh merge", nil, itoa(pr)); got.OK {
		t.Errorf("an error of the chain: %+v", got)
	}
	// a PR that does not exist, one that is merged, only the Grand Master
	if got := r.run("gm", "gh merge", nil, "999"); got.OK || got.Code != "not_found" {
		t.Errorf("a PR that does not exist: %+v", got)
	}
	mg.approved = 90
	r.gh.SetPullState(repoFull, pr, "closed", true, "")
	if got := r.run("gm", "gh merge", nil, itoa(pr)); got.OK || got.Code != "not_open" || got.Exit != 7 {
		t.Errorf("a PR merged already: %+v", got)
	}
	if got := r.run("tm", "gh merge", nil, itoa(pr)); got.OK || got.Exit != 3 {
		t.Errorf("merge by the Mestre da mesa: %+v", got)
	}
}

// Part of CA-CLI-GM-05: `gh settings apply` changes nothing: it asks the Owner for the
// ruleset, in the workspace of who asks, and ends pending; a body that is not a ruleset
// is a usage error; only the Grand Master.
func TestGhSettingsApplyAsksTheOwnerAndEndsPending(t *testing.T) {
	t.Parallel()
	r, _, _, rs, _ := approveRig(t)
	body := `{"name":"mestre","target":"branch","enforcement":"active","rules":[{"type":"pull_request"}]}`
	if got := r.run("gm", "gh settings", map[string]any{"body": body, "workspace": "outro"}, "apply"); got.OK || got.Exit != 2 || len(rs.ws) != 0 {
		t.Fatalf("apply with --workspace: %+v", got)
	}
	got := r.run("gm", "gh settings", map[string]any{"body": body}, "apply")
	if got.OK || got.Code != "approval_pending" || got.Exit != 9 || !strings.Contains(got.Error, "#55") {
		t.Fatalf("apply: %+v", got)
	}
	if len(rs.proposers) != 1 || rs.proposers[0] != "gm" {
		t.Errorf("the proposer is %v; want the Grand Master, who decides nothing of the request", rs.proposers)
	}
	if len(rs.ws) != 1 || rs.ws[0] != "meu-app" || rs.body[0]["name"] != "mestre" {
		t.Errorf("the request was made for %v %v; want the workspace of who asks and the body", rs.ws, rs.body)
	}
	for _, bad := range []string{"", "not json", "[1]", "null"} {
		if got = r.run("gm", "gh settings", map[string]any{"body": bad}, "apply"); got.OK || got.Exit != 2 {
			t.Errorf("body %q: %+v", bad, got)
		}
	}
	rs.err = approvals.ErrBadRuleset
	if got = r.run("gm", "gh settings", map[string]any{"body": `{"x":1}`}, "apply"); got.OK || got.Exit != 2 {
		t.Errorf("a body that is not a ruleset: %+v", got)
	}
	if got = r.run("tm", "gh settings", map[string]any{"body": body}, "apply"); got.OK || got.Exit != 3 {
		t.Errorf("apply by the Mestre da mesa: %+v", got)
	}
}
