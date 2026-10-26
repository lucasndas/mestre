//go:build full

package classes_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
)

// With the real git state, a repository that tracks a file named "-" does
// not make "python3 -" a script of the project (baixo): "-" is the standard
// input, so the code is not one the repository has.

// Part of CA-RISCO-12
func TestDashIsTheStandardInputEvenIfTheRepositoryHasAFileNamedDash(t *testing.T) {
	t.Parallel()
	tr := newTableRepo(t)
	tr.write(t, tr.mainDir, "-", "print('from the repository')\n")
	tr.commit(t, tr.main)
	tr.write(t, tr.wtDir, "-", "print('from the repository')\n")
	base, err := tr.main.ResolveRef(tr.ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	tr.scripts.Base = string(base)
	if tr.changed(t, "-") {
		t.Fatal("the fixture's file named - should read as the repository's")
	}
	if tr.changed(t, "scripts/build.sh") {
		t.Fatal("the repository's script reads as changed")
	}

	env := classes.Env{AgentID: agentID, Worktree: tr.wtDir, Git: tr.scripts, Exceptions: newLedger()}
	def := rules(t, "version: 1\n")
	for _, p := range []string{"python3", "node", "ruby", "perl", "php", "lua"} {
		for _, cmd := range []string{p + " -", p + " - arg", p + " - < in.txt"} {
			if got := shellRisk(env, def, cmd); got.Risk < classes.RiskMedium {
				t.Errorf("%q = %s (%s); want at least medio", cmd, got.Risk, got.Rule)
			}
		}
		wantRisk(t, env, def, "cat f.txt | "+p+" -", classes.RiskHigh)
	}
	// The control: the repository's own script is still baixo.
	wantRisk(t, env, def, "python3 scripts/build.sh", classes.RiskLow)
}
