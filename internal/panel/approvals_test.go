package panel_test

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/notices"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/testkit/fakechannel"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// tchain is the chain of approval over the real store of the requests, which decides
// as the chain does for a person: it closes the request and has the flow take the
// question back (the flow tells the panel).
type tchain struct {
	st   *chain.Store
	clk  clock.Clock
	flow **approvals.Flow
}

func (c tchain) Get(ctx context.Context, id int64) (chain.Request, error) { return c.st.Get(ctx, id) }

func (c tchain) DecideByPerson(ctx context.Context, d chain.PersonDecision) (chain.Outcome, error) {
	r, err := c.st.Get(ctx, d.Request)
	if err != nil {
		return chain.Outcome{}, err
	}
	if d.Answer == chain.AnswerAskBack {
		return chain.Outcome{Request: d.Request, State: chain.Open, Applied: "asked_back"}, nil
	}
	state := chain.Denied
	if d.Answer.Approves() || d.Answer == chain.AnswerDelegate {
		state = chain.Approved
	}
	return c.close(ctx, r, state, "user:"+d.User, d.Answer)
}

func (c tchain) Invalidate(ctx context.Context, id int64, _ string) error {
	r, err := c.st.Get(ctx, id)
	if err != nil {
		return err
	}
	_, err = c.close(ctx, r, chain.Invalidated, "", "")
	return err
}

func (c tchain) close(ctx context.Context, r chain.Request, state chain.State, by string, a chain.Answer) (chain.Outcome, error) {
	r.State, r.ClosedAt = state, c.clk.Now()
	r.Decision = &chain.Closing{State: state, By: by, Level: r.Level, Answer: a}
	if err := c.st.Save(ctx, r); err != nil {
		return chain.Outcome{}, err
	}
	_ = (*c.flow).Withdraw(ctx, r.ID, string(state))
	return chain.Outcome{Request: r.ID, State: state}, nil
}

type apprRig struct {
	*rig
	store *chain.Store
	flow  *approvals.Flow
	feed  *notices.Feed
	blk   *blocking.Store
	mask  func(string) string
	// listener is what the panel registered with the flow.
	listener atomic.Pointer[func(context.Context, int64)]
	admin    users.User
	adminTok string
	ana      users.User
	anaTok   string
	obs      users.User
	obsTok   string
	ownerTok string
}

