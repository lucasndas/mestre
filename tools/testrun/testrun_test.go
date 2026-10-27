package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

const pkg = "github.com/mestre-ia/mestre/internal/sample"

var today = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

// fakeExec answers commands with recorded output and keeps the calls.
type fakeExec struct {
	mu      sync.Mutex
	calls   []Cmd
	handle  func(c Cmd, out io.Writer) int
	missing string // a command name that is not installed
	// build answers `go test -c` (the build of the test binaries); nil
	// succeeds.
	build func(c Cmd, out io.Writer) int
}

func (f *fakeExec) Run(ctx context.Context, c Cmd, out io.Writer) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if c.Name == f.missing {
		return -1, fmt.Errorf("run %s: exec: %q: executable file not found in $PATH", c, c.Name)
	}
	var code int
	switch {
	case c.Name == "go" && slices.Contains(c.Args, testListFormat):
		// one package with tests, in the module of the repository
		_, _ = io.WriteString(out, pkg+"|github.com/mestre-ia/mestre|1|0\n")
	case c.Name == "go" && len(c.Args) > 1 && c.Args[0] == "test" && c.Args[1] == "-c":
		if f.build != nil {
			code = f.build(c, out)
		}
	case c.Name == "go" && len(c.Args) > 1 && c.Args[0] == "tool" && c.Args[1] == "-n":
		// test2json is run with `go tool`
	default:
		code = f.handle(c, out)
	}
	if err := ctx.Err(); err != nil {
		return -1, err // killed: the output is partial
	}
	return code, nil
}

func (f *fakeExec) callsMatching(sub string) []Cmd {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Cmd
	for _, c := range f.calls {
		if strings.Contains(c.String(), sub) {
			out = append(out, c)
		}
	}
	return out
}

func ev(test, action string, elapsed float64, output ...string) string {
	var b strings.Builder
	for _, o := range output {
		line, _ := json.Marshal(event{Action: "output", Package: pkg, Test: test, Output: o + "\n"})
		b.Write(append(line, '\n'))
	}
	line, _ := json.Marshal(event{Action: action, Package: pkg, Test: test, Elapsed: elapsed})
	b.Write(append(line, '\n'))
	return b.String()
}

func newRunner(fsys fstest.MapFS, clk clock.Clock, ex Exec) (*Runner, *bytes.Buffer) {
	var out bytes.Buffer
	if fsys == nil {
		fsys = fstest.MapFS{}
	}
	mkTemp := func() (string, func(), error) { return "/bins", func() {}, nil }
	return &Runner{Exec: ex, Clock: clk, FS: fsys, Out: &out, MkTemp: mkTemp, GOOS: "linux"}, &out
}

// isMainRun is the run of the test binary of the suite (not the solo re-run of
// slow tests).
func isMainRun(c Cmd) bool {
	return c.Name == "go" && strings.Contains(c.String(), "tool test2json") && !strings.Contains(c.String(), "-test.run")
}

// CA-TESTES-01
func TestOverBudgetFailsAndListsTenSlowest(t *testing.T) {
	t.Parallel()
	recorded, err := os.ReadFile("testdata/over-budget.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	span := parseEvents(recorded)
	clk := clock.NewFake(today)
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		if isMainRun(c) {
			_, _ = out.Write(recorded)
			clk.Advance(span.last.Sub(span.first)) // the recorded run took this long
		}
		return 0
	}}
	r, out := newRunner(nil, clk, ex)

	code := r.RunSuite(context.Background(), Suites["fast"])

	if code == 0 {
		t.Fatalf("exit 0 for a run of %v:\n%s", span.last.Sub(span.first), out)
	}
	text := out.String()
	if !strings.Contains(text, "FAIL fast suite took 37.5s, budget 36s. The 10 slowest tests:") {
		t.Fatalf("no budget failure:\n%s", text)
	}
	var list []string
	after := text[strings.Index(text, "The 10 slowest tests:"):]
	for _, l := range strings.Split(after, "\n")[1:] {
		if !strings.HasPrefix(l, "  ") {
			break
		}
		f := strings.Fields(l)
		list = append(list, f[len(f)-1])
	}
	want := "TestCase02 TestCase08 TestCase06 TestCase10 TestCase04 TestCase11 TestCase07 TestCase03 TestCase12 TestCase05"
	if got := strings.Join(list, " "); got != want {
		t.Fatalf("10 slowest = %s\nwant         %s", got, want)
	}
}

