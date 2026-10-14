package classes_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/providers"
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
	// Listing, testing or making an archive names what it writes, or writes
	// nothing: baixo, it runs.
	for _, cmd := range []string{"tar tzf a.tgz", "tar czf out.tgz src", "unzip -l a.zip", "7z l a.7z", "ar t lib.a", "cpio -o"} {
		want(t, decide(env, def, m.shell(cmd)), classes.Allow, "")
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