func newApprRig(t *testing.T) *apprRig {
	t.Helper()
	a := &apprRig{rig: newRig(t)}
	a.store = &chain.Store{DB: a.db}
	a.mask = logging.NewMasker().Mask
	var err error
	a.flow, err = approvals.New(approvals.Options{DB: a.db, Clock: a.clk, Chain: tchain{a.store, a.clk, &a.flow}, Users: a.users, PINs: a.users, Messenger: fakechannel.New(),
		Locked: func(context.Context) bool { return false },
		OnChange: func(ctx context.Context, id int64) {
			if fn := a.listener.Load(); fn != nil {
				(*fn)(ctx, id)
			}
		}})
	if err != nil {
		t.Fatal(err)
	}
	a.feed = &notices.Feed{DB: a.db, Clock: a.clk, Users: a.users, Mask: a.mask}
	a.blk = &blocking.Store{DB: a.db, Clock: a.clk}
	a.panel, err = panel.New(panel.Options{DB: a.db, Clock: a.clk, Users: a.users, Alerts: a.al, Assets: a.assets, Machine: "m1",
		Approvals: &panel.ApprovalSource{Flow: a.flow, Blocking: a.blk, Mask: a.mask, Listen: func(fn func(context.Context, int64)) func() {
			a.listener.Store(&fn)
			return func() { a.listener.Store(nil) }
		}},
		Notices: &panel.NoticeSource{Feed: a.feed}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.panel.Close)
	a.ownerTok = a.ownerSession()
	a.admin, a.adminTok = a.member("Bia", "bia", users.Administrator)
	a.ana, a.anaTok = a.member("Ana", "ana", users.Approver)
	a.obs, a.obsTok = a.member("Otto", "otto", users.Observer)
	return a
}

type reqOpt struct {
	workspace, table, command string
	risk                      classes.Risk
	typ                       classes.RequestType
	min                       classes.Level
	pin                       bool
	approvals                 int
	agent                     string
	pr                        *chain.PRRef
}

// open puts a request in front of the people the way the chain does.
func (a *apprRig) open(o reqOpt) chain.Request {
	a.t.Helper()
	r := chain.Request{Workspace: "loja", Table: "42-login", Type: classes.RequestClassPermission, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople},
		Requester: "c1", RequesterName: "Dev", Class: "dev", About: "rodar um comando", CreatedAt: a.clk.Now(), Risk: classes.RiskResult{Risk: classes.RiskHigh, Rule: "teste", TableVersion: 1}, PR: o.pr}
	if o.workspace != "" {
		r.Workspace = o.workspace
	}
	if o.table != "" {
		r.Table = o.table
	}
	if o.typ != "" {
		r.Type = o.typ
	}
	if o.risk != "" {
		r.Risk.Risk = o.risk
	}
	if o.command != "" {
		r.Action = &classes.Action{Kind: providers.KindShell, Command: o.command}
		r.About = o.command
	}
	if o.agent != "" {
		r.Justification = o.agent
	}
	r, err := a.store.Insert(a.ctx, r)
	if err != nil {
		a.t.Fatal(err)
	}
	min := o.min
	if min == "" {
		min = classes.LevelApprover
	}
	card := chain.Card(r, chain.Level{Kind: chain.LevelPeople}, chain.AnswersFor(chain.LevelPeople, r))
	q := chain.PeopleQuestion{Request: r, Text: i18n.Render(i18n.PtBR, card.Msg()), Engine: card.Engine, Agent: card.Agent, Approvals: max(o.approvals, 1), MinLevel: min, PIN: o.pin, Answers: chain.AnswersFor(chain.LevelPeople, r)}
	if err := a.flow.Ask(a.ctx, q); err != nil {
		a.t.Fatal(err)
	}
	return r
}

func (a *apprRig) list(tok, query string) panel.ApprovalList {
	a.t.Helper()
	w := a.call("GET", "/api/approvals"+query, nil, opts{cookie: tok})
	if w.Code != 200 {
		a.t.Fatalf("list: %d %s", w.Code, w.Body)
	}
	return decodeInto[panel.ApprovalList](a.t, w)
}

func (a *apprRig) answer(tok string, id int64, body panel.ApprovalAnswerRequest, pin string) *httptest.ResponseRecorder {
	return a.call("POST", fmt.Sprintf("/api/approvals/%d/answer", id), body, opts{cookie: tok, pin: pin})
}

func answersOf(it panel.ApprovalItem) map[string]panel.ApprovalAnswer {
	m := map[string]panel.ApprovalAnswer{}
	for _, x := range it.Answers {
		m[x.Answer] = x
	}
	return m
}

func ids(list panel.ApprovalList) []int64 {
	out := []int64{}
	for _, it := range list.Requests {
		out = append(out, it.ID)
	}
	return out
}

