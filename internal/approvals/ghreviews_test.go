package approvals_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/github"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/testkit/fakegithub"
	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/users"
)

// openPRs are the requests of PR the intake finds open.
type openPRs struct {
	mu   sync.Mutex
	reqs []chain.Request
}

func (o *openPRs) OpenPR(_ context.Context, _, _ string, _ int) ([]chain.Request, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]chain.Request(nil), o.reqs...), nil
}

// pinPrompter records the PIN requests the channel was asked to make.
type pinPrompter struct {
	mu    sync.Mutex
	asked map[string]approvals.Reply
}

func (p *pinPrompter) AskPIN(_ context.Context, userID string, rep approvals.Reply) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.asked == nil {
		p.asked = map[string]approvals.Reply{}
	}
	p.asked[userID] = rep
	return nil
}

func (p *pinPrompter) get(userID string) (approvals.Reply, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.asked[userID]
	return r, ok
}

const loja = "lucas/loja"

type reviewsRig struct {
	*world
	gh   *fakegithub.Server
	rest *github.REST
	in   *approvals.GitHubReviews
	reqs *openPRs
	pin  *pinPrompter
	pr   int
	sha  string
}

func newReviewsRig(t *testing.T) *reviewsRig {
	t.Helper()
	w := newWorld(t)
	gh, err := fakegithub.New(w.clk, t)
	if err != nil {
		t.Fatal(err)
	}
	gh.AddRepo("lucas", "loja")
	gh.IssueToken("ghs_grao", "mestre-grao-mestre")
	r := &reviewsRig{world: w, gh: gh, reqs: &openPRs{}, pin: &pinPrompter{}, sha: strings.Repeat("d", 40)}
	r.rest = &github.REST{BaseURL: "https://api.github.com", HTTP: &http.Client{Transport: fakenet.HandlerTransport(gh)},
		Token: func(context.Context, string) (string, error) { return "ghs_grao", nil }}
	r.pr = gh.AddPR(loja, "Login", "42-login", loja)
	gh.SetPullAuthor(loja, r.pr, "mestre-dev[bot]")
	gh.SetPull(loja, r.pr, r.sha, false, "clean")
	r.in = &approvals.GitHubReviews{DB: w.db, Clock: w.clk, Requests: r.reqs, Flow: w.flow, Users: w.svc, PIN: r.pin,
		Reader: func(context.Context, string) (approvals.ReviewReader, error) { return r.rest, nil }}
	return r
}

// request puts a request of merge for the PR in front of the people.
func (r *reviewsRig) request(o openOpts) chain.Request {
	r.t.Helper()
	o.typ = classes.RequestMerge
	o.target = string(approvals.PRTarget(loja, r.pr, r.sha, "main"))
	return r.track(o)
}

// track opens the request described by o (with its type and target) bound to the PR
// and makes the intake find it.
func (r *reviewsRig) track(o openOpts) chain.Request {
	r.t.Helper()
	o.pr = &chain.PRRef{Repo: loja, Number: r.pr, SHA: r.sha}
	req := r.open(o)
	r.reqs.mu.Lock()
	r.reqs.reqs = append(r.reqs.reqs, req)
	r.reqs.mu.Unlock()
	return req
}

// person registers a user with the login on GitHub, who can write there.
func (r *reviewsRig) person(name, login string, level users.Level) users.User {
	r.t.Helper()
	u := r.add(name, level, "loja")
	if _, err := r.svc.Edit(r.ctx, r.owner, u.ID, users.Edit{GitHubLogin: &login}); err != nil {
		r.t.Fatal(err)
	}
	r.gh.AddUser(login, "User")
	r.gh.SetPermission(loja, login, "write")
	return u
}

// review makes a review on GitHub and gives the event the poller would give.
func (r *reviewsRig) review(login, state, sha string) github.Event {
	r.t.Helper()
	r.gh.AddReviewAt(loja, r.pr, login, state, sha)
	all, err := r.rest.Reviews(r.ctx, loja, r.pr)
	if err != nil {
		r.t.Fatal(err)
	}
	last := all[len(all)-1]
	return r.event(last.ID, login, state)
}

