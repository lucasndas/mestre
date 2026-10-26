package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-TESTES-11
// With -race the budget and the go test timeout are three times the normal
// ones, the linter and the timing of slow tests do not run, and cgo is on.
func TestRaceMultipliesTheBudget(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		suite    string
		race     bool
		count    int
		took     time.Duration
		wantExit bool
		wantLine string
		timeout  string
	}{
		"fast, race, inside 3x":  {"fast", true, 0, 100 * time.Second, false, "PASS fast: 1 tests in 1m40s (budget 1m48s, race x3)", "2m42s"},
		"fast, race, over 3x":    {"fast", true, 0, 110 * time.Second, true, "FAIL fast suite took 1m50s, budget 1m48s", "2m42s"},
		"fast, no race, over 1x": {"fast", false, 0, 80 * time.Second, true, "FAIL fast suite took 1m20s, budget 36s", "54s"},
		"full, race, inside 3x":  {"full", true, 0, 10 * time.Minute, false, "PASS full: 1 tests in 10m0s (budget 10m48s, race x3)", "16m12s"},
		"full, race, over 3x":    {"full", true, 0, 10*time.Minute + 49*time.Second, true, "FAIL full suite took 10m49s, budget 10m48s", "16m12s"},
		"fast, race and count 2": {"fast", true, 2, 200 * time.Second, false, "PASS fast: 1 tests in 3m20s (budget 3m36s, race x3)", "5m24s"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(today)
			ex := &fakeExec{handle: func(cmd Cmd, out io.Writer) int {
				if isMainRun(cmd) {
					_, _ = io.WriteString(out, ev("TestSlow", "pass", 0.5)) // slow, but not timed with -race
					clk.Advance(c.took)
				}
				return 0
			}}
			r, out := newRunner(nil, clk, ex)
			r.Race = c.race
			r.Count = c.count

			code := r.RunSuite(context.Background(), Suites[c.suite])

			text := out.String()
			if (code != 0) != c.wantExit || !strings.Contains(text, c.wantLine) {
				t.Fatalf("exit %d, want line %q:\n%s", code, c.wantLine, text)
			}
			build := ex.callsMatching("test -c")
			run := ex.callsMatching("tool test2json")
			if len(build) != 1 || len(run) != 1 {
				t.Fatalf("build calls %v, run calls %v", build, run)
			}
			args := build[0].String()
			if got := strings.Contains(args, " -race"); got != c.race {
				t.Fatalf("-race = %v, want %v: %s", got, c.race, args)
			}
			if got := strings.Contains(args, RaceGcflags); got != c.race {
				t.Fatalf("checkptr off for modernc.org = %v, want %v: %s", got, c.race, args)
			}
			if !strings.Contains(run[0].String(), "-test.timeout="+c.timeout) {
				t.Fatalf("want -test.timeout=%s: %s", c.timeout, run[0])
			}
			if got := strings.Contains(strings.Join(build[0].Env, " "), "CGO_ENABLED=1"); got != c.race {
				t.Fatalf("env = %v, CGO_ENABLED=1 is %v, want %v", build[0].Env, got, c.race)
			}
			if c.race {
				if n := len(ex.callsMatching("golangci-lint")) + len(ex.callsMatching("-test.run")); n != 0 {
					t.Fatalf("race run started the linter or timed slow tests (%d calls)", n)
				}
			}
		})
	}
}

const modList = "example.com/m/pkg/clock|example.com/m|,,\n" +
	"example.com/m/internal/a|example.com/m|example.com/m/pkg/clock,,\n" +
	"example.com/m/internal/b|example.com/m|example.com/m/internal/a,,\n" +
	"example.com/m/internal/c|example.com/m|,example.com/m/internal/b,\n" +
	"example.com/m/internal/d|example.com/m|,,\n" +
	"example.com/m|example.com/m|example.com/m/internal/d,,\n"

