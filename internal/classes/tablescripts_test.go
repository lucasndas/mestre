package classes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
)

// branchOnly is a GitState that cannot tell the scripts of the table.
type branchOnly struct{}

func (branchOnly) Branch(context.Context, string) (string, error) { return "epico/42-login", nil }
func (branchOnly) Committed(context.Context, string, bool) ([]string, error) {
	return nil, nil
}

// A script created or changed at the table is not a script of the project:
// running it is medio (ADR 0025); one that came from the repository stays
// baixo, and without the git state every script is medio.
func TestScriptsOfTheTableAreMedio(t *testing.T) {
	t.Parallel()
	m := newMachine()
	def := rules(t, "version: 1\n")
	env := m.env()
	env.Git = fakeGit{branch: "epico/42-login", changed: []string{"scripts/new.sh", "tools/new.py"}}
	for _, cmd := range []string{"./scripts/build.sh", "bash scripts/build.sh", "python3 tools/ok.py --x", "sh ./scripts/build.sh", "make test"} {
		d := decide(env, def, m.shell(cmd))
		want(t, d, classes.Allow, "")
		if d.Risk.Risk != classes.RiskLow {
			t.Errorf("%q: risk %s (%s); want baixo", cmd, d.Risk.Risk, d.Risk.Rule)
		}
	}
	medio := func(env classes.Env, cmd string) {
		t.Helper()
		d := decide(env, def, m.shell(cmd))
		want(t, d, classes.Ask, "")
		if d.Risk.Risk != classes.RiskMedium || !strings.Contains(d.Risk.Rule, "script alterado na mesa") {
			t.Errorf("%q: risk %s (%s); want medio by the changed script", cmd, d.Risk.Risk, d.Risk.Rule)
		}
	}
	for _, cmd := range []string{"./scripts/new.sh", "bash scripts/new.sh", "python3 tools/new.py", "cd scripts && ./new.sh", "node ./tools/new.py", ". scripts/new.sh", "busybox sh scripts/new.sh"} {
		medio(env, cmd)
	}
	// No git state that tells them: every script of the worktree is medio.
	for _, g := range []classes.GitState{nil, branchOnly{}} {
		e := m.env()
		e.Git = g
		for _, cmd := range []string{"./scripts/build.sh", "python3 tools/ok.py"} {
			medio(e, cmd)
		}
	}
	// The classification of a request sees it too.
	if r := classes.ClassifyRisk(env, def, classes.RiskRequest{Tool: m.shell("./scripts/new.sh").Tool}); r.Risk != classes.RiskMedium {
		t.Errorf("ClassifyRisk = %s (%s); want medio", r.Risk, r.Rule)
	}
}