// CA-TESTES-02
func TestSlowFastSuiteTestIsFlaggedOnly(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		switch {
		case isMainRun(c):
			_, _ = io.WriteString(out, ev("TestSlow", "pass", 0.25)+ev("TestQuick", "pass", 0.05)+ev("", "pass", 0.3))
		case strings.Contains(c.String(), "^TestSlow$"):
			_, _ = io.WriteString(out, ev("TestSlow", "pass", 0.15))
		}
		return 0
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)

	code := r.RunSuite(context.Background(), Suites["fast"])

	text := out.String()
	if code != 0 {
		t.Fatalf("slow test failed the suite:\n%s", text)
	}
	if !strings.Contains(text, "warning: 1 fast-suite test(s) above 100ms") || !strings.Contains(text, "TestSlow") || strings.Contains(text, "TestQuick") {
		t.Fatalf("flags:\n%s", text)
	}
	if n := len(ex.callsMatching("-test.run ^TestSlow$")); n != 1 {
		t.Fatalf("TestSlow timed alone %d times, want 1", n)
	}
}

// Part of CA-TESTES-10 (a test of 150 ms is neither re-run nor flagged). A test
// between 100 and 200 ms in the suite run is not run again: contention
// does not double a time that often, and the re-run is for the few that might be
// slow. It is not flagged either.
func TestATestUnder200msInTheSuiteIsNotRunAgainNorFlagged(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		if isMainRun(c) {
			_, _ = io.WriteString(out, ev("TestMiddling", "pass", 0.15)+ev("TestEdge", "pass", 0.2)+ev("TestQuick", "pass", 0.05))
		}
		return 0
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)
	if code := r.RunSuite(context.Background(), Suites["fast"]); code != 0 || strings.Contains(out.String(), "warning") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if c := ex.callsMatching("-test.run"); len(c) != 0 {
		t.Fatalf("tests under 200 ms were run again: %v", c)
	}
}

func TestSlowOnlyUnderContentionIsNotFlagged(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		if isMainRun(c) {
			_, _ = io.WriteString(out, ev("TestBusy/sub_case", "pass", 0.3)+ev("TestBusy", "pass", 0.3))
		} else {
			_, _ = io.WriteString(out, ev("TestBusy/sub_case", "pass", 0.01))
		}
		return 0
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)
	if code := r.RunSuite(context.Background(), Suites["fast"]); code != 0 || strings.Contains(out.String(), "warning") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if c := ex.callsMatching("-test.run"); len(c) != 1 || !strings.Contains(c[0].String(), "-test.run ^TestBusy$/^sub_case$") {
		t.Fatalf("calls = %v", c)
	}
}

// CA-TESTES-03
func TestQuarantineSkipsAndExpiredFailsFull(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{QuarantinePath: {Data: []byte(`tests:
  - test: TestRecent
    issue: https://github.com/mestre-ia/mestre/issues/1
    added: 2026-10-01
    reason: races on slow disks
  - test: TestOld
    issue: https://github.com/mestre-ia/mestre/issues/2
    added: 2026-09-20
    reason: flaky pairing
`)}}
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		_, _ = io.WriteString(out, ev("TestOther", "pass", 0.01))
		return 0
	}}
	r, out := newRunner(fsys, clock.NewFake(today), ex)

	code := r.RunSuite(context.Background(), Suites["full"])

	main := ex.callsMatching("tool test2json")
	if len(main) != 1 || !strings.Contains(main[0].String(), "-test.count=1 -test.timeout=5m24s -test.skip=^(TestRecent|TestOld)$") {
		t.Fatalf("the quarantine is not passed to the test binary: %v", main)
	}
	if b := ex.callsMatching("test -c"); len(b) != 1 || !strings.Contains(b[0].String(), "-tags full") || strings.Contains(b[0].String(), "-skip") {
		t.Fatalf("build call = %v", b)
	}
	text := out.String()
	if !strings.Contains(text, "TestRecent (https://github.com/mestre-ia/mestre/issues/1") ||
		!strings.Contains(text, "TestOld (https://github.com/mestre-ia/mestre/issues/2, since 2026-09-20: flaky pairing) EXPIRED") {
		t.Fatalf("skipped list:\n%s", text)
	}
	if strings.Contains(text, "TestRecent (https://github.com/mestre-ia/mestre/issues/1, since 2026-10-01: races on slow disks) EXPIRED") {
		t.Fatalf("recent entry marked expired:\n%s", text)
	}
	if code == 0 || !strings.Contains(text, "FAIL quarantine has expired entries") {
		t.Fatalf("code %d:\n%s", code, text)
	}
}