// prepush runs the mode over a list of changed files and returns the exit
// code, the output and the arguments of the go test run ("" when none).
func prepush(t *testing.T, files string, testCode int) (int, string, string) {
	t.Helper()
	clk := clock.NewFake(today)
	var testArgs, testEnv string
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		switch {
		case c.Name == "go" && len(c.Args) > 0 && c.Args[0] == "list":
			_, _ = io.WriteString(out, modList)
		case c.Name == "go" && len(c.Args) > 0 && c.Args[0] == "test":
			testArgs, testEnv = c.String(), strings.Join(c.Env, " ")
			_, _ = io.WriteString(out, ev("TestOne", "pass", 0.01)+ev("TestTwo", "pass", 0.01))
			clk.Advance(42 * time.Second)
			return testCode
		}
		return 0
	}}
	r, out := newRunner(nil, clk, ex)
	r.Stdin = strings.NewReader(files)
	code := r.Prepush(context.Background())
	if testArgs != "" && (strings.Contains(testEnv, "CGO_ENABLED=1") || !strings.Contains(testEnv, "MESTRE_TEST_SUITE=full")) {
		t.Errorf("go test env = %q", testEnv)
	}
	return code, out.String(), testArgs
}

// CA-TESTES-12
// The pre-push run is the full suite, without -race, on the changed packages and
// their dependents only; documents alone run nothing.
func TestPrepushRunsTheChangedPackagesAndTheirDependents(t *testing.T) {
	t.Parallel()
	const pk = "example.com/m/"
	cases := map[string]struct {
		files    string
		wantPkgs string // the package arguments of go test; "" runs nothing
		wantText string
	}{
		"a leaf and its dependents": {"internal/a/a.go\ninternal/a/a_test.go\ndocs/x.md\n", pk + "internal/a " + pk + "internal/b " + pk + "internal/c", "3 to run with their dependents"},
		"the test import counts":    {"internal/b/b.go\n", pk + "internal/b " + pk + "internal/c", ""},
		"a base package":            {"pkg/clock/clock.go\n", pk + "internal/a " + pk + "internal/b " + pk + "internal/c " + pk + "pkg/clock", ""},
		"the root package":          {"main.go\n", "example.com/m", ""},
		"a data file":               {"internal/d/testdata/x.json\n", "example.com/m " + pk + "internal/d", ""},
		"go.mod":                    {"go.mod\ndocs/x.md\n", "./...", "running every package"},
		"a file outside a package":  {"testdata/projetos/x/main.go.txt\n", "./...", "running every package"},
		"only documents":            {"docs/a.md\nREADME.md\n.github/workflows/ci.yml\ntools/githooks/pre-push\n", "", "only documents and CI files changed (4 file(s)), nothing to run"},
		"nothing":                   {"", "", "nothing to run"},
		"a windows-only package":    {"internal/w/w_windows.go\n", "", "internal/w/w_windows.go is in no package of this system: left to the CI"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			code, text, args := prepush(t, c.files, 0)
			if code != 0 {
				t.Fatalf("exit %d:\n%s", code, text)
			}
			if c.wantPkgs == "" {
				if args != "" {
					t.Fatalf("ran go test: %s", args)
				}
			} else {
				if strings.Contains(args, "-race") {
					t.Fatalf("the pre-push run has no -race: %s", args)
				}
				for _, w := range []string{"-tags full ", "-count=1 ", "-timeout 5m24s "} {
					if !strings.Contains(args, w) {
						t.Fatalf("go test lacks %q: %s", w, args)
					}
				}
				if !strings.HasSuffix(args, " "+c.wantPkgs) {
					t.Fatalf("go test = %s\nwant packages %s", args, c.wantPkgs)
				}
				if !strings.Contains(text, "PASS prepush: 2 tests in") || !strings.Contains(text, "in 42s (tags full)") {
					t.Fatalf("no time line:\n%s", text)
				}
			}
			if c.wantText != "" && !strings.Contains(text, c.wantText) {
				t.Fatalf("output lacks %q:\n%s", c.wantText, text)
			}
		})
	}
	t.Run("a failing test fails the push", func(t *testing.T) {
		t.Parallel()
		code, text, _ := prepush(t, "internal/a/a.go\n", 1)
		if code != 1 || !strings.Contains(text, "FAIL prepush:") {
			t.Fatalf("exit %d:\n%s", code, text)
		}
		if !strings.Contains(text, "in 42s (tags full)") {
			t.Fatalf("a failing push does not print the time:\n%s", text)
		}
	})
}
