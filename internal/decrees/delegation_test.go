package decrees_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/users"
)

const day = 24 * time.Hour

// reminders records the delegations the author was reminded of.
type reminders struct {
	mu   sync.Mutex
	list []decrees.Decree
}

func (r *reminders) DelegationEnding(_ context.Context, d decrees.Decree) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, d)
}

func (r *reminders) n() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.list)
}

// hooks records what the emergency button asks of the rest of the engine.
type hooks struct {
	mu       sync.Mutex
	rules    map[string]int
	rerouted []string
	told     map[string][]string
}

func (h *hooks) RevokeRules(_ context.Context, ws, _ string) (int, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.rules[ws], nil
}

func (h *hooks) Reroute(_ context.Context, ws string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rerouted = append(h.rerouted, ws)
	return nil
}

func (h *hooks) Told(_ context.Context, ws, text string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.told == nil {
		h.told = map[string][]string{}
	}
	h.told[ws] = append(h.told[ws], text)
}

// withHooks is a second service on the same database, with the reminder, the
// emergency button and the tables the test wants.
func (w *world) withHooks(rem decrees.Reminder, h decrees.RevokeHooks, open func(context.Context, string, string) (bool, error)) *decrees.Service {
	w.t.Helper()
	s, err := decrees.New(decrees.Options{DB: w.db, Clock: w.clk, PINs: w.users, Alerts: w.al, Targets: w.heads, Requests: w.reqs, Reminder: rem, Revoked: h, TableOpen: open})
	if err != nil {
		w.t.Fatal(err)
	}
	return s
}

func destructive(w *world, id int64, table string) chain.Request {
	r := w.request(id, classes.RequestDestructive, nil)
	r.Table = table
	r.Risk = classes.RiskResult{Risk: classes.RiskHigh}
	w.reqs[id] = r
	return r
}

// risky is a request with the risk the engine gave it.
func (w *world) risky(id int64, typ classes.RequestType, risk classes.Risk) chain.Request {
	r := w.request(id, typ, nil)
	r.Risk = classes.RiskResult{Risk: risk}
	w.reqs[id] = r
	return r
}

func (w *world) covers(r chain.Request) bool {
	w.t.Helper()
	ok, err := w.svc.Covers(w.ctx, r)
	if err != nil {
		w.t.Fatal(err)
	}
	return ok
}

// Part of CA-DELEG-01 (the PIN through the flow and the decisions of the chain
// are in internal/orchestration/chain/delegation_audit_test.go): "Delegar" on a
// destructive request, confirmed by the Owner, is a decree of delegation for
// that type in that table, for 30 days; another table is not covered.
func TestDelegarMakesADelegationOfThirtyDaysForTheTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	r := destructive(w, 1, "42-login")
	if err := w.svc.Decree(w.ctx, chain.DecreeGrant{Request: 1, Answer: chain.AnswerDelegate, Table: "42-login", By: w.owner.ID, Level: chain.Level{Kind: chain.LevelPeople}}); err != nil {
		t.Fatal(err)
	}
	ds, err := w.svc.List(w.ctx, w.owner.ID)
	if err != nil || len(ds) != 1 {
		t.Fatalf("decrees = %+v, %v", ds, err)
	}
	d := ds[0]
	if d.Effect.Kind != decrees.EffectDelegation || d.Effect.Setting != string(classes.RequestDestructive) || d.Mesa != "42-login" || !d.PIN {
		t.Fatalf("decree = %+v", d)
	}
	if want := w.clk.Now().Add(30 * day); !d.Until.Equal(want) {
		t.Fatalf("until = %v, want %v", d.Until, want)
	}
	if !w.covers(destructive(w, 2, "42-login")) {
		t.Fatal("the next request of the table is not the Grand Master's")
	}
	if w.covers(destructive(w, 3, "43-busca")) {
		t.Fatal("a request of another table is covered: it must go to the people")
	}
	_ = r
}