// CA-CONV-03 (and the "newest first, filters" of the central): the request an
// approver sees is closed by another one through the flow (the way the Telegram
// answers), and the screen of the first is told, without reloading.
func TestARequestAnsweredElsewhereUpdatesTheOpenScreenAndLosesItsButtons(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	first := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestExtraCost, table: "7-busca"})
	second := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestExtraCost, table: "42-login"})

	list := a.list(a.anaTok, "")
	if got := ids(list); len(got) != 2 || got[0] != second.ID || got[1] != first.ID || list.Pending != 2 {
		t.Fatalf("the list is %v (pending %d), want the newest first", got, list.Pending)
	}
	if got := ids(a.list(a.anaTok, "?table=7-busca")); len(got) != 1 || got[0] != first.ID {
		t.Fatalf("the filter by table gave %v", got)
	}
	if got := ids(a.list(a.anaTok, "?type=pr")); len(got) != 0 {
		t.Fatalf("the filter by type gave %v", got)
	}
	if got := ids(a.list(a.anaTok, "?workspace=blog")); len(got) != 0 {
		t.Fatalf("the filter by workspace gave %v", got)
	}

	srv := a.wsServer()
	c, _, err := a.dial(srv, a.anaTok, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, panel.TypeUISubscribe, "S1", panel.UISubscribe{Topics: []string{"approvals:" + a.ana.ID}})
	read(t, c) // the snapshot with the count
	read(t, c) // the answer to the subscription

	// Bia answers by the path the Telegram uses
	if _, err := a.flow.Answer(a.ctx, a.admin.ID, approvals.Input{Request: second.ID, Answer: chain.AnswerApprove}); err != nil {
		t.Fatal(err)
	}
	var changed panel.ApprovalEvent
	var count panel.ApprovalsSnapshot
	for range 2 {
		env := read(t, c)
		var ev panel.UIEvent
		mustJSON(t, env.Payload, &ev)
		switch ev.Event {
		case "changed":
			mustJSON(t, ev.Data, &changed)
		case "count":
			mustJSON(t, ev.Data, &count)
		}
	}
	if changed.Request != second.ID || changed.State != "approved" || changed.Closed == nil || changed.Closed.By != "Bia" || count.Pending != 1 {
		t.Fatalf("event %+v count %+v", changed, count)
	}
	// the item, read again, has no buttons and says who answered
	got := decodeInto[panel.ApprovalItem](t, a.call("GET", fmt.Sprintf("/api/approvals/%d", second.ID), nil, opts{cookie: a.anaTok}))
	if got.Closed == nil || got.Closed.By != "Bia" || got.Closed.State != "approved" || got.Closed.Self || len(got.Answers) != 0 {
		t.Fatalf("the closed item is %+v", got)
	}
	if ids := ids(a.list(a.anaTok, "")); len(ids) != 1 || ids[0] != first.ID {
		t.Fatalf("the list after is %v", ids)
	}
}

// CA-CONV-04: an Observer calls the route that approves, and the server refuses it
// with the level that is required.
func TestAnObserverCannotAnswerAndTheServerNamesTheLevel(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	r := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestExtraCost})
	w := a.answer(a.obsTok, r.ID, panel.ApprovalAnswerRequest{Answer: "approve"}, "")
	a.wantErr(w, 403, panel.CodeForbiddenLevel)
	if b := decodeInto[panel.ErrorBody](t, w); b.Error.Params["need"] != "approver" {
		t.Fatalf("params %v", b.Error.Params)
	}
	if got := a.list(a.anaTok, ""); got.Pending != 1 {
		t.Fatalf("the request changed: %+v", got)
	}
	a.wantErr(a.call("GET", "/api/approvals", nil, opts{cookie: a.obsTok}), 403, panel.CodeForbiddenLevel)
}

