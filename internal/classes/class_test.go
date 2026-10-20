package classes_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/providers"
)

var (
	tableQA = classes.ClassKey{Scope: classes.ClassTable, Table: tableID, Name: "qa"}
	wsQA    = classes.ClassKey{Scope: classes.ClassWorkspace, Name: "qa"}
)

// CA-CLASSES-01
func TestCreatingAClassWithoutAppIsRefusedListingTheThreeOptions(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.lib.Save(context.Background(), classes.SaveRequest{
		Name: "qa", Scope: classes.ClassTable, Table: tableID, Files: pkg(t, "qa", "", defaultPerms, nil), By: tableMaster,
	})
	if !errors.Is(err, classes.ErrAppRequired) {
		t.Fatalf("err = %v; want ErrAppRequired", err)
	}
	for _, option := range []string{"`novo`", "`herdar:<classe>`", "`<nome-do-app>`"} {
		if !strings.Contains(err.Error(), option) {
			t.Errorf("the refusal does not list %s: %v", option, err)
		}
	}
	// Refused: nothing was created, in the files or in the database.
	if f.exists(tableQA) {
		t.Error("the folder of the class was created")
	}
	if recs, err := f.records.AllApproved(context.Background(), "loja"); err != nil || len(recs) != 0 {
		t.Errorf("records = %v (%v)", recs, err)
	}
	// A CLASS.md that names its own App does not stand in for --app.
	if _, err := f.lib.Save(context.Background(), classes.SaveRequest{
		Name: "qa", Scope: classes.ClassTable, Table: tableID, Files: pkg(t, "qa", "novo", defaultPerms, nil), By: tableMaster,
	}); !errors.Is(err, classes.ErrAppRequired) {
		t.Errorf("with app in the file but no --app: %v", err)
	}
}

// CA-CLASSES-02
func TestATableClassWithTheNameOfAWorkspaceClassIsRefused(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassWorkspace, App: "novo", Files: pkg(t, "qa", "", defaultPerms, nil), By: admin})

	_, err := f.lib.Save(context.Background(), classes.SaveRequest{
		Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: pkg(t, "qa", "", defaultPerms, nil), By: tableMaster,
	})
	if !errors.Is(err, classes.ErrNameTaken) {
		t.Fatalf("err = %v; want ErrNameTaken", err)
	}
	if f.exists(tableQA) {
		t.Error("the table class was created")
	}
	// Another name is free; and a table class of another table does not
	// meet this one's.
	f.save(classes.SaveRequest{Name: "qa-api", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: pkg(t, "qa-api", "", defaultPerms, nil), By: tableMaster})
	f.save(classes.SaveRequest{Name: "lint", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: pkg(t, "lint", "", defaultPerms, nil), By: tableMaster})
	f.save(classes.SaveRequest{Name: "lint", Scope: classes.ClassTable, Table: "7-outra", App: "novo", Files: pkg(t, "lint", "", defaultPerms, nil), By: classes.Author{Level: classes.LevelTableMaster, ID: "mestre-7", Table: "7-outra"}})
	// The other direction: a workspace class is visible from every table, so
	// a table class of that name stops it too.
	_, err = f.lib.Save(context.Background(), classes.SaveRequest{
		Name: "lint", Scope: classes.ClassWorkspace, App: "novo", Files: pkg(t, "lint", "", defaultPerms, nil), By: admin,
	})
	if !errors.Is(err, classes.ErrNameTaken) {
		t.Errorf("a workspace class over a table class: %v", err)
	}
}

