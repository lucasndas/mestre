package classes_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// Part of CA-PERM-25 and CA-PERM-32 (the sizes of the criterion, up to the 4 MiB
// line and the thousand and a half of chained wrappers, are in the full suite):
// the bound of the analysis on a reduced scale, for the three denials of the
// bound (long, deep, laborious) and for the chains of programs that run
// programs, which end in a fixed denial instead of being analysed.
func TestTheBoundOfTheAnalysisDeniesTheLongTheDeepAndTheChainedOnAReducedScale(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for goos, env := range tableEnvs() {
		for name, c := range map[string]struct {
			cmd, reason string
		}{
			"72 KiB of commands": {strings.Repeat("echo a; ", 9000), "longo demais"},
			"nested ${…}":        {strings.Repeat("${x:-", 40) + "x" + strings.Repeat("}", 40), "aninhadas demais"},
			"nested $((…))":      {"echo " + strings.Repeat("$((1+", 40) + "1" + strings.Repeat("))", 40), "aninhadas demais"},
			"nested in a body":   {"cat <<EOF > d\n" + strings.Repeat("${x:-", 40) + "\nEOF", "aninhadas demais"},
			"bare bodies":        {strings.Repeat("<<E\nx\nE\n", 1000) + strings.Repeat("bash\n", 1000), "trabalhoso demais"},
			"start":              {strings.Repeat("start ", 60) + "git push", "aninhado demais"},
			"npx":                {strings.Repeat("npx ", 200) + "git push", "aninhado demais"},
			"bwrap":              {strings.Repeat("bwrap --bind . . ", 100) + "git push", "aninhado demais"},
			"setarch":            {strings.Repeat("setarch x86_64 ", 200) + "git push", "aninhado demais"},
			"saps":               {strings.Repeat("saps ", 200) + "git push", "aninhado demais"},
			"cross-env":          {strings.Repeat("cross-env A=1 ", 200) + "git push", "aninhado demais"},
			"runcon":             {strings.Repeat("runcon -t t ", 200) + "git push", "aninhado demais"},
			"ssh":                {strings.Repeat("ssh host ", 200) + "git push", "aninhado demais"},
		} {
			d, _ := boundedDecision(t, goos+": "+name, env, free, c.cmd)
			want(t, d, classes.Deny, classes.LayerFixed, c.reason)
		}
		// A chain of a few is what it was.
		allowances(t, goos, env, free, "exec nice env -i time ls", "timeout 5 nice -n 5 stdbuf -oL ls")
	}
}

// CA-PERM-25
func TestTheDataOfAHereDocumentIsNotBounded(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for goos, env := range tableEnvs() {
		// The data of a here-document does not count: 76 KiB of JSON is
		// written, and a fixed rule after it still denies.
		data := "cat <<'EOF' > data.json\n" + strings.Repeat(`{"id":1,"tags":["a","b"]},`, 3000) + "\nEOF"
		if d := decideOn(t, env, free, data); d.Verdict != classes.Allow {
			t.Errorf("%s: a 76 KiB here-document = %s (%s); want allow", goos, d.Verdict, d.Reason)
		}
		want(t, decideOn(t, env, free, data+"\ngit push"), classes.Deny, classes.LayerFixed, "git push")
		// Thirty nested expansions are still read: a fixed rule inside denies.
		nested := "echo " + strings.Repeat("${x:-", 30) + "$(git push)" + strings.Repeat("}", 30)
		want(t, decideOn(t, env, free, nested), classes.Deny, classes.LayerFixed, "git push")
	}
}
