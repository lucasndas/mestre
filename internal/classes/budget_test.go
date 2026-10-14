package classes_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
)

// CA-PERM-25
func TestTheAnalysisOfACommandIsBounded(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for goos, env := range tableEnvs() {
		for name, c := range map[string]struct {
			cmd, reason string
		}{
			"4 MiB":                     {strings.Repeat("${x:-", 800000), "longo demais"},
			"64 KiB of commands":        {strings.Repeat("echo a; ", 9000), "longo demais"},
			"nested ${…}":               {strings.Repeat("${x:-", 10000) + "x" + strings.Repeat("}", 10000), "aninhadas demais"},
			"nested $((…))":             {"echo " + strings.Repeat("$((1+", 5000) + "1" + strings.Repeat("))", 5000), "aninhadas demais"},
			"nested in a body":          {"cat <<EOF > d\n" + strings.Repeat("${x:-", 5000) + "\nEOF", "aninhadas demais"},
			"bare bodies":               {strings.Repeat("<<E\nx\nE\n", 1000) + strings.Repeat("bash\n", 1000), "trabalhoso demais"},
			"a long body a shell reads": {"bash <<'EOF'\n" + strings.Repeat("echo a\n", 10000) + "EOF", "longo demais"},
		} {
			start := time.Now() //nolint:forbidigo // the point is to measure the bound of the analysis
			d := decideOn(t, env, free, c.cmd)
			if took := time.Since(start); took > raceFactor*time.Second { //nolint:forbidigo // same
				t.Errorf("%s: %s took %v; want well under a second", goos, name, took)
			}
			want(t, d, classes.Deny, classes.LayerFixed, c.reason)
		}
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
