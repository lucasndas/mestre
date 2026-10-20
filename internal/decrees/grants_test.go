package decrees_test

import (
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/users"
)

func (w *world) request(id int64, typ classes.RequestType, a *classes.Action) chain.Request {
	r := chain.Request{ID: id, Workspace: "loja", Table: "42-login", Type: typ, Requester: "d1", Class: "dev", Action: a, About: "o pedido"}
	w.reqs[id] = r
	return r
}

// "Aprovar agora" makes a one-time exception tied to the exact action, used at
// its first use; the same approval applied twice makes one.
func TestAnApprovalMakesAnExceptionOfTheExactAction(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	bia := w.person("Bia", users.Approver, "loja")
	a := shell("npm install x")
	r := w.request(1, classes.RequestClassPermission, &a)
	g := chain.ExceptionGrant{Request: r.ID, Character: "d1", Action: a, By: bia.ID, Level: chain.Level{Kind: chain.LevelPeople}}
	if err := w.svc.Exception(w.ctx, g); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Exception(w.ctx, g); err != nil {
		t.Fatalf("the same approval twice: %v", err)
	}
	if w.count("decrees") != 1 {
		t.Fatalf("%d exceptions", w.count("decrees"))
	}
	e := w.engine(t)
	if ans := e.run(t, "npm install y"); ans.Verdict != hook.VerdictDeny {
		t.Fatalf("another action: %+v", ans)
	}
	if ans := e.run(t, "npm install x"); ans.Verdict != hook.VerdictAllow || ans.Decree == 0 {
		t.Fatalf("the exact action: %+v", ans)
	}
	// an observer, a suspended user or a stranger does not make one
	obs := w.person("Olga", users.Observer, "loja")
	g.Request, g.By = 2, obs.ID
	w.request(2, classes.RequestClassPermission, &a)
	if err := w.svc.Exception(w.ctx, g); err == nil {
		t.Fatal("an Observer made an exception")
	}
	// the exception of an agent only turns an ask into allow
	w.request(3, classes.RequestClassPermission, &a)
	if err := w.svc.Exception(w.ctx, chain.ExceptionGrant{Request: 3, Character: "d1", Action: shell("make z"), By: "gm", Level: chain.Level{Kind: chain.LevelGrandMaster}}); err != nil {
		t.Fatal(err)
	}
	decs, _ := w.svc.Decrees(w.ctx, dev("d1"))
	var agent *classes.Decree
	for i := range decs {
		if decs[i].Level == classes.LevelGrandMaster {
			agent = &decs[i]
		}
	}
	if agent == nil || agent.Kind != classes.DecreeException {
		t.Fatalf("%+v", decs)
	}
}