// CA-CLASSES-05
func TestAScriptInATableClassIsReportedByCheckAndTheClassIsNotSaved(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	hooks := "post_tool:\n  - name: formatar\n    run: \"./hooks/formatar.sh\"\n    timeout: 20\n"
	files := pkg(t, "qa", "novo", defaultPerms, map[string]string{"hooks/hooks.yaml": hooks, "hooks/formatar.sh": "#!/bin/sh\nnpm run format\n"})

	// The class arrives in the folder of the table (a git pull): check says
	// what is wrong.
	f.put(tableQA, files)
	rep, err := f.lib.Check(context.Background(), "qa", tableID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid() || !strings.Contains(rep.String(), "classes da mesa não podem ter scripts") && !strings.Contains(rep.String(), "não pode existir numa classe da mesa") {
		t.Fatalf("check did not point out the script:\n%s", rep)
	}
	found := false
	for _, p := range rep.Problems {
		found = found || strings.Contains(p.Where, "formatar") || strings.Contains(p.Msg, "formatar.sh")
	}
	if !found {
		t.Errorf("the problem does not name the script: %v", rep.Problems)
	}

	// It is not saved: it never gets an approved version, the proposal cannot
	// be approved, and the engine has nothing to use.
	if _, err := f.lib.Use(context.Background(), "qa", tableID); !errors.Is(err, classes.ErrNoApproved) {
		t.Errorf("Use = %v; want ErrNoApproved", err)
	}
	pend, err := f.records.Pending(context.Background(), "loja")
	if err != nil || len(pend) != 1 || len(pend[0].Problems) == 0 {
		t.Fatalf("pending = %+v (%v)", pend, err)
	}
	if err := f.lib.Approve(context.Background(), pend[0].ID, pend[0].Hash, tableMaster); !errors.Is(err, classes.ErrProposalInvalid) {
		t.Errorf("approving it: %v; want ErrProposalInvalid", err)
	}
	// Through the Mestre it is refused with the same reason, and no folder
	// is made.
	f2 := newFixture(t)
	_, err = f2.lib.Save(context.Background(), classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: files, By: tableMaster})
	var inv *classes.InvalidError
	if !errors.As(err, &inv) || !errors.Is(err, classes.ErrInvalidClass) {
		t.Fatalf("save err = %v; want an InvalidError", err)
	}
	if f2.exists(tableQA) {
		t.Error("the table class with a script was saved")
	}
	// The same package is fine in the workspace scope, where an Administrator
	// approves the script.
	f2.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassWorkspace, App: "novo", Files: files, By: admin})
}

// CA-CLASSES-07
func TestCheckShowsTheAppBeingCreatedAndThatPreCompactDoesNotRunOnAntigravity(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	hooks := "pre_compact:\n  - name: salvar\n    run: \"echo salvando\"\n    timeout: 5\n"
	f.save(classes.SaveRequest{
		Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo", By: tableMaster,
		Files: pkg(t, "qa", "", defaultPerms, map[string]string{"hooks/hooks.yaml": hooks}),
	})
	rep, err := f.lib.Check(context.Background(), "qa", tableID)
	if err != nil {
		t.Fatal(err)
	}
	if rep.App.Link != "novo" || rep.App.Label != "em criação" {
		t.Errorf("app = %+v; want novo, em criação", rep.App)
	}
	text := rep.String()
	if !strings.Contains(text, "App novo: em criação") {
		t.Errorf("the text has no App line with the state:\n%s", text)
	}
	by := map[providers.ID]classes.ProviderReport{}
	for _, p := range rep.Providers {
		by[p.Provider] = p
	}
	if len(by) != 3 {
		t.Fatalf("providers = %+v", rep.Providers)
	}
	anti := by[providers.Antigravity]
	if len(anti.NotRunning) != 1 || !strings.Contains(anti.NotRunning[0], "pre_compact/salvar") || len(anti.Hooks) != 0 {
		t.Errorf("antigravity: not running %v, running %v; want the pre_compact hook not running", anti.NotRunning, anti.Hooks)
	}
	for _, id := range []providers.ID{providers.ClaudeCode, providers.Codex} {
		p := by[id]
		if len(p.NotRunning) != 0 || len(p.Hooks) != 1 || !strings.Contains(p.Hooks[0], "pre_compact/salvar") {
			t.Errorf("%s: not running %v, running %v; want the hook running", id, p.NotRunning, p.Hooks)
		}
	}
	// An App that is ready reads "pronto"; the heir shows the state of the App
	// it inherits.
	f.apps.mu.Lock()
	f.apps.requested = map[string]classes.AppState{"qa": classes.AppReady}
	f.apps.mu.Unlock()
	f.save(classes.SaveRequest{Name: "qa-api", Scope: classes.ClassTable, Table: tableID, App: "herdar:qa", By: tableMaster, Files: pkg(t, "qa-api", "", defaultPerms, nil)})
	rep, err = f.lib.Check(context.Background(), "qa-api", tableID)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.App.Inherited || rep.App.Origin != "qa" || rep.App.Label != "pronto" {
		t.Errorf("heir app = %+v", rep.App)
	}
}