// Part of CA-DELEG-02 (the whole criterion, through the real flow and the PIN,
// is in internal/orchestration/chain/delegation_audit_test.go): an Administrator can not delegate a type alto; "Delegar" on a medio
// request makes a decree of policy in that type and table.
func TestAnAdministratorDelegatesNoHighTypeAndDelegarOnAMedioIsAPolicy(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	p := decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: "42-login", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
		Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Text: "delegue"}
	if ref := w.refusal(p); ref.Code != decrees.CodeLevel {
		t.Fatalf("refusal = %+v", ref)
	}
	r := w.request(5, classes.RequestMerge, nil)
	r.Risk = classes.RiskResult{Risk: classes.RiskMedium}
	w.reqs[5] = r
	if err := w.svc.Decree(w.ctx, chain.DecreeGrant{Request: 5, Answer: chain.AnswerDelegate, Table: "42-login", By: ana.ID, Level: chain.Level{Kind: chain.LevelPeople}}); err != nil {
		t.Fatal(err)
	}
	ds, _ := w.svc.List(w.ctx, w.owner.ID)
	if len(ds) != 1 || ds[0].Effect.Kind != decrees.EffectPolicy || ds[0].Effect.Setting != "merge" || !ds[0].Effect.Loosens || ds[0].Mesa != "42-login" || ds[0].Author != ana.ID {
		t.Fatalf("decrees = %+v", ds)
	}
}

// Part of CA-DELEG-03 (the whole criterion, over the bot and the chain, is in
// internal/orchestration/chain/delegation_audit_test.go): the reminder comes 3
// days before the end with Renovar; Renovar
// proposes 30 more days with the PIN; the delegation expires at 30 days and
// covers nothing; 31 days is refused.
func TestADelegationIsRemindedRenewedAndExpires(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	rem := &reminders{}
	svc := w.withHooks(rem, nil, nil)
	prop := func(until time.Duration) decrees.Proposal {
		var end time.Time
		if until > 0 {
			end = w.clk.Now().Add(until)
		}
		return decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: "42-login", Until: end,
			Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Text: "delegue os workflows"}
	}
	if _, err := svc.Propose(w.ctx, prop(31*day)); err == nil {
		t.Fatal("31 days were accepted")
	} else if ref, ok := err.(*decrees.Refusal); !ok || ref.Code != decrees.CodeDelegation {
		t.Fatalf("31 days = %v", err)
	}
	d := w.confirm(prop(0))
	req := w.request(7, classes.RequestWorkflows, nil)
	w.clk.Advance(26 * day)
	if err := svc.Sweep(w.ctx); err != nil || rem.n() != 0 {
		t.Fatalf("reminded too early: %d, %v", rem.n(), err)
	}
	w.clk.Advance(1 * day) // 27 days
	if err := svc.Sweep(w.ctx); err != nil || rem.n() != 1 || rem.list[0].Number != d.Number {
		t.Fatalf("reminders = %+v, %v", rem.list, err)
	}
	if err := svc.Sweep(w.ctx); err != nil || rem.n() != 1 {
		t.Fatalf("reminded twice: %d, %v", rem.n(), err)
	}
	if _, err := svc.Renew(w.ctx, d.Number, w.person("Bia", users.Approver, "loja").ID); err == nil {
		t.Fatal("somebody else renewed it")
	}
	pend, err := svc.Renew(w.ctx, d.Number, w.owner.ID)
	if err != nil || !pend.PIN {
		t.Fatalf("renew = %+v, %v", pend, err)
	}
	pinReq, err := svc.BeginPIN(w.ctx, pend.ID, w.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.users.CheckPIN(w.ctx, pinReq.ID, correctPIN); err != nil {
		t.Fatal(err)
	}
	nd, err := svc.Confirm(w.ctx, pend.ID, w.owner.ID, pinReq.ID)
	if err != nil || !nd.Until.Equal(w.clk.Now().Add(30*day)) {
		t.Fatalf("renewed = %+v, %v", nd, err)
	}
	w.clk.Advance(3 * day) // 30 days after the first: the old one is over, the renewal stands
	if err := svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if !w.covers(req) {
		t.Fatal("the renewal does not cover")
	}
	w.clk.Advance(28 * day)
	if err := svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.covers(req) {
		t.Fatal("an expired delegation covers: the request must go to the people")
	}
}

