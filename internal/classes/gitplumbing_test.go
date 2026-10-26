package classes_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
)

// The index plumbing that moves files to protected paths is a fixed deny
// whenever the paths cannot be seen to be harmless: a --prefix into a
// protected folder (checkout-index, like read-tree), and the entries or paths
// read from the input (update-index --index-info/--stdin, checkout-index
// --stdin). docs/classes/permissoes.md, escrita em caminhos protegidos.

// CA-PERM-13
func TestIndexPlumbingIntoProtectedPathsIsAFixedDeny(t *testing.T) {
	t.Parallel()
	m := newMachine()
	free := rules(t, anyYAML)
	for _, goos := range []string{"linux", "windows"} {
		env := m.env()
		env.OS = goos
		for _, cmd := range []string{
			"git checkout-index -a --prefix=.claude/",
			"git checkout-index -a --prefix=.git/",
			"git checkout-index -a --prefix .git/",
			"git checkout-index -f -a --prefix=.mestre/pergaminhos/",
			"git checkout-index --all --prefix=.claude/agents/",
			"git checkout-index --prefix=.git/hooks/ -- README.md",
			"git read-tree --prefix=.git/ HEAD",
			"git checkout-index --stdin",
			"git checkout-index -z --stdin",
			"git update-index --index-info",
			"git update-index --stdin",
			"git update-index --add --stdin",
			"git update-index -z --index-info",
			"git update-index --cacheinfo 100644,abc123,.git/config",
			"echo '100644 abc123 0\t.git/config' | git update-index --index-info",
			"git ls-files | git update-index --stdin",
		} {
			d := decide(env, free, m.shell(cmd))
			want(t, d, classes.Deny, classes.LayerFixed)
			wantRisk(t, env, free, cmd, classes.RiskFixed)
		}
	}
}

// The plumbing that stays legible keeps its verdict: a prefix outside the
// protected paths and a named pathspec are not a fixed deny, and -a is still
// at least ask.
func TestIndexPlumbingThatIsLegibleIsNotAFixedDeny(t *testing.T) {
	t.Parallel()
	m := newMachine()
	free := rules(t, anyYAML)
	env := m.env()
	env.OS = "linux"
	for _, cmd := range []string{
		"git checkout-index -f -- src/a.go",
		"git checkout-index --prefix=out/ -- src/a.go",
		"git update-index --refresh",
		"git update-index --assume-unchanged src/a.go",
		"git update-index --cacheinfo 100644,abc123,src/a.go",
	} {
		if d := decide(env, free, m.shell(cmd)); d.Verdict == classes.Deny {
			t.Errorf("%q wrongly denied: %q", cmd, d.Reason)
		}
	}
	// -a with a harmless prefix writes the whole index: at least ask.
	if d := decide(env, free, m.shell("git checkout-index -a --prefix=out/")); d.Verdict != classes.Ask {
		t.Errorf("checkout-index -a --prefix=out/ = %s; want ask", d.Verdict)
	}
}