// CA-CONV-07: a request of risk alto with the people: an Administrator and the Owner
// are given the pattern and the rule for the class, only the Owner the delegation, the
// route of the delegation refuses the Administrator with the level, and approving
// the pattern brings the suggestion of the engine and asks the PIN before it counts.
func TestAHighRiskRequestGivesTheAnswersOfEachLevelAndTheSuggestedPattern(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	r := a.open(reqOpt{command: "npm install lodash"})

	adm := a.list(a.adminTok, "").Requests[0]
	own := a.list(a.ownerTok, "").Requests[0]
	for _, name := range []string{"approve", "approve_pattern", "approve_always", "deny", "deny_instruction", "ask_back"} {
		if _, ok := answersOf(adm)[name]; !ok {
			t.Errorf("the Administrator has no %s: %v", name, adm.Answers)
		}
	}
	if _, ok := answersOf(adm)["delegate"]; ok {
		t.Errorf("the Administrator is given Delegar")
	}
	if _, ok := answersOf(own)["delegate"]; !ok || own.Scope == nil || own.Scope.Table != "42-login" || own.Scope.Days != 30 {
		t.Errorf("the Owner is not given Delegar with its scope: %v %+v", own.Answers, own.Scope)
	}
	// an Approver has the plain answers of the type only
	apr := answersOf(a.list(a.anaTok, "").Requests[0])
	if _, ok := apr["approve_always"]; ok || len(apr) != 4 {
		t.Errorf("the Approver has %v", apr)
	}
	pat := answersOf(adm)["approve_pattern"]
	if pat.Suggested != "npm install *" || !pat.NeedsPIN || pat.Field != "pattern" {
		t.Fatalf("the pattern answer is %+v", pat)
	}
	if f := answersOf(adm)["approve_for"]; f.Field != "duration" || !f.NeedsPIN {
		t.Fatalf("the answer for a time is %+v", f)
	}

	a.wantErr(a.answer(a.adminTok, r.ID, panel.ApprovalAnswerRequest{Answer: "delegate"}, a.pinOf(a.admin)), 403, panel.CodeForbiddenLevel)
	// the pattern asks the PIN, and nothing is decided without it, or with a wrong one
	body := panel.ApprovalAnswerRequest{Answer: "approve_pattern", Pattern: pat.Suggested}
	a.wantErr(a.answer(a.adminTok, r.ID, body, ""), 403, panel.CodePINRequired)
	a.wantErr(a.answer(a.adminTok, r.ID, body, "000000"), 403, panel.CodePINInvalid)
	if st := a.stateOf(r.ID); st != chain.Open {
		t.Fatalf("the request is %s before the right PIN", st)
	}
	w := a.answer(a.adminTok, r.ID, body, "654321")
	if w.Code != 200 || decodeInto[panel.ApprovalAnswered](t, w).State != "approved" {
		t.Fatalf("approving the pattern with the PIN: %d %s", w.Code, w.Body)
	}
	if a.audited("panel.approval.answered") != 1 {
		t.Fatal("the answer is not in the audit")
	}
}

func (a *apprRig) pinOf(users.User) string { return "654321" }

func (a *apprRig) stateOf(id int64) chain.State {
	a.t.Helper()
	r, err := a.store.Get(a.ctx, id)
	if err != nil {
		a.t.Fatal(err)
	}
	return r.State
}

// A request that does not ask the PIN is answered by the button alone; a plain
// denial never asks it; the data an answer needs is checked by the server.
func TestAnAnswerThatAsksNoPINNeedsNone(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	r := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestExtraCost})
	it := a.list(a.anaTok, "").Requests[0]
	if answersOf(it)["approve"].NeedsPIN || answersOf(it)["deny_instruction"].Field != "text" {
		t.Fatalf("answers %+v", it.Answers)
	}
	a.wantErr(a.answer(a.anaTok, r.ID, panel.ApprovalAnswerRequest{Answer: "deny_instruction"}, ""), 400, panel.CodeBadRequest)
	a.wantErr(a.answer(a.anaTok, r.ID, panel.ApprovalAnswerRequest{Answer: "lixo"}, ""), 400, panel.CodeBadRequest)
	w := a.answer(a.anaTok, r.ID, panel.ApprovalAnswerRequest{Answer: "deny_instruction", Text: "use pnpm"}, "")
	if w.Code != 200 || decodeInto[panel.ApprovalAnswered](t, w).State != "denied" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// it ended: another answer is a conflict with the reason
	a.wantErr(a.answer(a.anaTok, r.ID, panel.ApprovalAnswerRequest{Answer: "approve"}, ""), 409, panel.CodeApprovalClosed)
}