func (r *reviewsRig) event(id int64, login, state string) github.Event {
	return github.Event{Kind: github.EventReview, ID: id, Key: fmt.Sprintf("revisao:%d:%s", id, state), Workspace: "loja", Repo: loja, Number: r.pr, Author: login,
		Link: fmt.Sprintf("https://github.com/%s/pull/%d#pullrequestreview-%d", loja, r.pr, id)}
}

func (r *reviewsRig) deliver(evs ...github.Event) {
	r.t.Helper()
	if err := r.in.Deliver(r.ctx, github.Watch{Workspace: "loja", Repo: loja}, "42-login", evs); err != nil {
		r.t.Fatal(err)
	}
}

// audit is what the intake wrote for a request: outcome and reason of each row.
func (r *reviewsRig) audit(request int64) []string {
	r.t.Helper()
	rows, err := r.db.Reader().QueryContext(r.ctx, `SELECT login || '|' || state || '|' || outcome || '|' || reason FROM approval_github_reviews WHERE request = ? ORDER BY review, state`, request)
	if err != nil {
		r.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, s)
	}
	return out
}

func (r *reviewsRig) count(request int64, outcome string) int {
	var n int
	err := r.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM approval_github_reviews WHERE request = ? AND outcome = ?`, request, outcome).Scan(&n)
	if err != nil && err != sql.ErrNoRows {
		r.t.Fatal(err)
	}
	return n
}

// pinOwner gives the Owner a login on GitHub, as admin of the repository, and a PIN.
func (r *reviewsRig) pinOwner(login string) users.User {
	r.t.Helper()
	if _, err := r.svc.Edit(r.ctx, r.owner, r.owner.ID, users.Edit{GitHubLogin: &login}); err != nil {
		r.t.Fatal(err)
	}
	r.gh.AddUser(login, "User")
	r.gh.SetPermission(loja, login, "admin")
	if err := r.svc.SetPIN(r.ctx, r.owner.ID, "123456"); err != nil {
		r.t.Fatal(err)
	}
	return r.owner
}

// CA-GH-APROV-09
func TestAReviewMadeOnGitHubOnlyAnswersTheRequestUnderEveryCondition(t *testing.T) {
	t.Parallel()
	r := newReviewsRig(t)
	ana := r.person("Ana", "ana-dev", users.Approver)
	// a registered and active user whose account on GitHub is not of the type User
	r.person("Robô", "robo-ci", users.Approver)
	r.gh.AddUser("robo-ci", "Bot")
	req := r.request(openOpts{})

	// an account that is not registered
	r.deliver(r.review("estranho", "APPROVED", r.sha))
	// a review of another commit
	r.deliver(r.review("ana-dev", "APPROVED", strings.Repeat("e", 40)))
	// an account of the type Bot
	r.deliver(r.review("robo-ci", "APPROVED", r.sha))
	if r.state(req.ID) != chain.Open || r.count(req.ID, "counted") != 0 {
		t.Fatalf("the request is %s after reviews that fail a condition; want it still waiting for the people", r.state(req.ID))
	}
	audit := r.audit(req.ID)
	if len(audit) != 3 || !strings.Contains(audit[0], "estranho|APPROVED|ignored|") || !strings.Contains(audit[0], "não é um usuário ativo") ||
		!strings.Contains(audit[1], "ana-dev|APPROVED|ignored|") || !strings.Contains(audit[1], "commit") ||
		!strings.Contains(audit[2], "robo-ci|APPROVED|ignored|") || !strings.Contains(audit[2], "Bot, não User") {
		t.Fatalf("audit = %q; want the three reviews ignored, each with its reason", audit)
	}

	// a review of a registered, active user, of type User, with write access, on the right commit
	good := r.review("ana-dev", "APPROVED", r.sha)
	r.deliver(good)
	if r.state(req.ID) != chain.Approved {
		t.Fatalf("the request is %s; want approved as if Ana had touched Aprovar", r.state(req.ID))
	}
	if d := r.ch.Decisions(); len(d) != 1 || d[0].User != ana.ID || d[0].Answer != chain.AnswerApprove {
		t.Fatalf("decisions = %+v; want the approval of Ana", d)
	}
	if got := r.audit(req.ID); len(got) != 4 || !strings.Contains(got[3], "ana-dev|APPROVED|counted|") {
		t.Fatalf("audit = %q; want the fourth review counted", got)
	}
	// the same event delivered again is the same review
	r.deliver(good)
	if got := r.audit(req.ID); len(got) != 4 {
		t.Fatalf("audit after a second delivery = %q", got)
	}

	// the same valid review once DISMISSED does not count (the event of the approval
	// reaches the engine after the dismissal)
	d := newReviewsRig(t)
	d.person("Ana", "ana-dev", users.Approver)
	dreq := d.request(openOpts{})
	ev := d.review("ana-dev", "APPROVED", d.sha)
	d.gh.DismissReview(loja, d.pr, "ana-dev")
	d.deliver(ev)
	d.deliver(d.event(ev.ID, "ana-dev", "DISMISSED"))
	if d.state(dreq.ID) != chain.Open || d.count(dreq.ID, "counted") != 0 || len(d.ch.Decisions()) != 0 {
		t.Fatalf("a dismissed review counted: %s, %q", d.state(dreq.ID), d.audit(dreq.ID))
	}
	if got := d.audit(dreq.ID); len(got) != 2 || !strings.Contains(got[0], "DISMISSED") || !strings.Contains(got[1], "|DISMISSED|dismissed|") {
		t.Fatalf("audit = %q; want the dismissal as the reason", got)
	}

	// a request that asks the PIN and the valid review of the Owner: it stays waiting,
	// the Owner is asked the PIN by the channel, and only the PIN concludes it
	p := newReviewsRig(t)
	owner := p.pinOwner("lucas-dono")
	// the request is a configuracao_repositorio, with the target of its own (SettingsTarget)
	settings, err := json.Marshal(approvals.SettingsTarget{Kind: approvals.KindRuleset, Repo: loja, Branch: "main", Body: map[string]any{"name": "mestre-review"}})
	if err != nil {
		t.Fatal(err)
	}
	preq := p.track(openOpts{typ: classes.RequestRepoSettings, target: string(settings), min: classes.LevelOwner, pin: true})
	p.deliver(p.review("lucas-dono", "APPROVED", p.sha))
	if p.state(preq.ID) != chain.Open || len(p.ch.Decisions()) != 0 {
		t.Fatalf("a review concluded a request that asks the PIN: %s", p.state(preq.ID))
	}
	rep, ok := p.pin.get(owner.ID)
	if !ok || rep.PIN == "" {
		t.Fatalf("the PIN was not asked of the Owner: %+v", rep)
	}
	if got := p.audit(preq.ID); len(got) != 1 || !strings.Contains(got[0], "lucas-dono|APPROVED|pin_asked|") {
		t.Fatalf("audit = %q; want the valid review registered with the PIN asked", got)
	}
	if err := p.svc.CheckPIN(p.ctx, rep.PIN, "000000"); err == nil {
		t.Fatal("a wrong PIN was accepted")
	}
	if p.state(preq.ID) != chain.Open {
		t.Fatalf("the request is %s without the PIN", p.state(preq.ID))
	}
	if err := p.svc.CheckPIN(p.ctx, rep.PIN, "123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.flow.ConfirmPIN(p.ctx, owner.ID, rep.PIN); err != nil || p.state(preq.ID) != chain.Approved {
		t.Fatalf("with the PIN: %v, %s", err, p.state(preq.ID))
	}
	if dd := p.ch.Decisions(); len(dd) != 1 || dd[0].User != owner.ID {
		t.Fatalf("decisions = %+v; want the one of the Owner, with the PIN", dd)
	}
}

func TestAReviewTheEnginePostedItselfIsNotAnotherVote(t *testing.T) {
	t.Parallel()
	r := newReviewsRig(t)
	r.person("Ana", "ana-dev", users.Approver)
	req := r.request(openOpts{})
	r.gh.AddReviewBody(loja, r.pr, "ana-dev", "APPROVED", r.sha, "Aprovado por Ana.\n\n<!-- mestre:op=11111111-2222-3333-4444-555555555555 -->")
	all, err := r.rest.Reviews(r.ctx, loja, r.pr)
	if err != nil {
		t.Fatal(err)
	}
	r.deliver(r.event(all[len(all)-1].ID, "ana-dev", "APPROVED"))
	if r.state(req.ID) != chain.Open {
		t.Fatalf("the request is %s; the review the engine wrote in the name of a person decided another request", r.state(req.ID))
	}
	if got := r.audit(req.ID); len(got) != 1 || !strings.Contains(got[0], "|ignored|") || !strings.Contains(got[0], "próprio Mestre") {
		t.Fatalf("audit = %q; want the review ignored with its reason", got)
	}
}

func TestAReviewThatWasDismissedDoesNotCount(t *testing.T) {
	t.Parallel()
	r := newReviewsRig(t)
	r.person("Ana", "ana-dev", users.Approver)
	req := r.request(openOpts{})
	ev := r.review("ana-dev", "APPROVED", r.sha)
	r.gh.DismissReview(loja, r.pr, "ana-dev") // the event of the approval reaches the engine late
	r.deliver(ev)
	if r.state(req.ID) != chain.Open || r.count(req.ID, "counted") != 0 {
		t.Fatalf("a dismissed review counted: %s, %q", r.state(req.ID), r.audit(req.ID))
	}
	if got := r.audit(req.ID); len(got) != 1 || !strings.Contains(got[0], "DISMISSED") {
		t.Fatalf("audit = %q; want the reason in the audit", got)
	}
}

// With two approvals required, the vote a review made is taken back when the
// review is dismissed afterwards.
func TestADismissalTakesBackTheVoteOfTheReview(t *testing.T) {
	t.Parallel()
	r := newReviewsRig(t)
	r.person("Ana", "ana-dev", users.Approver)
	req := r.request(openOpts{quorum: 2})
	ev := r.review("ana-dev", "APPROVED", r.sha)
	r.deliver(ev)
	if r.count(req.ID, "counted") != 1 || r.state(req.ID) != chain.Open {
		t.Fatalf("the first of two approvals: %s, %q", r.state(req.ID), r.audit(req.ID))
	}
	r.gh.DismissReview(loja, r.pr, "ana-dev")
	r.deliver(r.event(ev.ID, "ana-dev", "DISMISSED"))
	var counted int
	if err := r.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM approval_votes WHERE request = ? AND counts = 1`, req.ID).Scan(&counted); err != nil || counted != 0 {
		t.Fatalf("votes that count = %d, %v; want the vote of the dismissed review taken back", counted, err)
	}
	if r.count(req.ID, "dismissed") != 1 {
		t.Fatalf("audit = %q", r.audit(req.ID))
	}
}

