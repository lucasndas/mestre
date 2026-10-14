package classes_test

import (
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// An absent git.branches is the table's branch: a class that commits (or
// leaves the commit as a request) commits there, and nowhere else.
func TestAbsentBranchesAreTheTablesBranch(t *testing.T) {
	t.Parallel()
	m := newMachine()
	for _, y := range []string{"version: 1\n", "version: 1\ngit: {commit: true}\n"} {
		r := rules(t, y)
		if !slices.Equal(r.Git.Branches, []string{"epico/42-login"}) {
			t.Fatalf("%q: git.branches = %v; want the table's branch", y, r.Git.Branches)
		}
		env := m.env()
		env.Git = fakeGit{branch: "epico/42-login", staged: []string{"src/a.go"}}
		want(t, decide(env, r, m.shell("git commit -m x")), classes.Allow, "")
		env.Git = fakeGit{branch: "main", staged: []string{"src/a.go"}}
		want(t, decide(env, r, m.shell("git commit -m x")), classes.Deny, classes.LayerClass, "git.branches")
		want(t, decide(env, r, m.shell("git switch main")), classes.Deny, classes.LayerClass, "git.branches")
	}
	// Written, it holds as written; without a table, absent is none.
	r := rules(t, "version: 1\ngit: {branches: [\"epico/*\"]}\n")
	if !slices.Equal(r.Git.Branches, []string{"epico/*"}) {
		t.Fatalf("written branches = %v", r.Git.Branches)
	}
	p, err := classes.ParsePermissions([]byte("version: 1\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err = classes.Build(classes.Layers{AgentID: agentID, Class: "dev", Permissions: p, Now: now})
	if err != nil || len(r.Git.Branches) != 0 {
		t.Fatalf("without a table: %v, %v", r, err)
	}
	// The ceiling still cuts it.
	c := ceiling(t, "version: 1\npermissions: {git: {branches: [\"release/*\"]}}\n")
	r, err = classes.Build(classes.Layers{AgentID: agentID, Class: "dev", Permissions: p, Ceiling: c.Maximum(),
		Placeholders: map[string]string{"mesa.branch": "epico/42-login"}, Now: now})
	if err != nil || len(r.Git.Branches) != 0 {
		t.Fatalf("under a ceiling of release/*: %v, %v", r.Git.Branches, err)
	}
}
