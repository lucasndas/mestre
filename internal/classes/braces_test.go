package classes_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// Brace expansion builds words the shell sees and the text does not show: the
// dispatcher expands it before the fixed rules, and the line is at least
// ask and medio.
func TestBraceExpansionIsExpandedBeforeTheRules(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	free := rules(t, anyYAML)
	for cmd, reason := range map[string]string{
		"{git,} push origin":               "git push",
		"git {pu,x}sh origin":              "git push",
		"git pu{s,}h":                      "git push",
		"touch .gi{t,x}/config":            ".git",
		"cp a .{x,claude}/settings.json":   ".claude/settings.json",
		"{sudo,x} ls":                      "sudo",
		"echo {a,b}{c,d} && git {fetch,x}": "git fetch",
		"{x,y}{1..3} && cl{a,}ude":         "claude",
		"rm -f .mestre/{a..c}":             ".mestre",
	} {
		want(t, decide(env, free, m.shell(cmd)), classes.Deny, classes.LayerFixed, reason)
	}
	// What the expansion builds is not literal: the line asks, and is medio.
	def := rules(t, "version: 1\n")
	for _, cmd := range []string{"ec{h,}o hi", "echo {1..3}", "cat {a,b}.txt", "ec{ho,}"} {
		d := decide(env, def, m.shell(cmd))
		want(t, d, classes.Ask, "")
		if d.Risk.Risk != classes.RiskMedium || !strings.Contains(d.Risk.Rule, "chaves") {
			t.Errorf("%q: risk %s (%s); want medio", cmd, d.Risk.Risk, d.Risk.Rule)
		}
	}
	// Quoted, escaped or without a comma or a range, a brace is literal
	// (POSIX: PowerShell has no brace expansion, and its own reading of a
	// brace is apart).
	env.OS = "linux"
	for _, cmd := range []string{`echo "{a,b}"`, `echo '{a,b}'`, `echo \{a,b\}`, "echo {a}", "echo {}", "echo a,b", "echo {a\\,b}", "echo {a..}"} {
		d := decide(env, def, m.shell(cmd))
		if d.Verdict != classes.Allow || strings.Contains(d.Reason, "chaves") {
			t.Errorf("%q = %s (%q); want allow without brace expansion", cmd, d.Verdict, d.Reason)
		}
	}
	if r := shellRisk(env, def, "find . -name x -exec echo {} +"); strings.Contains(r.Rule, "chaves") {
		t.Errorf("find -exec {} counted as a brace expansion: %s", r.Rule)
	}
	// An expansion too large to analyse is denied.
	want(t, decide(env, free, m.shell("echo {1..100000}")), classes.Deny, classes.LayerFixed, "chaves")
	want(t, decide(env, free, m.shell("echo {a,b}{c,d}{e,f}{g,h}{i,j}{k,l}{m,n}{o,p}{q,r}")), classes.Deny, classes.LayerFixed, "chaves")
}