// Part of CA-DEC-11 (the decrees: the answers of the people)
func TestTheAnswersOfAPersonMakeRulesAndDenyAlwaysAsksNoPIN(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	caio := w.person("Caio", users.Approver, "loja")
	a := shell("curl https://exemplo.io/x")
	r := w.request(1, classes.RequestClassPermission, &a)
	deny := chain.DecreeGrant{Request: r.ID, Answer: chain.AnswerDenyAlways, Character: "d1", Class: "dev", Table: "42-login", By: ana.ID, Level: chain.Level{Kind: chain.LevelPeople}, Pattern: "curl *"}
	if err := w.svc.Decree(w.ctx, deny); err != nil {
		t.Fatal(err)
	}
	ds, _ := w.svc.List(w.ctx, ana.ID)
	if len(ds) != 1 || ds[0].PIN || ds[0].Kind != decrees.KindRule || ds[0].Effect.Deny == nil || ds[0].Effect.Deny.Commands[0] != "curl *" || ds[0].Level != classes.LevelAdmin {
		t.Fatalf("%+v", ds)
	}
	// an Approver makes no rule, with the level read again at the answer
	deny.By = caio.ID
	if err := w.svc.Decree(w.ctx, deny); err == nil {
		t.Fatal("an Approver made a rule")
	}
	// "por um tempo" and "o padrão": the rule has validity, or the pattern
	tmp := chain.DecreeGrant{Request: r.ID, Answer: chain.AnswerApproveFor, Character: "d1", Class: "dev", By: ana.ID, For: 3 * time.Hour}
	if err := w.svc.Decree(w.ctx, tmp); err != nil {
		t.Fatal(err)
	}
	pat := chain.DecreeGrant{Request: r.ID, Answer: chain.AnswerApprovePattern, Character: "d1", Class: "dev", By: ana.ID, Pattern: "make *", For: time.Hour}
	if err := w.svc.Decree(w.ctx, pat); err != nil {
		t.Fatal(err)
	}
	ds, _ = w.svc.List(w.ctx, ana.ID)
	if len(ds) != 3 || ds[1].Effect.Allow.Commands[0] != "curl https://exemplo.io/x" || ds[1].Until != w.clk.Now().Add(3*time.Hour) || ds[2].Effect.Allow.Commands[0] != "make *" {
		t.Fatalf("%+v", ds)
	}
	// an answer that makes no decree, or has no class, is refused
	if err := w.svc.Decree(w.ctx, chain.DecreeGrant{Request: r.ID, Answer: chain.AnswerApprove, Class: "dev", By: ana.ID}); err == nil {
		t.Fatal("approve made a decree")
	}
	if err := w.svc.Decree(w.ctx, chain.DecreeGrant{Request: r.ID, Answer: chain.AnswerDenyAlways, By: ana.ID}); err == nil {
		t.Fatal("a rule without a class")
	}
	// the invariants hold for the answers too: no rule frees the vault
	v := shell("mestre vault get x")
	r2 := w.request(2, classes.RequestClassPermission, &v)
	if err := w.svc.Decree(w.ctx, chain.DecreeGrant{Request: r2.ID, Answer: chain.AnswerApproveAlways, Character: "d1", Class: "dev", By: ana.ID}); err == nil {
		t.Fatal("a rule freed the vault")
	}
}

// A decree of delegation of the Owner lets the Grand Master decide a type of
// risk alto in its scope, and only then.
func TestADelegationOfTheOwnerCoversTheRequestsInItsScope(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	req := func(id int64, typ classes.RequestType, table, repo string) chain.Request {
		r := w.request(id, typ, nil)
		r.Table = table
		r.Target = json.RawMessage(`{"repo":"` + repo + `"}`)
		return r
	}
	cover := func(r chain.Request) bool {
		t.Helper()
		ok, err := w.svc.Covers(w.ctx, r)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if cover(req(1, classes.RequestRepoSettings, "42-login", "lucas/loja")) {
		t.Fatal("covered without a delegation")
	}
	d := w.confirm(decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: "42-login", Scope: "repo:lucas/loja",
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "configuracao_repositorio"}, Text: "delegue"})
	if !d.PIN {
		t.Fatalf("a delegation asks the PIN: %+v", d)
	}
	if !cover(req(2, classes.RequestRepoSettings, "42-login", "lucas/loja")) {
		t.Fatal("the delegation did not cover its own scope")
	}
	if cover(req(3, classes.RequestRepoSettings, "43-api", "lucas/loja")) {
		t.Fatal("covered another table")
	}
	if cover(req(4, classes.RequestRepoSettings, "42-login", "lucas/outro")) {
		t.Fatal("covered another repository")
	}
	if cover(req(5, classes.RequestWorkflows, "42-login", "lucas/loja")) {
		t.Fatal("covered another type")
	}
	if uses, _ := w.svc.Uses(w.ctx, d.Number); len(uses) != 0 {
		t.Fatalf("asking is not using: %+v", uses)
	}
	if err := w.svc.Used(w.ctx, req(9, classes.RequestRepoSettings, "42-login", "lucas/loja"), "grao-mestre"); err != nil {
		t.Fatal(err)
	}
	if uses, _ := w.svc.Uses(w.ctx, d.Number); len(uses) != 1 || uses[0].Character != "grao-mestre" {
		t.Fatalf("the use is registered in the decree: %+v", uses)
	}
	// an Administrator can not delegate; types that never are delegated are refused
	del := decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
		Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "configuracao_repositorio"}, Text: "delegue"}
	if ref := w.refusal(del); ref.Code != decrees.CodeLevel {
		t.Fatalf("%+v", ref)
	}
	for _, typ := range []string{"entrada_na_guilda", "promocao_cidadela", "atualizacao_mestre", "custo_extra"} {
		p := del
		p.Author = w.owner.ID
		p.Effect.Setting = typ
		if ref := w.refusal(p); ref.Code != decrees.CodeDelegation {
			t.Fatalf("%s: %+v", typ, ref)
		}
	}
	// a delegation ends with its validity and when it is revoked
	d2 := w.confirm(decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: "42-login", Until: w.clk.Now().Add(time.Hour),
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Text: "delegue os workflows"})
	if !cover(req(6, classes.RequestWorkflows, "42-login", "lucas/loja")) {
		t.Fatal("not covered")
	}
	w.clk.Advance(2 * time.Hour)
	if cover(req(7, classes.RequestWorkflows, "42-login", "lucas/loja")) {
		t.Fatal("covered after the validity")
	}
	if err := w.svc.Revoke(w.ctx, d.Number, w.owner.ID, ""); err != nil {
		t.Fatal(err)
	}
	if cover(req(8, classes.RequestRepoSettings, "42-login", "lucas/loja")) {
		t.Fatal("covered after the revocation")
	}
	_ = d2
}