func TestAReviewFromAccountsThatAreNotAPersonOrCannotWriteDoesNotCount(t *testing.T) {
	t.Parallel()
	r := newReviewsRig(t)
	r.person("Ana", "ana-dev", users.Approver)
	org := r.person("Acme", "acme-org", users.Approver)
	_ = org
	r.gh.AddUser("acme-org", "Organization")
	req := r.request(openOpts{})
	r.deliver(r.review("acme-org", "APPROVED", r.sha))
	if got := r.audit(req.ID); len(got) != 1 || !strings.Contains(got[0], "Organization") {
		t.Fatalf("audit = %q; want the type of the account as the reason", got)
	}
	// write access is read now, not remembered
	r.gh.SetPermission(loja, "ana-dev", "read")
	r.deliver(r.review("ana-dev", "APPROVED", r.sha))
	if r.state(req.ID) != chain.Open || r.count(req.ID, "counted") != 0 {
		t.Fatalf("a reader's review counted: %s, %q", r.state(req.ID), r.audit(req.ID))
	}
	// the permission the App cannot read: the association of the review stands in
	r.gh.FailPermission(http.StatusForbidden)
	r.gh.SetAssociation("ana-dev", "NONE")
	r.deliver(r.review("ana-dev", "APPROVED", r.sha))
	if r.state(req.ID) != chain.Open {
		t.Fatalf("a review with no association counted: %q", r.audit(req.ID))
	}
	r.gh.SetAssociation("ana-dev", "COLLABORATOR")
	r.deliver(r.review("ana-dev", "APPROVED", r.sha))
	if r.state(req.ID) != chain.Approved {
		t.Fatalf("a collaborator's review did not count: %q", r.audit(req.ID))
	}
}

