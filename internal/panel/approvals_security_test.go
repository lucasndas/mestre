package panel_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/panel"
)

func secretToken() string { return "ghp_" + strings.Repeat("a1B2", 9) }

// CA-CONV-08 (the text of the request): a token in the command of the request and in the
// text of the agent reaches neither the central, nor "Ver detalhes", nor the history:
// the panel masks every text just before it leaves, whatever the request carried when
// it was made.
func TestATokenInARequestReachesNeitherTheCentralNorTheDetailsNorTheHistory(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	token := secretToken()
	r := a.open(reqOpt{command: "curl -H " + token + " https://x.test", agent: "uso o token " + token, risk: classes.RiskLow, pr: &chain.PRRef{Repo: "lucas/loja", Number: 7, SHA: "abc"}})
	// the target of the request is raw too
	r.Target = []byte(`{"command":"` + token + `"}`)
	if err := a.store.Save(a.ctx, r); err != nil {
		t.Fatal(err)
	}
	it := a.list(a.anaTok, "").Requests[0]
	if blob := it.Text + it.Agent + it.About + fmt.Sprint(it.Members); strings.Contains(blob, token) || !strings.Contains(it.Agent, "uso o token") {
		t.Fatalf("the central shows %q", blob)
	}
	w := a.call("GET", fmt.Sprintf("/api/approvals/%d/details", r.ID), nil, opts{cookie: a.anaTok})
	if w.Code != 200 || strings.Contains(w.Body.String(), token) || !strings.Contains(w.Body.String(), "Alvo exato") {
		t.Fatalf("details %d %s", w.Code, w.Body)
	}
	w = a.call("GET", fmt.Sprintf("/api/approvals/%d", r.ID), nil, opts{cookie: a.anaTok})
	if strings.Contains(w.Body.String(), token) {
		t.Fatalf("the item %s", w.Body)
	}
	// it ends with a reason that carries the token, and the history does not show it
	r, err := a.store.Get(a.ctx, r.ID)
	if err != nil {
		t.Fatal(err)
	}
	r.State, r.ClosedAt = chain.Denied, a.clk.Now()
	r.Decision = &chain.Closing{State: chain.Denied, By: "c9", Level: chain.Level{Kind: chain.LevelMesa, Agent: "c9"}, Answer: chain.AnswerDeny, Reason: "vazou " + token}
	if err := a.store.Save(a.ctx, r); err != nil {
		t.Fatal(err)
	}
	w = a.call("GET", "/api/approvals/history", nil, opts{cookie: a.ownerTok})
	if w.Code != 200 || strings.Contains(w.Body.String(), token) || !strings.Contains(w.Body.String(), "vazou") {
		t.Fatalf("history %d %s", w.Code, w.Body)
	}
}

// The diff is masked too.
func TestTheDiffOfAPRIsMasked(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	token := secretToken()
	src := &panel.ApprovalSource{Flow: a.flow, Mask: a.mask, Diff: func(context.Context, chain.Request) ([]panel.ApprovalDiffFile, error) {
		return []panel.ApprovalDiffFile{{Path: "a.go", Status: "modified", Patch: "+key = " + token}}, nil
	}}
	p, err := panel.New(panel.Options{DB: a.db, Clock: a.clk, Users: a.users, Alerts: a.al, Assets: a.assets, Machine: "m1", Approvals: src})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	a.panel = p
	r := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestPR, pr: &chain.PRRef{Repo: "lucas/loja", Number: 7, SHA: "abc"}})
	w := a.call("GET", fmt.Sprintf("/api/approvals/%d/details", r.ID), nil, opts{cookie: a.anaTok})
	if w.Code != 200 || strings.Contains(w.Body.String(), token) || !strings.Contains(w.Body.String(), "+key =") {
		t.Fatalf("details %d %s", w.Code, w.Body)
	}
}