// The decree of delegation of the activation of the Operator (the approval
// itself comes with the Operator, in the M7).
// Part of CA-DEC-10
func TestTheDelegationOfTheOperatorNeedsTheOwnerAScopeAndAValidity(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	p := func(author users.User, scope string, until time.Duration) decrees.Proposal {
		q := decrees.Proposal{Author: author.ID, Kind: decrees.KindRule, Workspace: "loja", Scope: scope,
			Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "ativacao_operador", MaxMinutes: 30},
			Text: "o grão-mestre pode ativar o operador"}
		if until > 0 {
			q.Until = w.clk.Now().Add(until)
		}
		return q
	}
	day := 24 * time.Hour
	for name, c := range map[string]struct {
		p    decrees.Proposal
		code string
	}{
		"an Administrator":   {p(ana, "classe:operador", 3*day), decrees.CodeLevel},
		"no scope":           {p(w.owner, "", 3*day), decrees.CodeDelegation},
		"an empty scope":     {p(w.owner, "classe:", 3*day), decrees.CodeDelegation},
		"a scope of a repo":  {p(w.owner, "repo:lucas/loja", 3*day), decrees.CodeDelegation},
		"no validity":        {p(w.owner, "classe:operador", 0), decrees.CodeDelegation},
		"ten days":           {p(w.owner, "classe:operador", 10*day), decrees.CodeDelegation},
		"longer activations": {func() decrees.Proposal { q := p(w.owner, "classe:operador", 3*day); q.Effect.MaxMinutes = 45; return q }(), decrees.CodeDelegation},
	} {
		if ref := w.refusal(c.p); ref.Code != c.code {
			t.Errorf("%s: %+v; want %s", name, ref, c.code)
		}
	}
	d := w.confirm(p(w.owner, "classe:operador", 3*day))
	if !d.PIN || d.Effect.MaxMinutes != 30 || d.Scope != "classe:operador" {
		t.Fatalf("%+v", d)
	}
	if w.count("decree_proposals") != 1 {
		t.Fatalf("the refused proposals were kept: %d", w.count("decree_proposals"))
	}
}