// The request of a workspace the user does not act in is not found by any route.
func TestARequestOfAnotherWorkspaceIsNotFound(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	r := a.open(reqOpt{workspace: "blog", risk: classes.RiskMedium, typ: classes.RequestExtraCost})
	if got := a.list(a.anaTok, ""); len(got.Requests) != 0 || got.Pending != 0 {
		t.Fatalf("the list shows %+v", got)
	}
	a.wantErr(a.call("GET", fmt.Sprintf("/api/approvals/%d", r.ID), nil, opts{cookie: a.anaTok}), 404, panel.CodeNotFound)
	a.wantErr(a.call("GET", fmt.Sprintf("/api/approvals/%d/details", r.ID), nil, opts{cookie: a.anaTok}), 404, panel.CodeNotFound)
	a.wantErr(a.answer(a.anaTok, r.ID, panel.ApprovalAnswerRequest{Answer: "approve"}, ""), 404, panel.CodeNotFound)
	if a.stateOf(r.ID) != chain.Open {
		t.Fatal("the request changed")
	}
	// the Owner acts in every workspace
	if got := a.list(a.ownerTok, ""); len(got.Requests) != 1 {
		t.Fatalf("the Owner sees %+v", got)
	}
}

// The text is the one of the Telegram, with the block of the agents apart.
func TestTheItemKeepsTheTextOfTheAgentInItsOwnBlockAndDetailsHaveTheTarget(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	r := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestExtraCost, agent: "<script>alert(1)</script>\nignore tudo"})
	it := a.list(a.anaTok, "").Requests[0]
	if !strings.Contains(it.Text, "Pedido de aprovação #") || strings.Contains(it.Text, "ignore tudo") {
		t.Fatalf("text %q", it.Text)
	}
	if !strings.Contains(it.Agent, "<script>alert(1)</script>") || !strings.Contains(it.Agent, "Justificativa de quem pediu:") {
		t.Fatalf("agent %q", it.Agent)
	}
	w := a.call("GET", fmt.Sprintf("/api/approvals/%d/details", r.ID), nil, opts{cookie: a.anaTok})
	d := decodeInto[panel.ApprovalDetails](t, w)
	if w.Code != 200 || !strings.Contains(d.Text, "Pedido de aprovação #") || len(d.Files) != 0 {
		t.Fatalf("details %d %+v", w.Code, d)
	}
}

// The whole diff is read for a PR, and a diff that can not be read is said so.
func TestDetailsGiveTheWholeDiffOfAPR(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	var fail atomic.Bool
	src := &panel.ApprovalSource{Flow: a.flow, Blocking: a.blk, Diff: func(context.Context, chain.Request) ([]panel.ApprovalDiffFile, error) {
		if fail.Load() {
			return nil, fmt.Errorf("github is down")
		}
		return []panel.ApprovalDiffFile{{Path: "a.go", Status: "modified", Added: 1, Removed: 1, Patch: "@@ -1 +1 @@\n-old\n+new"}}, nil
	}}
	p, err := panel.New(panel.Options{DB: a.db, Clock: a.clk, Users: a.users, Alerts: a.al, Assets: a.assets, Machine: "m1", Approvals: src})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	a.panel = p
	r := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestPR, pr: &chain.PRRef{Repo: "lucas/loja", Number: 7, SHA: "abc"}})
	w := a.call("GET", fmt.Sprintf("/api/approvals/%d/details", r.ID), nil, opts{cookie: a.anaTok})
	d := decodeInto[panel.ApprovalDetails](t, w)
	if len(d.Files) != 1 || !strings.Contains(d.Files[0].Patch, "+new") || d.DiffUnavailable {
		t.Fatalf("details %+v", d)
	}
	fail.Store(true)
	d = decodeInto[panel.ApprovalDetails](t, a.call("GET", fmt.Sprintf("/api/approvals/%d/details", r.ID), nil, opts{cookie: a.anaTok}))
	if !d.DiffUnavailable || len(d.Files) != 0 || d.Text == "" {
		t.Fatalf("details %+v", d)
	}
}

