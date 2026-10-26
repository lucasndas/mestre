package classes_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
)

const (
	grandMasterID = "01JQGRANDMASTER"
	tableMasterID = "01JQTABLEMASTER"
)

func orchestrator(t *testing.T, lib *classes.Library, role classes.Level, id string, sheet string) *classes.Rules {
	t.Helper()
	l := classes.OrchestratorLayers{Role: role, AgentID: id, Placeholders: map[string]string{"mesa.branch": "epico/42-login"}}
	if sheet != "" {
		l.Sheet = parse(t, sheet)
	}
	r, err := lib.OrchestratorRules(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// Part of CA-ORQ-01 (the Citadel builder, in internal/app, closes it)
func TestTheOrchestratorsTakeTheirRulesFromTheBuiltInClassInEveryWorkspace(t *testing.T) {
	t.Parallel()
	a, b := newFixture(t), newFixture(t)
	b.lib.Workspace = "outro-workspace" // another workspace, the same ceiling
	var all []*classes.Rules
	for _, f := range []*fixture{a, b} {
		all = append(all,
			orchestrator(t, f.lib, classes.RoleGrandMaster, grandMasterID, ""),
			orchestrator(t, f.lib, classes.RoleTableMaster, tableMasterID, ""))
	}
	for i, r := range all {
		if r.Class != classes.ReservedOrchestrator {
			t.Errorf("rules %d: class = %q; want the built-in %q", i, r.Class, classes.ReservedOrchestrator)
		}
		if len(r.Files.Write) != 0 {
			t.Errorf("rules %d: files.write = %v; want empty", i, r.Files.Write)
		}
		if r.Commands.Default != classes.Ask || r.Tools.Default != classes.Ask {
			t.Errorf("rules %d: commands.default = %s, tools.default = %s; want ask, ask", i, r.Commands.Default, r.Tools.Default)
		}
		if r.Git.Commit != classes.Deny || r.Git.RequestPush != classes.Deny {
			t.Errorf("rules %d: git.commit = %s, git.request_push = %s; want deny, deny", i, r.Git.Commit, r.Git.RequestPush)
		}
	}
	// The two workspaces give the same rules for the same role.
	for i := range 2 {
		x, y := *all[i], *all[i+2]
		if !reflect.DeepEqual(x, y) {
			t.Errorf("role %d: the rules differ between the workspaces:\n%+v\n%+v", i, x, y)
		}
	}
	// And the class the library hands out is the built-in one, whatever the
	// scopes hold.
	got, err := a.lib.Use(context.Background(), classes.ReservedOrchestrator, tableID)
	built, _ := classes.Orchestrator()
	if err != nil || got.Hash != built.Hash || got.Name != classes.ReservedOrchestrator {
		t.Errorf("Use = %+v, %v; want the built-in class", got, err)
	}
}

// CA-ORQ-02
func TestTheMestreDaMesaApprovesOnlyWhatItsOwnRulesWouldNotDeny(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	tm := orchestrator(t, f.lib, classes.RoleTableMaster, tableMasterID, "")
	m := newMachine()
	env := m.env() // the worktree of the character that asks
	character := rules(t, "version: 1\nfiles: {write: [\"**\"]}\n")

	outside := m.file(providers.KindWrite, m.outside)
	if d := decide(env, character, outside); d.Verdict == classes.Deny {
		t.Fatalf("the character itself is denied the write outside the worktree (%s); the test needs it to ask", d.Reason)
	}
	if ok, d := classes.Attenuates(ctx, env, tm, outside); ok || d.Verdict != classes.Deny {
		t.Errorf("a write outside the worktree: approvable = %v (%s); want not approvable, the class does not write", ok, d.Verdict)
	}
	inside := m.shell("npm install x")
	if ok, d := classes.Attenuates(ctx, env, tm, inside); !ok {
		t.Errorf("npm install x in the worktree of the character: not approvable (%s: %s); want approvable", d.Verdict, d.Reason)
	}
}

// CA-ORQ-03
func TestTheBuiltInClassIsNeitherCreatedNorEditedNorProposed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	name := classes.ReservedOrchestrator
	for _, c := range []struct {
		scope classes.ClassScope
		table string
		by    classes.Author
	}{{classes.ClassWorkspace, "", admin}, {classes.ClassTable, tableID, tableMaster}, {classes.ClassWorkspace, "", grandMaster}} {
		_, err := f.lib.Save(ctx, classes.SaveRequest{Name: name, Scope: c.scope, Table: c.table, App: "novo",
			Files: pkg(t, name, "", defaultPerms, nil), By: c.by})
		if !errors.Is(err, classes.ErrNameTaken) {
			t.Errorf("%s by %s: err = %v; want name_taken", c.scope, c.by.Level, err)
		}
	}
	if _, err := f.lib.Save(ctx, classes.SaveRequest{Name: name, Scope: classes.ClassWorkspace, Edit: true, App: "novo", By: admin}); !errors.Is(err, classes.ErrBuiltIn) {
		t.Errorf("editing it: err = %v; want built_in", err)
	}

	// A folder with its name (a git pull) is not listed and is no proposal;
	// the users are told once.
	dir, err := f.roots.Dir(classes.ClassKey{Scope: classes.ClassWorkspace, Name: name})
	if err != nil {
		t.Fatal(err)
	}
	if err := classes.WriteDir(dir, pkg(t, name, "novo", defaultPerms, nil)); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(f.roots.Global, name), 0o755); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		entries, err := f.lib.Scan(ctx, tableID)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.Key.Name == name {
				t.Errorf("the built-in class is listed as editable: %+v", e)
			}
		}
	}
	if pend, err := f.records.Pending(ctx, f.lib.Workspace); err != nil || len(pend) != 0 {
		t.Errorf("proposals = %v, %v; want none", pend, err)
	}
	told := 0
	for _, a := range f.alerts.all() {
		if a.Key.Name == name && a.Kind == classes.ClassInvalid {
			told++
		}
	}
	if told != 2 { // the workspace and the global folder, once each
		t.Errorf("alerts for the folders = %d; want 2", told)
	}

	// A proposal that got to the records anyway is not approved.
	p, _, err := f.records.Propose(ctx, f.lib.Workspace, classes.Proposal{
		Key:  classes.ClassKey{Scope: classes.ClassWorkspace, Name: name},
		Hash: "h", Files: pkg(t, name, "novo", defaultPerms, nil), Origin: classes.OriginRequest, By: "gm",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.lib.Approve(ctx, p.ID, "h", owner); !errors.Is(err, classes.ErrNameTaken) {
		t.Errorf("approving it: err = %v; want name_taken", err)
	}
}

// CA-ORQ-04
func TestTheMestreDaMesaRunsLowRiskDirectlyAndAsksForTheRest(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	tm := orchestrator(t, f.lib, classes.RoleTableMaster, tableMasterID, "")
	m := newMachine()
	env := m.env()
	env.AgentID = tableMasterID
	ev := func(cmd string) providers.Event {
		e := m.shell(cmd)
		e.AgentID = tableMasterID
		return e
	}

	d := decide(env, tm, ev("echo ok"))
	want(t, d, classes.Allow, "", "padrão: risco baixo")
	d = decide(env, tm, ev("pip install x"))
	want(t, d, classes.Ask, "")
	if d.Risk.Risk != classes.RiskMedium {
		t.Errorf("pip install x: risk = %s; want %s", d.Risk.Risk, classes.RiskMedium)
	}
}

// The ceiling and the sheet only restrict the class; the sheet counts in the
// attenuation (docs/classes/classe-orquestrador.md#camadas).
func TestTheCeilingAndTheSheetOnlyRestrictTheOrchestrator(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	f := newFixture(t)
	f.ceiling.set(ceiling(t, "version: 1\npermissions: {files: {write: [\"**\"]}, git: {commit: true}, network: {domains: [\"github.com\"]}}\n"))
	r := orchestrator(t, f.lib, classes.RoleGrandMaster, grandMasterID, "")
	if len(r.Files.Write) != 0 || r.Git.Commit != classes.Deny {
		t.Errorf("a ceiling that allows more widened the class: write = %v, commit = %s", r.Files.Write, r.Git.Commit)
	}
	if !reflect.DeepEqual(r.Network.Domains, []string{"github.com"}) {
		t.Errorf("network.domains = %v; want the ceiling's [github.com]", r.Network.Domains)
	}

	m := newMachine()
	npm := m.shell("npm install x")
	plain := orchestrator(t, f.lib, classes.RoleTableMaster, tableMasterID, "")
	if ok, _ := classes.Attenuates(ctx, m.env(), plain, npm); !ok {
		t.Fatal("without a sheet the Mestre da mesa should be able to approve npm install x")
	}
	sheeted := orchestrator(t, f.lib, classes.RoleTableMaster, tableMasterID, "version: 1\ncommands: {deny: [\"npm install *\"]}\n")
	if ok, d := classes.Attenuates(ctx, m.env(), sheeted, npm); ok || !strings.Contains(d.Reason, "npm install") {
		t.Errorf("with a sheet that denies npm install: approvable = %v (%s); want not approvable", ok, d.Reason)
	}
}

func TestOnlyAnOrchestratorRoleGetsTheBuiltInRules(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	_, err := f.lib.OrchestratorRules(context.Background(), classes.OrchestratorLayers{Role: classes.LevelOwner, AgentID: "x"})
	if err == nil {
		t.Error("the rules of the built-in class were built for a role that is not an orchestrator")
	}
}
