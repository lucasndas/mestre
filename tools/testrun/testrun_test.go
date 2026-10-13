package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

const pkg = "github.com/lucasndas/mestre/internal/sample"

var today = time.Date(2026, 10, 12, 10, 0, 0, 0, time.UTC)

// fakeExec answers commands with recorded output and keeps the calls.
type fakeExec struct {
	mu      sync.Mutex
	calls   []Cmd
	handle  func(c Cmd, out io.Writer) int
	missing string // a command name that is not installed
}

func (f *fakeExec) Run(_ context.Context, c Cmd, out io.Writer) (int, error) {
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	if c.Name == f.missing {
		return -1, fmt.Errorf("run %s: exec: %q: executable file not found in $PATH", c, c.Name)
	}
	return f.handle(c, out), nil
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
	return &Runner{Exec: ex, Clock: clk, FS: fsys, Out: &out}, &out
}

func isMainRun(c Cmd) bool { return c.Name == "go" && strings.Contains(c.String(), "./...") }

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
	if !strings.Contains(text, "FAIL fast suite took 31.5s, budget 30s. The 10 slowest tests:") {
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
			_, _ = io.WriteString(out, ev("TestSlow", "pass", 0.15)+ev("TestQuick", "pass", 0.05)+ev("", "pass", 0.2))
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
	if n := len(ex.callsMatching("-run ^TestSlow$")); n != 1 {
		t.Fatalf("TestSlow timed alone %d times, want 1", n)
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
	if c := ex.callsMatching("-run"); len(c) != 1 || !strings.Contains(c[0].String(), "-run ^TestBusy$/^sub_case$") {
		t.Fatalf("calls = %v", c)
	}
}

// CA-TESTES-03
func TestQuarantineSkipsAndExpiredFailsFull(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{QuarantinePath: {Data: []byte(`tests:
  - test: TestRecent
    issue: https://github.com/lucasndas/mestre/issues/1
    added: 2026-10-01
    reason: races on slow disks
  - test: TestOld
    issue: https://github.com/lucasndas/mestre/issues/2
    added: 2026-09-20
    reason: flaky pairing
`)}}
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		_, _ = io.WriteString(out, ev("TestOther", "pass", 0.01))
		return 0
	}}
	r, out := newRunner(fsys, clock.NewFake(today), ex)

	code := r.RunSuite(context.Background(), Suites["full"])

	main := ex.callsMatching("go test")
	if len(main) != 1 || !strings.Contains(main[0].String(), "-tags full -skip ^(TestRecent|TestOld)$ ./...") {
		t.Fatalf("go test call = %v", main)
	}
	text := out.String()
	if !strings.Contains(text, "TestRecent (https://github.com/lucasndas/mestre/issues/1") ||
		!strings.Contains(text, "TestOld (https://github.com/lucasndas/mestre/issues/2, since 2026-09-20: flaky pairing) EXPIRED") {
		t.Fatalf("skipped list:\n%s", text)
	}
	if strings.Contains(text, "TestRecent (https://github.com/lucasndas/mestre/issues/1, since 2026-10-01: races on slow disks) EXPIRED") {
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
	if n := len(ex.callsMatching("go test")); n != 1 {
		t.Fatalf("go test called %d times, want once: %v", n, ex.calls)
	}
	if text := out.String(); !strings.Contains(text, "--- FAIL: "+pkg+" TestBroken") || !strings.Contains(text, "sample_test.go:9: boom") {
		t.Fatalf("failure report:\n%s", text)
	}
}

// CA-TESTES-06
func TestAffectedCallsVitestRelatedWithChangedPanelFiles(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"web/package.json": {Data: []byte("{}")}}
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		switch c.String() {
		case "git diff --name-only HEAD":
			_, _ = io.WriteString(out, "web/src/canvas.tsx\ninternal/x/x.go\n")
		case "git ls-files --others --exclude-standard":
			_, _ = io.WriteString(out, "web/src/new.test.ts\ndocs/a.md\n")
		}
		return 0
	}}
	r, _ := newRunner(fsys, clock.NewFake(today), ex)

	if code := r.Affected(context.Background()); code != 0 {
		t.Fatalf("code %d", code)
	}

	v := ex.callsMatching("vitest")
	if len(v) != 1 || v[0].Dir != "web" || v[0].String() != "npx vitest related --run --reporter=json src/canvas.tsx src/new.test.ts" {
		t.Fatalf("vitest calls = %+v", v)
	}
	g := ex.callsMatching("go test")
	if len(g) != 1 || strings.Contains(g[0].String(), "-count") {
		t.Fatalf("go test must use the test cache: %v", g)
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

func TestLintIssuesFailTheSuite(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		if c.Name == "golangci-lint" {
			_, _ = io.WriteString(out, "x.go:1:1: use of `time.Sleep` forbidden (forbidigo)\n")
			return 1
		}
		return 0
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)

	if code := r.RunSuite(context.Background(), Suites["fast"]); code == 0 || !strings.Contains(out.String(), "(forbidigo)") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if n := len(ex.callsMatching("golangci-lint")); n != 1 {
		t.Fatalf("lint ran %d times", n)
	}
	r2, _ := newRunner(nil, clock.NewFake(today), ex)
	r2.RunSuite(context.Background(), Suites["heavy"])
	if n := len(ex.callsMatching("golangci-lint")); n != 1 {
		t.Fatalf("heavy suite ran the linter")
	}
}

// The heavy suite has no hard limit, so go test must not kill it at its
// default 10 minutes; the timed suites keep the default.
func TestOnlyHeavyDisablesTheGoTestTimeout(t *testing.T) {
	t.Parallel()
	for _, suite := range []string{"fast", "full", "heavy"} {
		t.Run(suite, func(t *testing.T) {
			t.Parallel()
			ex := &fakeExec{handle: func(Cmd, io.Writer) int { return 0 }}
			r, _ := newRunner(nil, clock.NewFake(today), ex)
			r.WriteFile = func(string, []byte) error { return nil }

			r.RunSuite(context.Background(), Suites[suite])

			var main []Cmd
			for _, c := range ex.callsMatching("./...") {
				if isMainRun(c) {
					main = append(main, c)
				}
			}
			if len(main) != 1 {
				t.Fatalf("go test calls = %v", main)
			}
			has := strings.Contains(main[0].String(), " -timeout 0 ")
			if has != (suite == "heavy") {
				t.Fatalf("%s suite: %s", suite, main[0])
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

func TestBuildFailureIsReported(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		line, _ := json.Marshal(event{Action: "build-output", ImportPath: pkg, Output: "x.go:3:1: syntax error\n"})
		_, _ = fmt.Fprintf(out, "%s\n%s", line, ev("", "fail", 0))
		return 1
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)
	if code := r.RunSuite(context.Background(), Suites["fast"]); code == 0 || !strings.Contains(out.String(), "x.go:3:1: syntax error") {
		t.Fatalf("code %d:\n%s", code, out)
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
