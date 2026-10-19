package classes_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// What the review of the debuggers and wrappers found
// (docs/classes/permissoes.md, fixed rules).

// Part of CA-PERM-32. A line with a program that runs another and nothing after its `--` is
// no reason to fall: the analysis answers, whatever the line, and does not
// need the net of failClosed for it.
func TestNoLineOfTheDebuggersBringsTheAnalysisDown(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		for _, cmd := range []string{"perf --", "perf -- ", "rr --", "rr record --", "perf stat --", "perf sched record --", "lldb --", "gdb --args", "hyperfine --", "perf", "rr", "perf -", "rr record -"} {
			if d := judge(ee.env, r, cmd); d.Reason == classes.ReasonInternalError {
				t.Errorf("%s: %q fell on the analysis (denied as an internal failure)", ee.name, cmd)
			}
		}
	}
}

// Part of CA-PERM-32: the options of the debuggers and meters as their programs
// read them.
func TestTheDebuggersAndMetersReadTheOptionsTheWayTheirProgramsDo(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	cases := []evasion{
		// hyperfine: the value of a short option stuck to it
		{"hyperfine -p'git push' ls", "git push"}, {"hyperfine -c'git push' ls", "git push"},
		{"hyperfine -s'git push' ls", "git push"}, {"hyperfine -w3 -p'git push' ls", "git push"},
		// lldb: abbreviations and the spaces of the command
		{"lldb -o 'plat shell git push'", "git push"}, {"lldb -o 'pl sh git push'", "git push"}, {"lldb -o 'platform  shell git push'", "git push"},
		{"lldb -o \"platform\tshell git push\"", "git push"}, {"lldb -o 'platform sh git push'", "git push"},
		{"lldb --one-line 'plat sh git push'", "git push"}, {"lldb -b -o run -o 'platform shell git push' -- ls", "git push"},
		// gdb: pipe, |, target remote |, sh and she
		{"gdb -ex 'pipe p 1 | git push'", "git push"}, {"gdb -ex '| p 1 | git push'", "git push"}, {"gdb -ex 'target remote | git push'", "git push"},
		{"gdb -ex 'target extended-remote | git push'", "git push"}, {"gdb -ex 'sh git push'", "git push"}, {"gdb -ex 'she git push'", "git push"},
		{"gdb -batch -ex 'shell  git push'", "git push"}, {"gdb -ex 'pipe -d X p 1 X git push'", "git push"},
	}
	for _, ee := range evasionEnvs() {
		fixedDenials(t, ee.name, ee.env, r, cases)
	}
}

// Part of CA-PERM-32: what the new readings do not take is for the class.
func TestTheDebuggersAndMetersStillRunWhatIsHarmless(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	for _, ee := range evasionEnvs() {
		allowances(t, ee.name, ee.env, r,
			"hyperfine -w3 'ls'", "hyperfine -pgit ls", "hyperfine -N -r5 'ls'", "hyperfine -n name 'ls'",
			"lldb -o 'settings set target.x 1' -- ls", "lldb -o 'breakpoint set -n main' -- ls", "lldb -o 'platform status' -- ls",
			"gdb -ex 'show version' --args ls", "gdb -ex 'set pagination off' -ex run --args ls", "gdb -ex 'target remote localhost:1234'",
			"perf --version", "rr --version")
	}
}

// Part of CA-PERM-32: what the review of the first fix found around gdb, lldb and rr.
func TestTheCommandsAGdbOrLldbValueHoldsAreAllRead(t *testing.T) {
	t.Parallel()
	r := rules(t, anyYAML)
	cases := []evasion{
		{"gdb -batch -eval-command 'sh git push' x", "git push"}, {"gdb -batch -init-eval-command 'sh git push' x", "git push"},
		{"gdb -ex \"bt\nsh git push\"", "git push"}, {"gdb -ex 'if 1\nsh git push\nend'", "git push"},
		{"gdb -ex 'with confirm off -- sh git push'", "git push"}, {"gdb -ex 'alias -a gp = shell git push' -ex gp", "git push"},
		{"gdb -ex 'set exec-wrapper git push' --args ls", "git push"},
		{"lldb -o 'command alias p platform shell git push' -o p x", "git push"},
		{"rr replay -- -ex 'sh git push'", "git push"}, {"rr replay -a -- -ex 'shell git push'", "git push"},
	}
	for _, ee := range evasionEnvs() {
		fixedDenials(t, ee.name, ee.env, r, cases)
	}
}