// Requests like each other are one item with the list of their actions, and the
// pattern is the one they share.
func TestSimilarRequestsAreOneItem(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	for _, c := range []string{"npm install a", "npm install b", "npm install c"} {
		a.open(reqOpt{command: c, risk: classes.RiskLow})
	}
	got := a.list(a.adminTok, "")
	if len(got.Requests) != 1 || len(got.Requests[0].Members) != 3 || got.Pending != 1 {
		t.Fatalf("list %+v", got)
	}
	if s := answersOf(got.Requests[0])["approve_pattern"].Suggested; s != "npm install *" {
		t.Fatalf("the shared pattern is %q", s)
	}
	// answering the item with "Aprovar agora" answers the three, as in the Telegram
	if w := a.answer(a.adminTok, got.Requests[0].ID, panel.ApprovalAnswerRequest{Answer: "approve"}, ""); w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if left := a.list(a.adminTok, ""); left.Pending != 0 {
		t.Fatalf("what is left: %+v", left)
	}
}

// CA-CONV-08 and CA-CONV-10: a notice that stops a table, with 10 lines of evidence
// where a token is, shows all the lines with the token masked, the text of the agent
// apart, and the step of the vault is a button for the Owner only.
func TestABlockingNoticeShowsTheWholeMaskedEvidenceAndTheStepOnlyToTheOwner(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	token := "ghp_" + strings.Repeat("a1B2", 9)
	var ev []string
	for i := 1; i <= 10; i++ {
		line := fmt.Sprintf("checkout_test.go:%d → falhou", i)
		if i == 4 {
			line += " com " + token
		}
		ev = append(ev, line)
	}
	n := blocking.Notice{Workspace: "loja", Mesa: "42-login", Motive: blocking.Credential, Stopped: "falta a chave STRIPE_TEST_KEY", Evidence: strings.Join(ev, "\n"),
		Steps: []blocking.Step{{Text: i18n.M("block.step_vault"), Kind: blocking.StepVault}}, Owner: i18n.Text("Lucas"),
		WhyNot: "envolve cobrança, é decisão sua", Checked: "a chave não está no cofre"}
	id, err := a.blk.Save(a.ctx, n, a.mask)
	if err != nil {
		t.Fatal(err)
	}
	own := a.list(a.ownerTok, "").Blocking
	adm := a.list(a.adminTok, "").Blocking
	if len(own) != 1 || len(adm) != 1 || own[0].ID != id {
		t.Fatalf("owner %v admin %v", own, adm)
	}
	v := own[0]
	if strings.Count(v.Evidence, "\n") != 9 || strings.Contains(v.Evidence, token) || !strings.Contains(v.Evidence, "checkout_test.go:10") {
		t.Fatalf("evidence %q", v.Evidence)
	}
	if !strings.Contains(v.Agent, "Por que não decido: envolve cobrança") || strings.Contains(v.Stopped+v.Evidence, "decisão sua") {
		t.Fatalf("agent %q", v.Agent)
	}
	if len(v.Steps) != 1 || v.Steps[0].Action != "open_vault" || !v.Steps[0].CanDo {
		t.Fatalf("the Owner's steps %+v", v.Steps)
	}
	if s := adm[0].Steps[0]; s.CanDo {
		t.Fatalf("the Administrator is given the step: %+v", s)
	}
	// an Approver is not who these notices go to; type=aviso is only these
	if got := a.list(a.anaTok, ""); len(got.Blocking) != 0 {
		t.Fatalf("the Approver sees %v", got.Blocking)
	}
	if got := a.list(a.ownerTok, "?type=aviso"); len(got.Blocking) != 1 || len(got.Requests) != 0 {
		t.Fatalf("filter aviso: %+v", got)
	}
	if got := a.list(a.ownerTok, "?workspace=blog"); len(got.Blocking) != 0 {
		t.Fatalf("filter workspace: %+v", got)
	}
	// old notices go
	a.clk.Advance(panel.BlockingWindow + time.Hour)
	if got := a.list(a.ownerSession(), ""); len(got.Blocking) != 0 {
		t.Fatalf("an old notice is still there: %+v", got)
	}
}