// docs/aprovacoes/delegacao.md: Delegar on a request of risk medio makes a decree of
// policy, which an Administrator makes; on alto it is the Owner's alone.
func TestAnAdministratorDelegatesAMediumRiskRequestButNotAHighOne(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	medium := a.open(reqOpt{command: "npm install lodash", risk: classes.RiskMedium})
	if _, ok := answersOf(a.list(a.adminTok, "").Requests[0])["delegate"]; !ok {
		t.Fatal("the Administrator is not given Delegar on a medio request")
	}
	if _, ok := answersOf(a.list(a.anaTok, "").Requests[0])["delegate"]; ok {
		t.Fatal("an Approver is given Delegar")
	}
	a.wantErr(a.answer(a.anaTok, medium.ID, panel.ApprovalAnswerRequest{Answer: "delegate"}, "654321"), 403, panel.CodeForbiddenLevel)
	w := a.answer(a.adminTok, medium.ID, panel.ApprovalAnswerRequest{Answer: "delegate"}, "654321")
	if w.Code != 200 || decodeInto[panel.ApprovalAnswered](t, w).State != "approved" {
		t.Fatalf("delegate: %d %s", w.Code, w.Body)
	}
	high := a.open(reqOpt{command: "npm install lodash", risk: classes.RiskHigh})
	a.wantErr(a.answer(a.adminTok, high.ID, panel.ApprovalAnswerRequest{Answer: "delegate"}, "654321"), 403, panel.CodeForbiddenLevel)
}

// The server checks again what the screen gives: an answer that does not exist for the
// type of the request is refused even for a level that has the capability.
func TestAnAnswerTheTypeOfTheRequestDoesNotHaveIsRefusedByTheServer(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	cost := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestExtraCost})
	for _, body := range []panel.ApprovalAnswerRequest{{Answer: "approve_always"}, {Answer: "approve_pattern", Pattern: "npm *"}, {Answer: "approve_for", For: "3h"}} {
		a.wantErr(a.answer(a.ownerTok, cost.ID, body, "123456"), 422, "approval.answer_not_offered")
	}
	low := a.open(reqOpt{risk: classes.RiskLow, typ: classes.RequestExtraCost})
	a.wantErr(a.answer(a.ownerTok, low.ID, panel.ApprovalAnswerRequest{Answer: "delegate"}, "123456"), 422, "approval.answer_not_offered")
	if a.stateOf(cost.ID) != chain.Open || a.stateOf(low.ID) != chain.Open {
		t.Fatal("a request changed")
	}
}

// A request of the engine (no workspace) is for who acts in "": the Owner, as on the
// Telegram. An Administrator does not see it in the central nor by its number.
func TestARequestWithNoWorkspaceIsOnlyTheOwners(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	sys := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestExtraCost})
	sys.Workspace = ""
	if _, err := a.db.Writer().ExecContext(a.ctx, `UPDATE approval_requests SET workspace = '' WHERE id = ?`, sys.ID); err != nil {
		t.Fatal(err)
	}
	if err := a.store.Save(a.ctx, sys); err != nil {
		t.Fatal(err)
	}
	if got := a.list(a.adminTok, ""); len(got.Requests) != 0 {
		t.Fatalf("the Administrator sees %+v", got)
	}
	a.wantErr(a.call("GET", fmt.Sprintf("/api/approvals/%d", sys.ID), nil, opts{cookie: a.adminTok}), 404, panel.CodeNotFound)
	if got := a.list(a.ownerTok, ""); len(got.Requests) != 1 {
		t.Fatalf("the Owner sees %+v", got)
	}
}

// CA-CONV-08 (the suggested pattern): a pattern built from a command that carries a token
// is not suggested at all, so the central never offers the secret as the text to approve.
func TestAPatternThatCarriesATokenIsNotSuggested(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	token := secretToken()
	a.open(reqOpt{command: "deploy " + token + " now"})
	it := a.list(a.adminTok, "").Requests[0]
	if pat := answersOf(it)["approve_pattern"]; pat.Suggested != "" || strings.Contains(fmt.Sprint(it), token) {
		t.Fatalf("the pattern answer is %+v", pat)
	}
	// a clean command still gets its suggestion
	b := newApprRig(t)
	b.open(reqOpt{command: "npm install lodash"})
	if s := answersOf(b.list(b.adminTok, "").Requests[0])["approve_pattern"].Suggested; s != "npm install *" {
		t.Errorf("the clean command is suggested %q", s)
	}
}