// CA-TESTES-04
func TestFailingTestRunsOnce(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		_, _ = io.WriteString(out, ev("TestBroken", "fail", 0.4, "sample_test.go:9: boom")+ev("", "fail", 0.4))
		return 1
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)

	code := r.RunSuite(context.Background(), Suites["fast"])

	if code == 0 {
		t.Fatal("failing test passed the suite")
	}
	if n := len(ex.callsMatching("tool test2json")); n != 1 {
		t.Fatalf("go test called %d times, want once: %v", n, ex.calls)
	}
	if text := out.String(); !strings.Contains(text, "--- FAIL: "+pkg+" TestBroken") || !strings.Contains(text, "sample_test.go:9: boom") {
		t.Fatalf("failure report:\n%s", text)
	}
}

// A goroutine that panics after its test ended takes the binary down while
// other tests run: `go test -json` puts the panic text under the test that was
// running, which never finishes. The report shows it instead of a bare
// "(package)" failure.
func TestACrashedBinaryShowsThePanicUnderTheTestThatWasRunning(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		if !isMainRun(c) {
			return 0
		}
		_, _ = io.WriteString(out, ev("TestDone", "pass", 0.1))
		for _, o := range []string{"=== CONT  TestRunning", "panic: nil pointer dereference", "goroutine 8 [running]:"} {
			line, _ := json.Marshal(event{Action: "output", Package: pkg, Test: "TestRunning", Output: o + "\n"})
			_, _ = out.Write(append(line, '\n'))
		}
		_, _ = io.WriteString(out, ev("", "fail", 0.4, "FAIL\t"+pkg+"\t0.4s"))
		return 1
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)

	code := r.RunSuite(context.Background(), Suites["fast"])

	text := out.String()
	if code == 0 || !strings.Contains(text, "--- FAIL: "+pkg+" TestRunning") || !strings.Contains(text, "panic: nil pointer dereference") {
		t.Fatalf("code %d, the report does not show the panic:\n%s", code, text)
	}
	if strings.Contains(text, "TestDone") || strings.Contains(text, "(package)") {
		t.Fatalf("the report names a test that passed or the package:\n%s", text)
	}
}

// CA-TESTES-08
func TestLintRunsWithSuiteTagsAndMissingBinaryPointsToSetup(t *testing.T) {
	t.Parallel()
	for _, suite := range []string{"fast", "full"} {
		t.Run(suite, func(t *testing.T) {
			t.Parallel()
			ex := &fakeExec{missing: "golangci-lint", handle: func(Cmd, io.Writer) int { return 0 }}
			r, out := newRunner(nil, clock.NewFake(today), ex)

			code := r.RunSuite(context.Background(), Suites[suite])

			if code == 0 || !strings.Contains(out.String(), "FAIL golangci-lint did not run") ||
				!strings.Contains(out.String(), "docs/desenvolvimento/ambiente.md") {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			want := "golangci-lint run ./..."
			if suite == "full" {
				want = "golangci-lint run --build-tags full ./..."
			}
			if calls := ex.callsMatching("golangci-lint"); len(calls) != 1 || calls[0].String() != want {
				t.Fatalf("lint calls = %v, want %q", calls, want)
			}
		})
	}
}

// CA-TESTES-08
func TestLintIssuesFailTheSuite(t *testing.T) {
	t.Parallel()
	for _, suite := range []string{"fast", "full"} {
		t.Run(suite, func(t *testing.T) {
			t.Parallel()
			ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
				if c.Name == "golangci-lint" {
					_, _ = io.WriteString(out, "x.go:1:1: use of `time.Sleep` forbidden (forbidigo)\n")
					return 1
				}
				return 0
			}}
			r, out := newRunner(nil, clock.NewFake(today), ex)

			if code := r.RunSuite(context.Background(), Suites[suite]); code == 0 || !strings.Contains(out.String(), "(forbidigo)") {
				t.Fatalf("exit %d:\n%s", code, out)
			}
			if n := len(ex.callsMatching("golangci-lint")); n != 1 {
				t.Fatalf("lint ran %d times", n)
			}
		})
	}
}