// CA-CLASSES-08
func TestAValidHandEditOfTheApprovedClassIsOnlyAProposal(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassWorkspace, App: "novo", Files: pkg(t, "qa", "", defaultPerms, nil), By: admin})
	approved, err := f.lib.Use(ctx, "qa", tableID)
	if err != nil {
		t.Fatal(err)
	}

	// A valid hand edit: the class may now write everywhere.
	edited := pkg(t, "qa", "novo", "version: 1\nfiles: {write: [\"**\"]}\ncommands: {allow: [\"npm test\"], default: ask}\n", nil)
	f.put(wsQA, edited)

	// A character of the class starts a turn: the turn_start reaches the hooks
	// of the class of the engine, which look at the class.
	startTurn(t, f)
	got, err := f.lib.Approved(ctx, "qa", tableID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Hash != approved.Hash {
		t.Fatalf("the class in use is %s; want the approved %s", got.Hash, approved.Hash)
	}
	m := newMachine()
	r, err := classes.Build(classes.Layers{AgentID: agentID, Class: "qa", Permissions: got.Permissions, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	want(t, decide(m.env(), r, m.file(providers.KindEdit, m.in("src/a.go"))), classes.Deny, classes.LayerClass)
	want(t, decide(m.env(), r, m.file(providers.KindEdit, m.in("tests/a_test.go"))), classes.Allow, "")

	// The proposal with the change is pending, once, and the users were told.
	pend, err := f.records.Pending(ctx, "loja")
	if err != nil || len(pend) != 1 {
		t.Fatalf("pending = %+v (%v)", pend, err)
	}
	if p := pend[0]; p.Key != wsQA || p.Hash != edited.Hash() || p.Origin != classes.OriginDisk || len(p.Problems) != 0 || string(p.Files[1].Data) != string(edited[1].Data) {
		t.Errorf("proposal = %+v", p)
	}
	startTurn(t, f)
	if pend, _ := f.records.Pending(ctx, "loja"); len(pend) != 1 {
		t.Errorf("a second turn made %d proposals", len(pend))
	}
	if al := f.alerts.all(); len(al) != 1 || al[0].Kind != classes.ClassProposed || al[0].Hash != edited.Hash() || al[0].Approved != approved.Hash {
		t.Errorf("alerts = %+v; want one, of the proposal", al)
	}

	// Approved by an Administrator, the new version is the one in use.
	if err := f.lib.Approve(ctx, pend[0].ID, pend[0].Hash, admin); err != nil {
		t.Fatal(err)
	}
	now2, err := f.lib.Use(ctx, "qa", tableID)
	if err != nil || now2.Hash != edited.Hash() {
		t.Fatalf("after the approval: %v, %v", now2, err)
	}
	if pend, _ := f.records.Pending(ctx, "loja"); len(pend) != 0 {
		t.Errorf("pending after the approval = %+v", pend)
	}
}

// oneCharacter is the registry of the machine for a single character, "qa".
type oneCharacter struct{ rules *classes.Rules }

func (o oneCharacter) Rules(context.Context, string) (*classes.Rules, error) { return o.rules, nil }
func (oneCharacter) Env(context.Context, string) (classes.Env, error)        { return newMachine().env(), nil }

// startTurn is the start of a turn of a character of the class qa, as the
// engine sees it: the turn_start of the dispatcher asks the hooks of the
// class (hook.ClassHookSource), which look at the class.
func startTurn(t *testing.T, f *fixture) {
	t.Helper()
	oc := oneCharacter{rules: rules(t, defaultPerms)}
	src := &hook.ClassHookSource{Classes: f.lib, Rules: oc, Characters: oc}
	_, err := src.Sync(context.Background(), providers.Event{
		Version: providers.EventVersion, Provider: providers.ClaudeCode, Event: providers.EventTurnStart, AgentID: agentID, TableID: tableID,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// Part of CA-CLASSES-06 (the criterion is closed through the approval flow in
// internal/app/classapproval_full_test.go): the rule the flow applies, that the
// Grand Master only proposes a workspace class, never approves what he proposed,
// and that the class only comes into use with the approval of an Administrator.
func TestTheGrandMasterProposesAWorkspaceClassAndOnlyAnAdministratorApprovesIt(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	res, err := f.lib.Save(ctx, classes.SaveRequest{Name: "qa", Scope: classes.ClassWorkspace, App: "novo", Files: pkg(t, "qa", "", defaultPerms, nil), By: grandMaster})
	if err != nil {
		t.Fatal(err)
	}
	if res.Applied || res.Proposal == nil || f.exists(wsQA) {
		t.Fatalf("result = %+v; want a proposal, nothing written", res)
	}
	if al := f.alerts.all(); len(al) != 1 || al[0].Kind != classes.ClassWaiting {
		t.Errorf("alerts = %+v; want the users told that a proposal waits", al)
	}
	p := res.Proposal

	// The Grand Master cannot approve his own proposal (nor can an approver).
	for _, by := range []classes.Author{grandMaster, {Level: classes.LevelApprover, ID: "ze"}, tableMaster} {
		if err := f.lib.Approve(ctx, p.ID, p.Hash, by); err == nil {
			t.Errorf("%s approved a workspace class", by.Level)
		}
	}
	if err := f.lib.Approve(ctx, p.ID, p.Hash, grandMaster); !errors.Is(err, classes.ErrNeedsAdministrator) {
		t.Errorf("err = %v; want ErrNeedsAdministrator", err)
	}
	if _, err := f.lib.Use(ctx, "qa", ""); !errors.Is(err, classes.ErrNoApproved) {
		t.Errorf("the class is in use before the approval: %v", err)
	}
	// The Administrator approves the hash he saw; a different hash is refused.
	if err := f.lib.Approve(ctx, p.ID, "outro-hash", admin); !errors.Is(err, classes.ErrClassChanged) {
		t.Errorf("approving another hash: %v", err)
	}
	if err := f.lib.Approve(ctx, p.ID, p.Hash, admin); err != nil {
		t.Fatal(err)
	}
	if c, err := f.lib.Use(ctx, "qa", ""); err != nil || c.Hash != p.Hash || !f.exists(wsQA) {
		t.Fatalf("after the approval: %v, %v", c, err)
	}
}

// CA-FMAQ-04
func TestAClassWithARequirementThatCannotBeMetIsRefusedWhenSaved(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct{ requires, reason string }{
		"an interface":        {"{gui: true}", "só vale na base `operador`"},
		"an unknown property": {"{cor: azul}", "`cor` desconhecido"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			files := pkg(t, "qa", "", defaultPerms, nil)
			files = withRequires(t, files, c.requires)
			_, err := f.lib.Save(context.Background(), classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: files, By: tableMaster})
			if !errors.Is(err, classes.ErrInvalidClass) || !strings.Contains(err.Error(), c.reason) {
				t.Fatalf("err = %v; want invalid_class with %q", err, c.reason)
			}
			if f.exists(tableQA) {
				t.Error("the class was saved")
			}
			if recs, _ := f.records.AllApproved(context.Background(), "loja"); len(recs) != 0 {
				t.Errorf("records = %+v", recs)
			}
		})
	}
	// A requirement the machine can meet is saved.
	f := newFixture(t)
	files := withRequires(t, pkg(t, "qa", "", defaultPerms, nil), "{gpu: true, os: linux}")
	f.save(classes.SaveRequest{Name: "qa", Scope: classes.ClassTable, Table: tableID, App: "novo", Files: files, By: tableMaster})
}

func withRequires(t testing.TB, files classes.Files, requires string) classes.Files {
	t.Helper()
	data, _ := files.Get("CLASS.md")
	text := strings.Replace(string(data), "---\nTeste", "requires: "+requires+"\n---\nTeste", 1)
	out := append(classes.Files(nil), files...)
	for i := range out {
		if out[i].Path == "CLASS.md" {
			out[i].Data = []byte(text)
		}
	}
	return out
}