func TestADelegationNeedsAScope(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	p := decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
		Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Text: "delegue"}
	if ref := w.refusal(p); ref.Code != decrees.CodeDelegation {
		t.Fatalf("no scope = %+v", ref)
	}
	p.Scope = "padrao:tudo"
	if ref := w.refusal(p); ref.Code != decrees.CodeDelegation {
		t.Fatalf("scope that is not understood = %+v", ref)
	}
	p.Scope = "classe:dev"
	w.confirm(p)
	if !w.covers(w.request(1, classes.RequestWorkflows, nil)) {
		t.Fatal("a delegation to the class dev did not cover a request of dev")
	}
	other := w.request(2, classes.RequestWorkflows, nil)
	other.Class = "qa"
	if w.covers(other) {
		t.Fatal("covered another class")
	}
}

var (
	gm   = characters.Character{ID: "gm", Name: "Grão-Mestre", Role: characters.RoleGrandMaster, Workspace: "loja"}
	tm42 = characters.Character{ID: "tm42", Name: "Mestre da mesa", Role: characters.RoleTableMaster, Workspace: "loja", Table: "42-login"}
	tm43 = characters.Character{ID: "tm43", Name: "Mestre da mesa", Role: characters.RoleTableMaster, Workspace: "loja", Table: "43-busca"}
)

func (w *world) workflowsDelegation(mesa string, until time.Duration) decrees.Decree {
	p := decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: mesa,
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Text: "delegue"}
	if until > 0 {
		p.Until = w.clk.Now().Add(until)
	}
	return w.confirm(p)
}

func refusalCode(t *testing.T, err error) string {
	t.Helper()
	ref, ok := err.(*decrees.Refusal)
	if !ok {
		t.Fatalf("err = %v, want a refusal", err)
	}
	return ref.Code
}

// Part of CA-DELEG-04 (the decision of the Mestre da mesa through the chain is in
// internal/orchestration/chain/delegation_audit_test.go): the Grand Master
// subdelegates to the Mestre da mesa inside its
// delegation; wider, other table, too long or by the Mestre da mesa is refused;
// when the origin is revoked the subdelegation ends with it.
func TestTheGrandMasterSubdelegatesNarrowerAndTheOriginEndsIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	origin := w.workflowsDelegation("42-login", 0)
	until := func(d time.Duration) time.Time { return w.clk.Now().Add(d) }
	ask := func(by, to characters.Character, mesa string, d time.Duration) (decrees.Sub, error) {
		return w.svc.Subdelegate(w.ctx, decrees.SubRequest{By: by, Grantee: to, Mesa: mesa, Type: "envio_workflows", Until: until(d), Reason: "x"})
	}
	if _, err := ask(gm, tm43, "43-busca", 2*day); refusalCode(t, err) != decrees.CodeTooWide {
		t.Fatalf("another table: %v", err)
	}
	if _, err := ask(gm, tm42, "42-login", 40*day); refusalCode(t, err) != decrees.CodeTooWide {
		t.Fatalf("40 days: %v", err)
	}
	if _, err := ask(tm42, tm42, "42-login", 2*day); refusalCode(t, err) != decrees.CodeLevelDenied {
		t.Fatalf("the Mestre da mesa subdelegating: %v", err)
	}
	if _, err := ask(gm, tm42, "42-login", 40*day); err == nil {
		t.Fatal("40 days accepted")
	}
	sub, err := ask(gm, tm42, "42-login", 2*day)
	if err != nil || !sub.Active || sub.Origin != origin.Number || sub.Grantee != "tm42" {
		t.Fatalf("sub = %+v, %v", sub, err)
	}
	req := w.risky(8, classes.RequestWorkflows, classes.RiskHigh)
	covered := func(c characters.Character) bool {
		ok, err := w.svc.SubCovers(w.ctx, req, c.ID)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if !covered(tm42) || covered(tm43) {
		t.Fatal("the subdelegation must cover its Mestre da mesa and nobody else")
	}
	if err := w.svc.SubUsed(w.ctx, req, "tm42"); err != nil {
		t.Fatal(err)
	}
	if n, _ := w.svc.SubUses(w.ctx, sub.ID); n != 1 {
		t.Fatalf("uses = %d", n)
	}
	if err := w.svc.Revoke(w.ctx, origin.Number, w.owner.ID, ""); err != nil {
		t.Fatal(err)
	}
	if covered(tm42) {
		t.Fatal("the subdelegation outlived its origin")
	}
	if got, _ := w.svc.Sub(w.ctx, sub.ID); got.Active {
		t.Fatalf("sub still active: %+v", got)
	}
}

