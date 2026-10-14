package classes_test

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

func parse(t testing.TB, yaml string) *classes.Permissions {
	t.Helper()
	p, err := classes.ParsePermissions([]byte(yaml))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// CA-PERM-10
func TestBuildCutsByTheCeilingAndHardensByTheSheet(t *testing.T) {
	t.Parallel()
	class := parse(t, `version: 1
commands: {allow: ["curl *", "npm test", "git commit *"], default: allow}
git: {commit: true, branches: ["{mesa.branch}"]}
`)
	ceiling := parse(t, "version: 1\ncommands: {deny: [\"curl *\"]}\n")
	sheet := parse(t, "version: 1\ncommands: {deny: [\"git commit *\"]}\n")
	r, err := classes.Build(classes.Layers{
		AgentID: agentID, Class: "dev", Permissions: class, Ceiling: ceiling, Sheet: sheet,
		Placeholders: map[string]string{"mesa.branch": "epico/42-login"}, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(r.Commands.Deny, "curl *") || r.Origin["commands.deny:curl *"] != classes.LayerCeiling {
		t.Fatalf("commands.deny %v, origin %v; want curl * from the ceiling", r.Commands.Deny, r.Origin)
	}
	if r.Git.Commit {
		t.Fatal("git.commit is true; the sheet denies git commit *")
	}
	if r.Origin["git.commit"] != classes.LayerSheet || r.Origin["commands.deny:git commit *"] != classes.LayerSheet {
		t.Fatalf("origin %v; want git.commit and git commit * from the sheet", r.Origin)
	}
	if slices.Contains(r.Commands.Allow, "curl *") {
		t.Fatalf("commands.allow %v still frees curl *", r.Commands.Allow)
	}
	doc, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "{mesa.") || !slices.Equal(r.Git.Branches, []string{"epico/42-login"}) {
		t.Fatalf("placeholder left in %s", doc)
	}
	if _, err := classes.ParseRules(doc); err != nil {
		t.Fatalf("the document does not read back: %v", err)
	}
}

func TestBuildNarrowsWhatTheMaximumDoesNotSurelyHold(t *testing.T) {
	t.Parallel()
	class := parse(t, `version: 1
files: {write: ["**"]}
commands: {allow: ["npm *", "docker *"], default: allow}
network: {internet: true, domains: ["github.com", "example.com"]}
cli: {allow: ["issue comment", "pr open"]}
git: {request_push: true, workflows: allow, commit: true}
operator: true
`)
	ceiling := parse(t, `version: 1
files: {write: ["src/**", "tests/**"], deny: ["infra/**"]}
commands: {allow: ["npm test", "npm run *"], ask: ["npm publish *"]}
network: {domains: ["github.com"]}
cli: {allow: ["issue *"]}
git: {request_push: false, workflows: never}
operator: false
`)
	sheet := parse(t, "version: 1\ncommands: {default: ask}\nnetwork: {domains: [\"gitlab.com\"]}\n")
	r, err := classes.Build(classes.Layers{AgentID: agentID, Class: "dev", Permissions: class, Ceiling: ceiling, Sheet: sheet, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	check := func(name string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Fatalf("%s = %v; want %v", name, got, want)
		}
	}
	check("files.write", r.Files.Write, []string{"src/**", "tests/**"})
	check("files.deny", r.Files.Deny, []string{"infra/**"})
	check("commands.allow", r.Commands.Allow, []string{"npm test", "npm run *"})
	check("commands.ask", r.Commands.Ask, []string{"npm publish *"})
	check("cli.allow", r.CLI.Allow, []string{"issue comment"})
	if r.Commands.Default != classes.Ask || r.Origin["commands.default"] != classes.LayerSheet {
		t.Fatalf("commands.default %s (%s); want ask from the sheet", r.Commands.Default, r.Origin["commands.default"])
	}
	if r.Git.RequestPush || r.Git.Workflows != classes.WorkflowsNever || r.Operator {
		t.Fatalf("git %+v operator %v; the ceiling turns them off", r.Git, r.Operator)
	}
	if r.Origin["operator"] != classes.LayerCeiling || r.Origin["files.write:src/**"] != classes.LayerCeiling {
		t.Fatalf("origin %v", r.Origin)
	}
	// github.com ∩ gitlab.com is nothing: no domain, not "any domain".
	if r.Network.Internet || len(r.Network.Domains) != 0 || r.Origin["network.internet"] != classes.LayerSheet {
		t.Fatalf("network %+v origin %v; want no internet from the sheet", r.Network, r.Origin["network.internet"])
	}
}

func TestBuildRefusesAPlaceholderWithoutValue(t *testing.T) {
	t.Parallel()
	_, err := classes.Build(classes.Layers{AgentID: agentID, Permissions: parse(t, "version: 1\ngit: {branches: [\"{mesa.branch}\"]}\n"), Now: now})
	if err == nil || !strings.Contains(err.Error(), "mesa.branch") {
		t.Fatalf("err = %v; want the placeholder without value", err)
	}
	if _, err := classes.Build(classes.Layers{AgentID: "../x", Permissions: parse(t, "version: 1\n"), Now: now}); err == nil {
		t.Fatal("an agent id with a path separator was accepted")
	}
}

func TestClassDefaults(t *testing.T) {
	t.Parallel()
	r, err := classes.Build(classes.Layers{AgentID: agentID, Permissions: parse(t, "version: 1\n"), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.Files.Read, []string{"**"}) || len(r.Files.Write) != 0 || r.Commands.Default != classes.Ask || r.Tools.Default != classes.Ask ||
		r.Git.Commit || r.Git.Workflows != classes.WorkflowsNever || r.Network.Internet || r.Operator {
		t.Fatalf("defaults %+v", r)
	}
}

// CA-PERM-17
func TestAnAbsentDefaultAsksAndTheCeilingStillCloses(t *testing.T) {
	t.Parallel()
	m := newMachine()
	class := parse(t, "version: 1\nfiles: {write: [\"**\"]}\ncommands: {allow: [\"npm test\"]}\n")
	r, err := classes.Build(classes.Layers{AgentID: agentID, Class: "dev", Permissions: class, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if r.Commands.Default != classes.Ask || r.Tools.Default != classes.Ask {
		t.Fatalf("commands.default %s, tools.default %s; want both ask", r.Commands.Default, r.Tools.Default)
	}
	for _, ev := range []providers.Event{m.shell("foo --bar"), toolEvent(providers.KindMCP, "mcp__x__y")} {
		d := decide(m.env(), r, ev)
		want(t, d, classes.Ask, classes.LayerClass)
		if d.Risk.Risk != classes.RiskMedium {
			t.Fatalf("%s: the approval request has risk %+v; want medio", ev.Tool.NativeName, d.Risk)
		}
	}

	closed := ceiling(t, "version: 1\npermissions: {commands: {default: deny}}\n")
	if err := closed.Fits(class); err != nil {
		t.Fatalf("the class without defaults under a ceiling with commands.default deny: %v; want it saved", err)
	}
	cut, err := classes.Build(classes.Layers{AgentID: agentID, Class: "dev", Permissions: class, Ceiling: closed.Maximum(), Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if cut.Commands.Default != classes.Deny || cut.Origin["commands.default"] != classes.LayerCeiling {
		t.Fatalf("commands.default %s (origin %q); want deny from the ceiling", cut.Commands.Default, cut.Origin["commands.default"])
	}

	d := classes.DecideDegraded(context.Background(), m.env(), r, m.shell("foo --bar"))
	want(t, d, classes.Deny, classes.LayerDegraded)
	if d.Reason != classes.DegradedReason {
		t.Fatalf("degraded: reason %q; want %q", d.Reason, classes.DegradedReason)
	}
}
