package classes_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
)

// An extraction or a patch writes files the line does not name, which the
// fixed rules and the files block cannot see: it is medio, so it asks.
func TestWritersWithoutAVisiblePathAreMedio(t *testing.T) {
	t.Parallel()
	m := newMachine()
	env := m.env()
	def := rules(t, "version: 1\n")
	for _, cmd := range []string{
		"tar xzf a.tgz", "tar -xf a.tar -C out", "tar --extract -f a.tar", "bsdtar -x -f a.zip",
		"unzip a.zip", "unzip -o a.zip -d out", "7z x a.7z", "7za e a.7z", "unrar x a.rar",
		"cpio -idv", "pax -r -f a.pax", "ar x lib.a", "jar xf a.jar", "patch -p1 -i x.diff",
		"git apply x.diff", "git am x.patch", "busybox tar xf a.tar", "Expand-Archive a.zip out",
	} {
		d := decide(env, def, m.shell(cmd))
		want(t, d, classes.Ask, "")
		if d.Risk.Risk != classes.RiskMedium || !strings.Contains(d.Risk.Rule, "escrita sem caminho visível") {
			t.Errorf("%q: risk %s (%s); want medio by the signal", cmd, d.Risk.Risk, d.Risk.Rule)
		}
	}
	// Listing or testing an archive writes nothing: baixo, it runs. Making
	// one writes a file the write rules do not see as a write: outside the
	// list of known-safe programs, medio.
	for _, cmd := range []string{"tar tzf a.tgz", "tar -tvf a.tar", "unzip -l a.zip", "7z l a.7z", "ar t lib.a", "cpio -o"} {
		want(t, decide(env, def, m.shell(cmd)), classes.Allow, "")
	}
	for _, cmd := range []string{"tar czf out.tgz src", "tar -cf .claude.tar x", "zip -r out.zip src"} {
		if d := decide(env, def, m.shell(cmd)); d.Verdict != classes.Ask || d.Risk.Risk != classes.RiskMedium {
			t.Errorf("%q: %s/%s (%s); want ask, medio", cmd, d.Verdict, d.Risk.Risk, d.Risk.Rule)
		}
	}
	// The reach of a pattern sees them.
	for _, p := range []string{"tar *", "unzip *", "patch *", "git a*"} {
		ap, err := classes.CheckApprovablePattern(p, classes.Action{Kind: providers.KindShell, Command: strings.TrimSuffix(p, "*") + "x"}, "dev", "42-login")
		if err != nil {
			t.Fatalf("%q: %v", p, err)
		}
		if ap.Risk.Risk == classes.RiskLow {
			t.Errorf("%q reaches %s (%s); want medio or more", p, ap.Risk.Risk, ap.Risk.Rule)
		}
	}
}