func TestASubdelegationEndsWithItsValidityAndItsTable(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	open := true
	svc := w.withHooks(nil, nil, func(context.Context, string, string) (bool, error) { return open, nil })
	w.workflowsDelegation("42-login", 0)
	req := w.risky(8, classes.RequestWorkflows, classes.RiskHigh)
	sub, err := svc.Subdelegate(w.ctx, decrees.SubRequest{By: gm, Grantee: tm42, Mesa: "42-login", Type: "envio_workflows", Until: w.clk.Now().Add(2 * day)})
	if err != nil {
		t.Fatal(err)
	}
	covered := func() bool { ok, _ := svc.SubCovers(w.ctx, req, "tm42"); return ok }
	if !covered() {
		t.Fatal("not covered")
	}
	open = false
	if covered() {
		t.Fatal("covered after the table closed")
	}
	open = true
	w.clk.Advance(3 * day)
	if covered() {
		t.Fatal("covered after the validity")
	}
	open = false
	if _, err := svc.Subdelegate(w.ctx, decrees.SubRequest{By: gm, Grantee: tm42, Mesa: "42-login", Type: "envio_workflows", Until: w.clk.Now().Add(day)}); refusalCode(t, err) != decrees.CodeTooWide {
		t.Fatalf("closed table: %v", err)
	}
	_ = sub
}

// A subdelegation of a type alto without a delegation of the Owner has no
// origin: refused. A medio comes from the ceiling of the Grand Master.
func TestASubdelegationOfAHighTypeNeedsTheDelegationAndAMedioDoesNot(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	until := w.clk.Now().Add(day)
	if _, err := w.svc.Subdelegate(w.ctx, decrees.SubRequest{By: gm, Grantee: tm42, Mesa: "42-login", Type: "envio_workflows", Until: until}); refusalCode(t, err) != decrees.CodeTooWide {
		t.Fatalf("no origin: %v", err)
	}
	if _, err := w.svc.Subdelegate(w.ctx, decrees.SubRequest{By: gm, Grantee: tm42, Mesa: "42-login", Type: "entrada_na_guilda", Until: until}); refusalCode(t, err) != decrees.CodeTooWide {
		t.Fatalf("fixed type: %v", err)
	}
	sub, err := w.svc.Subdelegate(w.ctx, decrees.SubRequest{By: gm, Grantee: tm42, Mesa: "42-login", Type: "merge", Until: until})
	if err != nil || sub.Origin != 0 {
		t.Fatalf("medio = %+v, %v", sub, err)
	}
	req := w.risky(9, classes.RequestMerge, classes.RiskMedium)
	if ok, _ := w.svc.SubCovers(w.ctx, req, "tm42"); !ok {
		t.Fatal("a medio subdelegation does not cover")
	}
	// the Grand Master that gave it, or an Administrator, revokes it
	if err := w.svc.RevokeSub(w.ctx, sub.ID, "other", false); err != decrees.ErrNotAllowed {
		t.Fatalf("revoked by a stranger: %v", err)
	}
	if err := w.svc.RevokeSub(w.ctx, sub.ID, gm.ID, false); err != nil {
		t.Fatal(err)
	}
	if ok, _ := w.svc.SubCovers(w.ctx, req, "tm42"); ok {
		t.Fatal("covers after the revocation")
	}
}

