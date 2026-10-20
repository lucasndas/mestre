//go:build full

package classes_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// The bound of the analysis, measured on the inputs built to take long, up
// to the 4 MiB the hook reads: each decision spends at most workLimit units
// of work (see boundedDecision). Work is counted, not
// timed, so the load of the machine, which runs several suites at once, does
// not change the result; a wall-clock net far above the old 1 s bound still
// catches work the counter does not see. The most laborious decision is
// logged.
func TestTheBoundOfTheAnalysisIsMeasured(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	def := rules(t, "version: 1\n")
	w := "echo " + strings.Repeat("x", 1900) + strings.Repeat("{b,c}", 8)
	var distinct strings.Builder
	for i := range 3800 {
		fmt.Fprintf(&distinct, "print > \"a%d\"; ", i)
	}
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
		"awk distinct writes":   "awk '" + distinct.String() + "'",
		"a 1 MiB body":          "cat <<'EOF' > d\n" + strings.Repeat("{a,b} $(x) ${y:-z}\n", 50000) + "EOF\necho ok",
		"4 MiB":                 strings.Repeat("echo a; ", 500000),
	}
	var most int
	var mostName string
	for goos, env := range tableEnvs() {
		for name, cmd := range cases {
			for _, r := range []struct {
				name string
				r    *classes.Rules
			}{{"allow *", free}, {"default", def}} {
				label := goos + ", " + r.name + ", " + name
				d, work := boundedDecision(t, label, env, r.r, cmd)
				if work > most {
					most, mostName = work, label
				}
				if strings.Contains(cmd, "git push") && d.Verdict == classes.Allow {
					t.Errorf("%s: allowed; want a denial", label)
				}
			}
		}
	}
	t.Logf("bound per decision: %d units; most laborious decision: %d (%s)", workLimit, most, mostName)
}

// Part of CA-PERM-25
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
			d, _ := boundedDecision(t, goos+": "+name, env, free, c.cmd)
			want(t, d, classes.Deny, classes.LayerFixed, c.reason)
		}
	}
}

// Part of CA-PERM-25: nor does a chain of the transparent wrappers (exec, nice, env,
// timeout…) grow the analysis as the square of its length: the text of every
// level is built, so every level is paid for, and a line of a thousand and a half of them is too
// laborious to analyse, not allowed.
func TestChainsOfTransparentWrappersAreTooLaborious(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		chains := map[string]string{"exec": strings.Repeat("exec ", 1500) + "git push"}
		if ee.name == "native" { // the second reading of Windows doubles the work; one chain shows it
			chains["env -i"] = strings.Repeat("env -i ", 1200) + "ls"
		}
		for name, cmd := range chains {
			d, _ := boundedDecision(t, ee.name+": "+name, ee.env, free, cmd)
			want(t, d, classes.Deny, classes.LayerFixed, "trabalhoso demais")
		}
		// A chain of a few is what it was.
		allowances(t, ee.name, ee.env, free, "exec nice env -i time ls", "timeout 5 nice -n 5 stdbuf -oL ls")
	}
}

// Part of CA-PERM-25: the analysis of a chain of commands that run commands is bounded.
func TestChainsOfLaunchersAndWrappersAreBounded(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		for name, cmd := range map[string]string{
			"start":        strings.Repeat("start ", 60) + "git push",
			"saps":         strings.Repeat("saps ", 200) + "git push",
			"start /wait":  strings.Repeat("start /wait ", 60) + "git push",
			"npm x exec":   strings.Repeat("npm -a x exec ", 60) + "git push",
			"npx":          strings.Repeat("npx ", 200) + "git push",
			"setarch":      strings.Repeat("setarch x86_64 ", 200) + "git push",
			"cross-env":    strings.Repeat("cross-env A=1 ", 200) + "git push",
			"dot operator": strings.Repeat(". ", 200) + "git push",
			"corepack":     strings.Repeat("corepack npx ", 100) + "git push",
		} {
			d, _ := boundedDecision(t, ee.name+": "+name, ee.env, free, cmd)
			if name == "dot operator" && ee.name != "windows" {
				continue // . is source in a POSIX shell, and the call operator in PowerShell
			}
			if d.Verdict != classes.Deny || d.Layer != classes.LayerFixed {
				t.Errorf("%s: %s = %s by %s (%q); want a fixed denial", ee.name, name, d.Verdict, d.Layer, d.Reason)
			}
		}
	}
}

// Part of CA-PERM-32: a chain of the new wrappers is bounded like the others, and a
// line made to take long is never allowed by running out of time.
func TestChainsOfTheNewWrappersAreBounded(t *testing.T) {
	t.Parallel()
	free := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		for name, cmd := range map[string]string{
			"bwrap":    strings.Repeat("bwrap --bind . . ", 200) + "git push",
			"runcon":   strings.Repeat("runcon -t t ", 200) + "git push",
			"cpulimit": strings.Repeat("cpulimit -l 50 ", 200) + "git push",
			"gdb":      strings.Repeat("gdb --args ", 200) + "git push",
			"perf":     strings.Repeat("perf stat -a ", 200) + "git push",
			"ssh":      strings.Repeat("ssh host ", 200) + "git push",
			"python":   strings.Repeat("python -m pipx run ", 200) + "git push",
			"mixed":    strings.Repeat("bwrap --bind . . perf stat -- ssh host ", 50) + "git push",
		} {
			d, _ := boundedDecision(t, ee.name+": "+name, ee.env, free, cmd)
			if d.Verdict != classes.Deny || d.Layer != classes.LayerFixed {
				t.Errorf("%s: %s = %s by %s (%q); want a fixed denial", ee.name, name, d.Verdict, d.Layer, d.Reason)
			}
		}
	}
}