func TestHeavySuiteDoesNotLint(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(Cmd, io.Writer) int { return 0 }}
	r, _ := newRunner(nil, clock.NewFake(today), ex)
	r.WriteFile = func(string, []byte) error { return nil }
	r.RunSuite(context.Background(), Suites["heavy"])
	if n := len(ex.callsMatching("golangci-lint")); n != 0 {
		t.Fatalf("heavy suite ran the linter %d times", n)
	}
}

// Every suite has a hard go test timeout a little above its budget; the
// heavy suite has none, so go test must not kill it at its default 10 min.
func TestEverySuiteHasAHardTimeoutExceptHeavy(t *testing.T) {
	t.Parallel()
	want := map[string]string{"fast": " -test.timeout=54s", "full": " -test.timeout=5m24s", "heavy": " -test.timeout=0"}
	for suite, flag := range want {
		t.Run(suite, func(t *testing.T) {
			t.Parallel()
			ex := &fakeExec{handle: func(Cmd, io.Writer) int { return 0 }}
			r, _ := newRunner(nil, clock.NewFake(today), ex)
			r.WriteFile = func(string, []byte) error { return nil }

			r.RunSuite(context.Background(), Suites[suite])

			var main []Cmd
			for _, c := range ex.callsMatching("tool test2json") {
				if isMainRun(c) {
					main = append(main, c)
				}
			}
			if len(main) != 1 || !strings.Contains(main[0].String(), flag) {
				t.Fatalf("%s suite: want %q in %v", suite, flag, main)
			}
		})
	}
}

// CA-TESTES-07
func TestHeavyWarnsAboveMedian(t *testing.T) {
	t.Parallel()
	var hist []heavyRun
	for _, m := range []float64{38, 45, 40, 39, 42, 40, 41} {
		hist = append(hist, heavyRun{At: today, Seconds: m * 60})
	}
	b, _ := json.Marshal(hist)
	fsys := fstest.MapFS{HistoryPath: {Data: b}}
	clk := clock.NewFake(today)
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		_, _ = io.WriteString(out, ev("TestMVP01_InstallAndAutoPair", "pass", 3000))
		clk.Advance(50 * time.Minute)
		return 0
	}}
	r, out := newRunner(fsys, clk, ex)
	var saved []byte
	r.WriteFile = func(p string, data []byte) error {
		if p == HistoryPath {
			saved = data
		}
		return nil
	}

	code := r.RunSuite(context.Background(), Suites["heavy"])

	if code != 0 {
		t.Fatalf("a slower heavy run must only warn:\n%s", out)
	}
	if !strings.Contains(out.String(), "warning: heavy suite took 50m0s, more than 20% above the median 40m0s of the last 7 runs") {
		t.Fatalf("no warning:\n%s", out)
	}
	var after []heavyRun
	if json.Unmarshal(saved, &after) != nil || len(after) != 8 || after[7].Seconds != 3000 {
		t.Fatalf("history not saved: %s", saved)
	}
}

func TestHeavyWithinMedianDoesNotWarn(t *testing.T) {
	t.Parallel()
	h := []heavyRun{{Seconds: 2400}, {Seconds: 2400}}
	if w := heavyWarning(h, 48*time.Minute); w != "" {
		t.Fatalf("warned at exactly +20%%: %s", w)
	}
}

// A build failure fails the suite with the compiler output; nothing runs and
// the linter is not started.
func TestBuildFailureIsReported(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(Cmd, io.Writer) int { return 0 }}
	ex.build = func(c Cmd, out io.Writer) int {
		if !c.MergeStderr {
			t.Error("the compiler output (standard error) is not captured")
		}
		_, _ = io.WriteString(out, "# "+pkg+"\nx.go:3:1: syntax error\n")
		return 1
	}
	r, out := newRunner(nil, clock.NewFake(today), ex)
	code := r.RunSuite(context.Background(), Suites["fast"])
	if code == 0 || !strings.Contains(out.String(), "x.go:3:1: syntax error") || !strings.Contains(out.String(), "FAIL fast: building the test binaries failed") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if n := len(ex.callsMatching("tool test2json")) + len(ex.callsMatching("golangci-lint")); n != 0 {
		t.Fatalf("ran tests or lint after the build failed (%d calls)", n)
	}
}