// Part of CA-DELEG-05 (over fakes of the hooks; the real rules and the bot are in
// internal/telegram/delegation_test.go): the emergency button ends every
// delegation and subdelegation of
// every workspace, in the audit with who and when, tells the workspaces and
// takes the open requests up; an Administrator is refused.
func TestRevokeAllEndsEveryDelegationAndIsTheOwnersOnly(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	h := &hooks{rules: map[string]int{"loja": 2, "blog": 1}}
	svc := w.withHooks(nil, h, nil)
	ana := w.person("Ana", users.Administrator, "loja", "blog")
	d1 := w.workflowsDelegation("42-login", 0)
	blog := w.confirm(decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "blog", Mesa: "1-x",
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "blog"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "configuracao_repositorio"}, Text: "delegue"})
	keep := w.confirm(allowCmd(w.owner, "dev", "make test"))
	sub, err := w.svc.Subdelegate(w.ctx, decrees.SubRequest{By: gm, Grantee: tm42, Mesa: "42-login", Type: "envio_workflows", Until: w.clk.Now().Add(day)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RevokeAllDelegations(w.ctx, ana.ID); err != decrees.ErrNotAllowed {
		t.Fatalf("Administrator: %v", err)
	}
	if got, _ := w.svc.Get(w.ctx, d1.Number); got.State != decrees.Active {
		t.Fatalf("a refused button changed %+v", got)
	}
	w.clk.Advance(time.Minute)
	rep, err := svc.RevokeAllDelegations(w.ctx, w.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Decrees) != 2 || rep.Subdelegations != 1 || rep.Rules["loja"] != 2 || rep.Rules["blog"] != 1 || rep.By.ID != w.owner.ID || !rep.At.Equal(w.clk.Now()) {
		t.Fatalf("report = %+v", rep)
	}
	for _, n := range []int64{d1.Number, blog.Number} {
		got, _ := w.svc.Get(w.ctx, n)
		if got.State != decrees.Revoked {
			t.Fatalf("decree %d = %+v", n, got)
		}
		hist, _ := w.svc.History(w.ctx, n)
		if last := hist[len(hist)-1]; last.By != w.owner.ID || !last.At.Equal(w.clk.Now()) {
			t.Fatalf("history of %d = %+v", n, hist)
		}
	}
	if got, _ := w.svc.Get(w.ctx, keep.Number); got.State != decrees.Active {
		t.Fatal("a rule that is not a delegation was revoked")
	}
	if got, _ := w.svc.Sub(w.ctx, sub.ID); got.Active || got.EndedBy != w.owner.ID {
		t.Fatalf("sub = %+v", got)
	}
	if len(h.told["loja"]) != 1 || len(h.told["blog"]) != 1 || len(h.rerouted) != 2 {
		t.Fatalf("told = %+v, rerouted = %v", h.told, h.rerouted)
	}
	var n int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM delegation_revocations WHERE by_user = ?`, w.owner.ID).Scan(&n); err != nil || n != 1 {
		t.Fatalf("audit rows = %d, %v", n, err)
	}
	if w.covers(destructive(w, 3, "42-login")) {
		t.Fatal("covers after the revocation")
	}
	_ = json.RawMessage(nil)
}

// CA-DELEG-06: the types that are never delegated (the entry in the Guild, and
// the extra cost, which a delegation would only ever open above the ceiling:
// below it the type is medio and a decree of policy is the way) are refused
// before the confirmation, with "tipo não delegável"; nothing is left to confirm.
func TestTypesThatAreNeverDelegatedAreRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, typ := range []string{"entrada_na_guilda", "custo_extra", "promocao_cidadela", "atualizacao_mestre"} {
		p := decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: "42-login", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
			Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: typ}, Text: "delegue"}
		ref := w.refusal(p)
		if ref.Code != decrees.CodeDelegation {
			t.Fatalf("%s: %+v", typ, ref)
		}
		contains(t, ref.Message, "tipo não delegável")
		contains(t, ref.Message, typ)
	}
	if n := w.count("decree_proposals"); n != 0 {
		t.Fatalf("%d proposals left to confirm", n)
	}
	if n := w.count("decrees"); n != 0 {
		t.Fatalf("%d decrees", n)
	}
	// the same proposal for a type alto is not refused: the refusal is of the type
	ok := decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: "42-login", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
		Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Text: "delegue"}
	if _, err := w.svc.Propose(w.ctx, ok); err != nil {
		t.Fatalf("a type alto: %v", err)
	}
}

// A subdelegation is never more than the Grão-Mestre has: a medio one does not
// cover an alto request, and a class permission is not subdelegable.
func TestASubdelegationIsNeverWiderThanTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	until := w.clk.Now().Add(day)
	if _, err := w.svc.Subdelegate(w.ctx, decrees.SubRequest{By: gm, Grantee: tm42, Mesa: "42-login", Type: "permissao_de_classe", Until: until}); refusalCode(t, err) != decrees.CodeTooWide {
		t.Fatalf("class permission: %v", err)
	}
	if _, err := w.svc.Subdelegate(w.ctx, decrees.SubRequest{By: gm, Grantee: tm42, Mesa: "42-login", Type: "merge", Until: until}); err != nil {
		t.Fatal(err)
	}
	covers := func(r chain.Request) bool { ok, _ := w.svc.SubCovers(w.ctx, r, "tm42"); return ok }
	if !covers(w.risky(1, classes.RequestMerge, classes.RiskMedium)) {
		t.Fatal("the medio is not covered")
	}
	if covers(w.risky(2, classes.RequestMerge, classes.RiskHigh)) {
		t.Fatal("a medio subdelegation covers an alto request")
	}
}

// The validity a delegation gets and the time of its reminder are those of the
// policy file of its workspace (`delegacao`), never more than 30 days; another
// workspace keeps the constants.
func TestTheValidityAndTheReminderOfADelegationComeFromThePolicyOfItsWorkspace(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	rem := &reminders{}
	svc := w.withHooks(rem, nil, nil)
	times := func(_ context.Context, ws string) (time.Duration, time.Duration) {
		if ws == "loja" {
			return 10 * day, 2 * day
		}
		return 90 * day, 0 // more than the 30 is not given
	}
	svc.SetDelegationTimes(times)
	w.svc.SetDelegationTimes(times)
	d := w.confirm(decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: "42-login",
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Text: "delegue os workflows"})
	if want := w.clk.Now().Add(10 * day); !d.Until.Equal(want) {
		t.Fatalf("until = %v, want %v", d.Until, want)
	}
	// another workspace asks for more than the 30 days the file may give: it gets 30
	other := w.confirm(decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "blog", Mesa: "7-post",
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "blog"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Text: "delegue os workflows"})
	if want := w.clk.Now().Add(30 * day); !other.Until.Equal(want) {
		t.Fatalf("another workspace: until = %v, want the 30 days at most (%v)", other.Until, want)
	}
	// the renewal gives the validity of the workspace
	pend, err := svc.Renew(w.ctx, d.Number, w.owner.ID)
	if err != nil || !strings.Contains(pend.Text, "29/10/2026") {
		t.Fatalf("renew = %+v, %v; want the 10 days of the workspace", pend, err)
	}
	w.clk.Advance(7 * day) // 3 days left: the reminder of the workspace is 2
	if err := svc.Sweep(w.ctx); err != nil || rem.n() != 0 {
		t.Fatalf("reminded too early: %d, %v", rem.n(), err)
	}
	w.clk.Advance(1 * day)
	if err := svc.Sweep(w.ctx); err != nil || rem.n() != 1 {
		t.Fatalf("reminders = %d, %v", rem.n(), err)
	}
}
