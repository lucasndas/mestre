package main

import (
	"context"
	"io"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
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
		"fast, race, inside 3x":  {"fast", true, 0, 80 * time.Second, false, "PASS fast: 1 tests in 1m20s (budget 1m30s, race x3)", "2m15s"},
		"fast, race, over 3x":    {"fast", true, 0, 100 * time.Second, true, "FAIL fast suite took 1m40s, budget 1m30s", "2m15s"},
		"fast, no race, over 1x": {"fast", false, 0, 80 * time.Second, true, "FAIL fast suite took 1m20s, budget 30s", "45s"},
		"full, race, inside 3x":  {"full", true, 0, 8 * time.Minute, false, "PASS full: 1 tests in 8m0s (budget 9m0s, race x3)", "13m30s"},
		"full, race, over 3x":    {"full", true, 0, 9*time.Minute + time.Second, true, "FAIL full suite took 9m1s, budget 9m0s", "13m30s"},
		"fast, race and count 2": {"fast", true, 2, 170 * time.Second, false, "PASS fast: 1 tests in 2m50s (budget 3m0s, race x3)", "4m30s"},
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
			run := ex.callsMatching("./...")
			var goRun Cmd
			for _, cmd := range run {
				if cmd.Name == "go" {
					goRun = cmd
				}
			}
			args := goRun.String()
			if got := strings.Contains(args, " -race"); got != c.race {
				t.Fatalf("-race = %v, want %v: %s", got, c.race, args)
			}
			if got := strings.Contains(args, RaceGcflags); got != c.race {
				t.Fatalf("checkptr off for modernc.org = %v, want %v: %s", got, c.race, args)
			}
			if !strings.Contains(args, "-timeout "+c.timeout+" ") {
				t.Fatalf("want -timeout %s: %s", c.timeout, args)
			}
			if got := strings.Contains(strings.Join(goRun.Env, " "), "CGO_ENABLED=1"); got != c.race {
				t.Fatalf("env = %v, CGO_ENABLED=1 is %v, want %v", goRun.Env, got, c.race)
			}
			if c.race {
				if n := len(ex.callsMatching("golangci-lint")) + len(ex.callsMatching("-run")); n != 0 {
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
	if testArgs != "" && (!strings.Contains(testEnv, "CGO_ENABLED=1") || !strings.Contains(testEnv, "MESTRE_TEST_SUITE=full")) {
		t.Errorf("go test env = %q", testEnv)
	}
	return code, out.String(), testArgs
}

// CA-TESTES-12
// The pre-push run is the full suite, with -race, on the changed packages and
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
				for _, w := range []string{" -race ", "-tags full ", "-count=1 ", "-timeout 13m30s "} {
					if !strings.Contains(args, w) {
						t.Fatalf("go test lacks %q: %s", w, args)
					}
				}
				if !strings.HasSuffix(args, " "+c.wantPkgs) {
					t.Fatalf("go test = %s\nwant packages %s", args, c.wantPkgs)
				}
				if !strings.Contains(text, "PASS prepush: 2 tests in") || !strings.Contains(text, "in 42s (race, tags full)") {
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
	})
	t.Run("the panel is not supported yet", func(t *testing.T) {
		t.Parallel()
		ex := &fakeExec{handle: func(Cmd, io.Writer) int { return 0 }}
		r, out := newRunner(fstest.MapFS{"web/package.json": {}}, clock.NewFake(today), ex)
		r.Stdin = strings.NewReader("internal/a/a.go\n")
		if code := r.Prepush(context.Background()); code != 2 || !strings.Contains(out.String(), "M6") {
			t.Fatalf("exit %d:\n%s", code, out)
		}
	})
}