// The build of the test binaries is outside the budget: a slow build does not
// fail a suite whose run is inside it, its time is printed on its own line
// and the run is what is timed.
func TestTheBuildIsNotCountedInTheBudgetButTheRunIs(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		build, run time.Duration
		wantFail   bool
		wantLine   string
	}{
		"slow build, fast run": {5 * time.Minute, 20 * time.Second, false, "PASS fast: 1 tests in 20s (budget 36s)"},
		"fast build, slow run": {2 * time.Second, 37 * time.Second, true, "FAIL fast suite took 37s, budget 36s"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(today)
			ex := &fakeExec{handle: func(cmd Cmd, out io.Writer) int {
				if isMainRun(cmd) {
					_, _ = io.WriteString(out, ev("TestOne", "pass", 0.01))
					clk.Advance(c.run)
				}
				return 0
			}}
			ex.build = func(Cmd, io.Writer) int { clk.Advance(c.build); return 0 }
			r, out := newRunner(nil, clk, ex)

			code := r.RunSuite(context.Background(), Suites["fast"])

			text := out.String()
			if (code != 0) != c.wantFail || !strings.Contains(text, c.wantLine) {
				t.Fatalf("exit %d, want line %q:\n%s", code, c.wantLine, text)
			}
			want := "build took " + c.build.String()
			if name == "slow build, fast run" {
				want = "build took 5m0s"
			}
			if !strings.Contains(text, want+" ") {
				t.Fatalf("no %q line:\n%s", want, text)
			}
		})
	}
}

// Every binary is built with the tags and flags of the suite and run from its
// own package directory; the quarantine and -count go to the binary as -test.*.
func TestBinariesAreBuiltWithTheSuiteFlagsAndRunInThePackageDirectory(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		_, _ = io.WriteString(out, ev("TestOne", "pass", 0.01))
		return 0
	}}
	r, _ := newRunner(nil, clock.NewFake(today), ex)
	r.Race, r.Shuffle = true, true

	r.RunSuite(context.Background(), Suites["full"])

	b := ex.callsMatching("test -c")
	if len(b) != 1 {
		t.Fatalf("build calls = %v", b)
	}
	for _, want := range []string{"-o /bins/", "-tags full", "-race " + RaceGcflags, pkg} {
		if !strings.Contains(strings.ReplaceAll(b[0].String(), "\\", "/"), want) {
			t.Fatalf("build lacks %q: %s", want, b[0])
		}
	}
	if !slices.Contains(b[0].Env, "CGO_ENABLED=1") {
		t.Fatalf("build env = %v", b[0].Env)
	}
	run := ex.callsMatching("tool test2json")
	if len(run) != 1 || run[0].Dir != "internal/sample" || !strings.Contains(run[0].String(), "-test.shuffle=on") || !strings.Contains(run[0].String(), "-test.v=test2json") {
		t.Fatalf("run calls = %v", run)
	}
}

func TestQuarantineModeRunsTwentyTimes(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{QuarantinePath: {Data: []byte("tests:\n  - {test: TestFlaky, issue: '#3', added: 2026-10-10, reason: r}\n")}}
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		_, _ = io.WriteString(out, strings.Repeat(ev("TestFlaky", "pass", 0.01), 20))
		return 0
	}}
	r, out := newRunner(fsys, clock.NewFake(today), ex)
	if code := r.Quarantine(context.Background()); code != 0 || !strings.Contains(out.String(), "TestFlaky passed 20/20: it can leave the quarantine") {
		t.Fatalf("code %d:\n%s", code, out)
	}
	if c := ex.callsMatching("-count=20"); len(c) != 1 || !strings.Contains(c[0].String(), "-run ^TestFlaky$") {
		t.Fatalf("calls = %v", ex.calls)
	}
}

func TestQuarantineRejectsBadEntries(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{QuarantinePath: {Data: []byte("tests:\n  - {test: TestA/sub, issue: x, added: 2026-10-10, reason: r}\n")}}
	if _, err := loadQuarantine(fsys); err == nil {
		t.Fatal("subtest accepted")
	}
}

func TestRealQuarantineFileIsValid(t *testing.T) {
	t.Parallel()
	if _, err := loadQuarantine(os.DirFS("../..")); err != nil {
		t.Fatal(err)
	}
}