// The history is read from what ended, with its filters, and has what the agents decided alone.
func TestTheHistoryListsWhatEndedWithFilters(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	one := a.open(reqOpt{risk: classes.RiskMedium, typ: classes.RequestExtraCost})
	two := a.open(reqOpt{risk: classes.RiskHigh, typ: classes.RequestExtraCost})
	if _, err := a.flow.Answer(a.ctx, a.ana.ID, approvals.Input{Request: one.ID, Answer: chain.AnswerApprove}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.flow.Answer(a.ctx, a.admin.ID, approvals.Input{Request: two.ID, Answer: chain.AnswerDeny}); err != nil {
		t.Fatal(err)
	}
	// one an agent decided alone, which the people never saw
	r := chain.Request{Workspace: "loja", Table: "42-login", Type: classes.RequestClassPermission, State: chain.Open, Level: chain.Level{Kind: chain.LevelMesa, Agent: "c9"}, CreatedAt: a.clk.Now(),
		Risk: classes.RiskResult{Risk: classes.RiskLow}, About: "ls"}
	r, err := a.store.Insert(a.ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	r.State, r.ClosedAt, r.Decision = chain.Approved, a.clk.Now(), &chain.Closing{State: chain.Approved, By: "c9", Level: r.Level, Answer: chain.AnswerApprove, Reason: "só leitura"}
	if err := a.store.Save(a.ctx, r); err != nil {
		t.Fatal(err)
	}
	get := func(q string) panel.ApprovalHistory {
		w := a.call("GET", "/api/approvals/history"+q, nil, opts{cookie: a.ownerTok})
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", q, w.Code, w.Body)
		}
		return decodeInto[panel.ApprovalHistory](t, w)
	}
	all := get("")
	if len(all.Items) != 3 || all.Items[0].ID != r.ID || all.Items[0].Reason != "só leitura" || all.Items[0].ByUser || all.Items[0].LevelKind != "mesa" {
		t.Fatalf("history %+v", all.Items)
	}
	if got := get("?state=denied").Items; len(got) != 1 || got[0].ID != two.ID || got[0].By != "Bia" || !got[0].ByUser {
		t.Fatalf("denied: %+v", got)
	}
	if got := get("?by=" + a.ana.ID).Items; len(got) != 1 || got[0].ID != one.ID {
		t.Fatalf("by Ana: %+v", got)
	}
	if got := get("?by=agent").Items; len(got) != 1 || got[0].ID != r.ID {
		t.Fatalf("by agent: %+v", got)
	}
	if got := get("?level=people").Items; len(got) != 2 {
		t.Fatalf("level people: %+v", got)
	}
	if got := get("?risk=alto").Items; len(got) != 1 || got[0].ID != two.ID {
		t.Fatalf("risk: %+v", got)
	}
	day := a.clk.Now().Format("2006-01-02")
	if got := get("?from=" + day + "&to=" + day).Items; len(got) != 3 {
		t.Fatalf("period: %+v", got)
	}
	if got := get("?from=2030-01-01").Items; len(got) != 0 {
		t.Fatalf("future period: %+v", got)
	}
	if p := get("?limit=2"); len(p.Items) != 2 || p.Next == 0 {
		t.Fatalf("page: %+v", p)
	} else if rest := get(fmt.Sprintf("?limit=2&before=%d", p.Next)); len(rest.Items) != 1 {
		t.Fatalf("next page: %+v", rest)
	}
	// Ana sees what she could have been asked about, and an Observer nothing
	a.wantErr(a.call("GET", "/api/approvals/history", nil, opts{cookie: a.obsTok}), 403, panel.CodeForbiddenLevel)
	if got := decodeInto[panel.ApprovalHistory](t, a.call("GET", "/api/approvals/history", nil, opts{cookie: a.anaTok})); len(got.Items) != 3 {
		t.Fatalf("Ana sees %+v", got.Items)
	}
}

// websocket helpers for the bell and the counter.
func readEvent(t *testing.T, c *websocket.Conn) (string, panel.UIEvent) {
	t.Helper()
	env := read(t, c)
	var ev panel.UIEvent
	mustJSON(t, env.Payload, &ev)
	return env.Type, ev
}