// CA-APROV-POL-13: the policy decrees: a tightening reaches any type, a loosening only the
// ones the Grand Master may decide.
func TestAPolicyDecreeNeverLoosensAHighType(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	pol := func(setting string, loosens bool) decrees.Proposal {
		return decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
			Effect: decrees.Effect{Kind: decrees.EffectPolicy, Setting: setting, Loosens: loosens}, Text: "ajuste a política"}
	}
	ref := w.refusal(pol("acao_destrutiva", true))
	if ref.Code != decrees.CodePolicy {
		t.Fatalf("%+v", ref)
	}
	contains(t, ref.Message, "delegation")
	for _, typ := range []string{"configuracao_repositorio", "envio_workflows", "ativacao_operador", "entrada_na_guilda"} {
		if ref := w.refusal(pol(typ, true)); ref.Code != decrees.CodePolicy {
			t.Fatalf("%s: %+v", typ, ref)
		}
	}
	if d := w.confirm(pol("acao_destrutiva", false)); d.State != decrees.Active {
		t.Fatalf("%+v", d)
	}
	// "me pergunte tudo" holds for a type alto: the rules the chain reads, with the
	// decrees that reach the request, send it to the people
	destructive := w.request(9, classes.RequestDestructive, nil)
	adj, err := w.svc.PolicyFor(w.ctx, destructive)
	if err != nil || chain.DefaultRules().WithDecrees(adj).AsksAlways(destructive) == "" {
		t.Fatalf("the decree that tightens acao_destrutiva does not reach the request: %+v, %v", adj, err)
	}
	if d := w.confirm(pol("migracoes", true)); d.State != decrees.Active {
		t.Fatalf("%+v", d)
	}
	if _, err := w.svc.Propose(w.ctx, pol("", false)); err == nil {
		t.Fatal("a policy without a setting")
	}
}

func TestNewNeedsItsPieces(t *testing.T) {
	t.Parallel()
	if _, err := decrees.New(decrees.Options{}); err == nil {
		t.Fatal("no database")
	}
}

// The decrees of policy that reach a request are those of its workspace, its
// table and its class, still valid: the chain reads them (chain.PolicyDecrees).
func TestThePolicyDecreesThatReachARequestAreThoseOfItsWorkspaceTableAndClass(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	pol := func(target decrees.Target, mesa, setting string, loosens bool, until time.Time) decrees.Proposal {
		return decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: mesa, Target: target, Until: until,
			Effect: decrees.Effect{Kind: decrees.EffectPolicy, Setting: setting, Loosens: loosens}, Text: "ajuste a política"}
	}
	r := w.request(1, classes.RequestPR, nil) // loja, 42-login, class dev
	w.confirm(pol(decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, "", "migracoes", true, time.Time{}))
	w.confirm(pol(decrees.Target{Kind: decrees.TargetTable, Name: "42-login"}, "42-login", "*", false, w.clk.Now().Add(time.Hour)))
	w.confirm(pol(decrees.Target{Kind: decrees.TargetClass, Name: "dev"}, "", "pr", false, time.Time{}))
	// not reaching this request: another table, another class
	w.confirm(pol(decrees.Target{Kind: decrees.TargetTable, Name: "43-busca"}, "43-busca", "infraestrutura", true, time.Time{}))
	w.confirm(pol(decrees.Target{Kind: decrees.TargetClass, Name: "qa"}, "", "pagamentos", true, time.Time{}))
	got, err := w.svc.PolicyFor(w.ctx, r)
	if err != nil || !slices.Equal(got.Loosen, []string{"migracoes"}) || !slices.Equal(got.Tighten, []string{"*", "pr"}) {
		t.Fatalf("PolicyFor = %+v, %v", got, err)
	}
	w.clk.Advance(2 * time.Hour) // the decree with a validity ended
	got, err = w.svc.PolicyFor(w.ctx, r)
	if err != nil || !slices.Equal(got.Tighten, []string{"pr"}) {
		t.Fatalf("after the validity: %+v, %v", got, err)
	}
	other := r
	other.Workspace = "blog"
	if got, _ := w.svc.PolicyFor(w.ctx, other); len(got.Loosen)+len(got.Tighten) != 0 {
		t.Fatalf("another workspace: %+v", got)
	}
}

