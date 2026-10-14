//go:build full

package classes_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
)

// The bound of the analysis, measured on the inputs built to take long, up
// to the 4 MiB the hook reads: each decision ends well under a second (the
// slowest measured, the expansion of braces read on Windows, about 0.35 s).
func TestTheBoundOfTheAnalysisIsMeasured(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	def := rules(t, "version: 1\n")
	w := "echo " + strings.Repeat("x", 1900) + strings.Repeat("{b,c}", 8)
	cases := map[string]string{
		"nested ${…} 80k":       "git push; " + strings.Repeat("${x:-", 80000) + "x" + strings.Repeat("}", 80000),
		"nested ${…} 12k":       strings.Repeat("${x:-", 12000) + "x" + strings.Repeat("}", 12000),
		"quoted ${…} 10k":       "echo \"" + strings.Repeat("${x:-", 10000) + "x" + strings.Repeat("}", 10000) + "\"; git push",
		"body substitutions":    "cat <<EOF > d\n" + strings.Repeat("$(echo a) ${b} `c`\n", 3000) + "EOF\ngit push",
		"body without end":      "git push; cat <<EOF\n" + strings.Repeat("{a,b} $(x)\n", 5000),
		"brace words":           strings.Repeat(w+"; ", 30) + "git push",
		"many bodies":           strings.Repeat("cat <<E\nx\nE\n", 5000) + "git push",
		"bare bodies":           strings.Repeat("<<E\nx\nE\n", 7000) + "bash",
		"pipes":                 strings.Repeat("echo a | ", 6500) + "git push",
		"substitutions":         strings.Repeat("$(echo a) ", 6000) + "; git push",
		"nested substitutions":  strings.Repeat("$(echo ", 6000) + strings.Repeat(")", 6000),
		"nested shells":         strings.Repeat("bash -c 'echo a'; ", 3500) + "git push",
		"one long nested shell": "bash -c '" + strings.Repeat("echo a; ", 7000) + "'",
		"pipes into a shell":    strings.Repeat("echo 'echo a' | bash; ", 2800) + "git push",
		"awk commands":          strings.Repeat("awk 'BEGIN{system(\"echo a\")}'; ", 2000) + "git push",
		"script blocks":         strings.Repeat("% { ", 7000) + strings.Repeat("} ", 7000),
		"find -exec":            "find " + strings.Repeat("-exec echo ; ", 5000),
		"tmux commands":         "tmux " + strings.Repeat("new 'echo a' ; ", 4000),
		"awk writes":            "awk '" + strings.Repeat("print > \"a\"; ", 5000) + "'",
		"a 1 MiB body":          "cat <<'EOF' > d\n" + strings.Repeat("{a,b} $(x) ${y:-z}\n", 50000) + "EOF\necho ok",
		"4 MiB":                 strings.Repeat("echo a; ", 500000),
	}
	var slowest time.Duration
	for goos, env := range tableEnvs() {
		for name, cmd := range cases {
			for _, r := range []struct {
				name string
				r    *classes.Rules
			}{{"allow *", free}, {"default", def}} {
				start := time.Now() //nolint:forbidigo // the point is to measure the bound of the analysis
				d := decideOn(t, env, r.r, cmd)
				took := time.Since(start) //nolint:forbidigo // same
				slowest = max(slowest, took)
				if took > raceFactor*time.Second {
					t.Errorf("%s, %s, %s: %v (%s); want well under a second", goos, r.name, name, took, d.Verdict)
				}
				if strings.Contains(cmd, "git push") && d.Verdict == classes.Allow {
					t.Errorf("%s, %s, %s: allowed; want a denial", goos, r.name, name)
				}
			}
		}
	}
	t.Logf("slowest decision: %v", slowest)
}
