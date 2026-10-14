package classes_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
)

// The defaults of ADR 0025: an absent field follows the provider CLIs in a
// plain terminal.

// CA-PERM-19
func TestADefaultBaixoRunsDirectlyAndAnExplicitAskStillAsks(t *testing.T) {
	t.Parallel()
	m := newMachine()
	r := rules(t, "version: 1\n")
	d := decide(m.env(), r, m.shell("echo hello"))
	if d.Verdict != classes.Allow || !strings.Contains(d.Reason, "padrão: risco baixo") || d.Risk.Risk != classes.RiskLow || !d.ClassHooks {
		t.Fatalf("echo hello: %+v; want allow by the default baixo, through the class hooks", d)
	}
	d = decide(m.env(), r, m.shell("./scripts/x.sh https://evil.example"))
	if d.Verdict != classes.Ask || d.Risk.Risk != classes.RiskMedium {
		t.Fatalf("a medio command without a rule: %+v; want a request", d)
	}
	explicit := rules(t, "version: 1\ncommands: {ask: [\"echo *\"]}\n")
	want(t, decide(m.env(), explicit, m.shell("echo hello")), classes.Ask, classes.LayerClass, "commands.ask")
	// The engine down: the fail-safe holds, the default baixo too.
	deg := classes.DecideDegraded(context.Background(), m.env(), r, m.shell("echo hello"))
	want(t, deg, classes.Deny, classes.LayerDegraded, classes.DegradedReason)
}

// CA-PERM-20
func TestAnAbsentFilesWriteIsTheWorktree(t *testing.T) {
	t.Parallel()
	m := newMachine()
	r := rules(t, "version: 1\n")
	want(t, decide(m.env(), r, m.file(providers.KindWrite, m.in("src/a.ts"))), classes.Allow, "")
	want(t, decide(m.env(), r, m.file(providers.KindWrite, m.in(".git/config"))), classes.Deny, classes.LayerFixed)
	d := decide(m.env(), r, m.file(providers.KindWrite, m.outside))
	if d.Verdict != classes.Ask || d.Risk.Risk == classes.RiskLow {
		t.Fatalf("a write outside the worktree: %+v; want a request, not baixo", d)
	}
	narrow := rules(t, "version: 1\nfiles: {write: [\"tests/**\"]}\n")
	want(t, decide(m.env(), narrow, m.file(providers.KindWrite, m.outside)), classes.Deny, classes.LayerClass, "fora do worktree")
}

// CA-PERM-21
func TestNativeSubagentsRunByDefault(t *testing.T) {
	t.Parallel()
	m := newMachine()
	d := decide(m.env(), rules(t, "version: 1\n"), toolEvent(providers.KindSubagent, "Agent"))
	if d.Verdict != classes.Allow || !strings.Contains(d.Reason, "padrão: risco baixo") {
		t.Fatalf("Agent without a rule: %+v; want allow", d)
	}
	want(t, decide(m.env(), rules(t, "version: 1\ntools: {deny: [\"Agent\"]}\n"), toolEvent(providers.KindSubagent, "Agent")), classes.Deny, classes.LayerClass)
}

// CA-PERM-22
func TestAnAbsentGitCommitIsARequestThatRunsOnTheOwnBranch(t *testing.T) {
	t.Parallel()
	m := newMachine()
	const class = "version: 1\nfiles: {write: [\"tests/**\"]}\ngit: {branches: [\"{mesa.branch}\"]}\n"
	r := rules(t, class)
	if r.Git.Commit != classes.Ask {
		t.Fatalf("git.commit %q; want ask", r.Git.Commit)
	}
	env := m.env()
	env.Git = fakeGit{branch: "epico/42-login", staged: []string{"tests/a.test.ts"}}
	d := decide(env, r, m.shell("git commit -m x"))
	if d.Verdict != classes.Allow || !strings.Contains(d.Reason, "padrão: risco baixo") {
		t.Fatalf("a commit of allowed files on the own branch: %+v; want allow by the default baixo", d)
	}
	env.Git = fakeGit{branch: "epico/42-login", staged: []string{"src/b.ts"}}
	want(t, decide(env, r, m.shell("git commit -m x")), classes.Deny, classes.LayerClass, "src/b.ts")
	env.Git = fakeGit{branch: "main", staged: []string{"tests/a.test.ts"}}
	want(t, decide(env, r, m.shell("git commit -m x")), classes.Deny, classes.LayerClass, "main")
	off := rules(t, "version: 1\ngit: {commit: false, branches: [\"{mesa.branch}\"]}\n")
	want(t, decide(env, off, m.shell("git commit -m x")), classes.Deny, classes.LayerClass, "git.commit")
}

// CA-PERM-23
func TestAnAbsentRequestPushIsARequest(t *testing.T) {
	t.Parallel()
	for yaml, wantPush := range map[string]string{
		"version: 1\n": `"request_push":"ask"`,
		"version: 1\ngit: {request_push: true}\n":  `"request_push":"allow"`,
		"version: 1\ngit: {request_push: false}\n": `"request_push":"deny"`,
	} {
		doc, err := json.Marshal(rules(t, yaml).Git)
		if err != nil || !strings.Contains(string(doc), wantPush) {
			t.Errorf("%q: git %s (%v); want %s", yaml, doc, err, wantPush)
		}
	}
	// The ceiling that turns it off still does.
	cut, err := classes.Build(classes.Layers{AgentID: agentID, Permissions: parse(t, "version: 1\n"),
		Ceiling: parse(t, "version: 1\ngit: {request_push: false}\n"), Now: now})
	if err != nil || cut.Git.RequestPush != classes.Deny || cut.Origin["git.request_push"] != classes.LayerCeiling {
		t.Fatalf("under a ceiling without push: %+v (%v); want deny from the ceiling", cut.Git, err)
	}
}