// The reach of a decree of policy is the one it was made for: a decree made for a
// table loosens only that table, the loosening of an author who was suspended
// ends, a decree aimed at a character or at a setting that is not one is refused,
// and what a real decree loosens is what the rules of the chain stop asking.
func TestADecreeOfPolicyReachesOnlyWhatItWasMadeForAndEndsWithItsAuthor(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	pol := func(setting string, loosens bool, target decrees.Target, mesa string) decrees.Proposal {
		return decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: mesa, Target: target,
			Effect: decrees.Effect{Kind: decrees.EffectPolicy, Setting: setting, Loosens: loosens}, Text: "ajuste a política"}
	}
	workspace := decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}
	// made for the table 42-login, aimed at the workspace (the answer "Delegar" of a medio request)
	w.confirm(pol("migracoes", true, workspace, "42-login"))
	inside := w.request(1, classes.RequestPR, nil) // loja, 42-login
	got, err := w.svc.PolicyFor(w.ctx, inside)
	if err != nil || !slices.Equal(got.Loosen, []string{"migracoes"}) {
		t.Fatalf("the table of the decree: %+v, %v", got, err)
	}
	other := inside
	other.Table = "43-busca"
	if got, _ := w.svc.PolicyFor(w.ctx, other); len(got.Loosen) != 0 {
		t.Fatalf("another table of the workspace is loosened too: %+v", got)
	}
	// what the rules of the chain stop asking
	pr := chain.PRFacts{LabelsRead: true, CIGreen: true, ReviewDone: true, Files: []chain.PRFile{{Path: "db/migrations/0003.sql", Added: 3}}}
	if why := chain.DefaultRules().Conditions(inside, pr, true); len(why) == 0 {
		t.Fatal("a migration passes the conditions without the decree")
	}
	loosened, _ := w.svc.PolicyFor(w.ctx, inside)
	if why := chain.DefaultRules().WithDecrees(loosened).Conditions(inside, pr, true); len(why) != 0 {
		t.Fatalf("the decree loosened migracoes but the rules still ask: %v", why)
	}
	// the author is suspended: the loosening ends (a tightening would stay)
	if err := w.users.Suspend(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.PolicyFor(w.ctx, inside); len(got.Loosen) != 0 {
		t.Fatalf("the decree of a suspended author still loosens: %+v", got)
	}

	// refused at the proposal
	bia := w.person("Bia", users.Administrator, "loja")
	bad := func(p decrees.Proposal) {
		t.Helper()
		p.Author = bia.ID
		if ref := w.refusal(p); ref.Code != decrees.CodeInvalid && ref.Code != decrees.CodeInvariant {
			t.Errorf("%+v: %+v", p.Effect, ref)
		}
	}
	bad(pol("*", false, decrees.Target{Kind: decrees.TargetCharacter, Name: "Dev"}, ""))
	bad(pol("migracoes", false, workspace, "")) // an area is not tightened by name
	bad(pol("mestre", true, workspace, ""))     // the areas of the engine are never opened
	bad(pol("permissoes_e_segredos", true, workspace, ""))
	bad(pol("tipo_que_nao_existe", true, workspace, ""))
}

// The chain names the person that answered "user:<id>" (chain.Service.DecideByPerson):
// the decree and the exception are made for that user, at the level it has.
func TestTheAnswerOfAPersonNamedByTheChainMakesTheDecreeOfThatUser(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	a := shell("curl https://exemplo.io/x")
	r := w.request(1, classes.RequestClassPermission, &a)
	people := chain.Level{Kind: chain.LevelPeople, Name: ana.ID}
	if err := w.svc.Decree(w.ctx, chain.DecreeGrant{Request: r.ID, Answer: chain.AnswerDenyAlways, Character: "d1", Class: "dev", By: "user:" + ana.ID, Level: people, Pattern: "curl *"}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Exception(w.ctx, chain.ExceptionGrant{Request: r.ID, Character: "d1", Action: a, By: "user:" + ana.ID, Level: people}); err != nil {
		t.Fatal(err)
	}
	ds, err := w.svc.List(w.ctx, ana.ID)
	if err != nil || len(ds) != 2 {
		t.Fatalf("decrees of Ana = %+v, %v", ds, err)
	}
	for _, d := range ds {
		if d.Author != ana.ID || d.Level != classes.LevelAdmin {
			t.Fatalf("decree %+v, want Ana's at her level", d)
		}
	}
}