func TestAReviewOfAUserBelowTheLevelOrOutsideTheWorkspaceDoesNotCount(t *testing.T) {
	t.Parallel()
	r := newReviewsRig(t)
	r.person("Olho", "olho-dev", users.Observer)
	req := r.request(openOpts{})
	r.deliver(r.review("olho-dev", "APPROVED", r.sha))
	if r.state(req.ID) != chain.Open || r.count(req.ID, "counted") != 0 {
		t.Fatalf("an Observer's review counted: %q", r.audit(req.ID))
	}
	r.person("Admin", "admin-dev", users.Approver)
	req2 := r.request(openOpts{min: classes.LevelAdmin})
	r.deliver(r.review("admin-dev", "APPROVED", r.sha))
	if r.state(req2.ID) != chain.Open {
		t.Fatalf("a review of a level below the minimum counted: %q", r.audit(req2.ID))
	}
}

func TestAReviewForARequestThatAsksThePINAsksItAndOnlyThePINConcludes(t *testing.T) {
	t.Parallel()
	r := newReviewsRig(t)
	owner := r.owner
	login := "lucas-dono"
	if _, err := r.svc.Edit(r.ctx, owner, owner.ID, users.Edit{GitHubLogin: &login}); err != nil {
		t.Fatal(err)
	}
	r.gh.AddUser(login, "User")
	r.gh.SetPermission(loja, login, "admin")
	if err := r.svc.SetPIN(r.ctx, owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	req := r.request(openOpts{min: classes.LevelOwner, pin: true})
	r.deliver(r.review(login, "APPROVED", r.sha))
	if r.state(req.ID) != chain.Open {
		t.Fatalf("a review concluded a request that asks the PIN: %s", r.state(req.ID))
	}
	rep, ok := r.pin.get(owner.ID)
	if !ok || rep.PIN == "" {
		t.Fatalf("the PIN was not asked of the Owner: %+v", rep)
	}
	if got := r.audit(req.ID); len(got) != 1 || !strings.Contains(got[0], "pin_asked") {
		t.Fatalf("audit = %q; want the valid review registered with the PIN asked", got)
	}
	if err := r.svc.CheckPIN(r.ctx, rep.PIN, "123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.flow.ConfirmPIN(r.ctx, owner.ID, rep.PIN); err != nil || r.state(req.ID) != chain.Approved {
		t.Fatalf("with the PIN: %v, %s", err, r.state(req.ID))
	}
}

// A review dismissed while its PIN is pending: the PIN that comes confirms nothing.
func TestADismissalDropsThePINThatWasWaiting(t *testing.T) {
	t.Parallel()
	r := newReviewsRig(t)
	owner := r.owner
	login := "lucas-dono"
	if _, err := r.svc.Edit(r.ctx, owner, owner.ID, users.Edit{GitHubLogin: &login}); err != nil {
		t.Fatal(err)
	}
	r.gh.AddUser(login, "User")
	r.gh.SetPermission(loja, login, "admin")
	if err := r.svc.SetPIN(r.ctx, owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	req := r.request(openOpts{min: classes.LevelOwner, pin: true})
	ev := r.review(login, "APPROVED", r.sha)
	r.deliver(ev)
	rep, ok := r.pin.get(owner.ID)
	if !ok {
		t.Fatal("no PIN asked")
	}
	r.gh.DismissReview(loja, r.pr, login)
	r.deliver(r.event(ev.ID, login, "DISMISSED"))
	if err := r.svc.CheckPIN(r.ctx, rep.PIN, "123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.flow.ConfirmPIN(r.ctx, owner.ID, rep.PIN); err == nil || r.state(req.ID) != chain.Open {
		t.Fatalf("the PIN of a dismissed review concluded the request: %v, %s", err, r.state(req.ID))
	}
}

func TestReviewsOfNoRequestAreNobodysBusiness(t *testing.T) {
	t.Parallel()
	r := newReviewsRig(t)
	r.person("Ana", "ana-dev", users.Approver)
	before := len(r.gh.Reads())
	r.deliver(r.review("ana-dev", "APPROVED", r.sha))
	// not a review, or no id: nothing is read
	r.deliver(github.Event{Kind: github.EventComment, Key: "comentario:1:x", Repo: loja, Number: r.pr})
	r.deliver(github.Event{Kind: github.EventReview, Key: "revisao:0:APPROVED", Repo: loja, Number: r.pr})
	if after := len(r.gh.Reads()); after-before > 1 { // only the list of reviews that made the event
		t.Fatalf("%d reads for events with no request", after-before)
	}
}
