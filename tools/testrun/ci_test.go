package main

import (
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// The solo re-runs of slow tests are one go test per package, not per test.
func TestSlowTestsAreTimedAloneInOneRunPerPackage(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		switch {
		case isMainRun(c):
			_, _ = io.WriteString(out, ev("TestA", "pass", 0.25)+ev("TestB", "pass", 0.3)+ev("TestC", "pass", 0.4)+ev("TestQuick", "pass", 0.01))
		case strings.Contains(c.String(), "-test.run"):
			_, _ = io.WriteString(out, ev("TestA", "pass", 0.15)+ev("TestB", "pass", 0.02)+ev("TestC", "pass", 0.3))
		}
		return 0
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)

	if code := r.RunSuite(context.Background(), Suites["fast"]); code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}

	calls := ex.callsMatching("-test.run")
	if len(calls) != 1 || !strings.Contains(calls[0].String(), "-test.run ^(TestA|TestB|TestC)$") {
		t.Fatalf("solo runs = %v", calls)
	}
	text := out.String()
	if !strings.Contains(text, "warning: 2 fast-suite test(s) above 100ms") || strings.Contains(text, "TestB") || strings.Contains(text, "TestQuick") {
		t.Fatalf("only TestA and TestC are slow alone:\n%s", text)
	}
}

func TestRunPatternKeepsSubtestLevels(t *testing.T) {
	t.Parallel()
	mk := func(names ...string) []*result {
		var rs []*result
		for _, n := range names {
			rs = append(rs, &result{Test: n})
		}
		return rs
	}
	cases := map[string]struct {
		tests []*result
		want  string
	}{
		"single":        {mk("TestA"), "^TestA$"},
		"subtest":       {mk("TestA/x_y"), "^TestA$/^x_y$"},
		"several":       {mk("TestA", "TestB"), "^(TestA|TestB)$"},
		"same parent":   {mk("TestA/x", "TestA/y"), "^TestA$/^(x|y)$"},
		"quoted":        {mk("TestA/a.b"), `^TestA$/^a\.b$`},
		"mixed depth":   {mk("TestA", "TestB/x"), "^(TestA|TestB)$/^x$"},
		"duplicate leg": {mk("TestA/x", "TestA/x"), "^TestA$/^x$"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if got := runPattern(c.tests); got != c.want {
				t.Fatalf("runPattern = %q, want %q", got, c.want)
			}
		})
	}
}

// CA-TESTES-10
// The wall time that the budget checks includes the solo re-runs, but they
// only get what the budget has left (less a tenth): the measuring never makes
// a suite fail, and a failing suite is not made worse by it.
func TestSoloRerunsCountInTheWallTimeWithinTheBudgetLeft(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		suite, rerun time.Duration // time of the suite run and of the re-run if allowed to finish
		wantExit     bool
		wantRerun    bool
		wantLine     string
	}{
		"fits":         {24 * time.Second, 5 * time.Second, false, true, "PASS fast: 1 tests in 29s (budget 36s, 5s of it timing slow tests alone)"},
		"cut off":      {24 * time.Second, 15 * time.Second, false, true, "PASS fast: 1 tests in 32.4s (budget 36s, 8.4s of it timing slow tests alone)"},
		"no time left": {33 * time.Second, 5 * time.Second, false, false, "PASS fast: 1 tests in 33s (budget 36s)"},
		"over budget":  {37 * time.Second, 5 * time.Second, true, false, "FAIL fast: 1 tests in 37s (budget 36s)"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(today)
			ex := &fakeExec{handle: func(cmd Cmd, out io.Writer) int {
				switch {
				case isMainRun(cmd):
					_, _ = io.WriteString(out, ev("TestSlow", "pass", 0.5))
					clk.Advance(c.suite)
				case strings.Contains(cmd.String(), "-test.run"):
					_, _ = io.WriteString(out, ev("TestSlow", "pass", 0.5))
					clk.Advance(min(c.rerun, 8400*time.Millisecond)) // killed at the allowance, 36s - 24s - 3.6s
				}
				return 0
			}}
			r, out := newRunner(nil, clk, ex)

			code := r.RunSuite(context.Background(), Suites["fast"])

			text := out.String()
			if (code != 0) != c.wantExit || !strings.Contains(text, c.wantLine) {
				t.Fatalf("exit %d, want line %q:\n%s", code, c.wantLine, text)
			}
			if got := len(ex.callsMatching("-test.run")) > 0; got != c.wantRerun {
				t.Fatalf("re-run = %v, want %v", got, c.wantRerun)
			}
			if name == "fits" && !strings.Contains(text, "above 100ms when run alone") || name == "no time left" && !strings.Contains(text, "above 100ms in the suite (not re-run)") {
				t.Fatalf("the warning does not say how the time was measured:\n%s", text)
			}
			if name == "fits" && strings.Contains(text, "not re-run") {
				t.Fatalf("a test measured within the budget is not reported as unmeasured:\n%s", text)
			}
			if name != "fits" && !strings.Contains(text, "TestSlow (not re-run: no budget left)") && name != "over budget" {
				t.Fatalf("an unmeasured test must say so:\n%s", text)
			}
		})
	}
}

// CA-TESTES-09
// With -count > 1 a test that passes and fails is named; a later pass does not
// hide the failure; the budget scales with the count.
func TestMixedResultsAreNamedWithCount(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(today)
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		if c.Name != "go" {
			return 0 // the linter
		}
		_, _ = io.WriteString(out,
			ev("TestFlaky", "fail", 0.01, "flaky_test.go:7: boom")+ev("TestFlaky", "pass", 0.01)+
				ev("TestFlaky", "pass", 0.01)+ev("TestFlaky", "fail", 0.01)+ev("TestFlaky", "pass", 0.01)+
				strings.Repeat(ev("TestSteady", "pass", 0.01), 5)+
				ev("TestBroken", "fail", 0.01)+strings.Repeat(ev("TestBroken", "fail", 0.01), 4))
		clk.Advance(100 * time.Second) // over the 36s budget of one run, under five
		return 1
	}}
	r, out := newRunner(nil, clk, ex)
	r.Count, r.Shuffle = 5, true

	code := r.RunSuite(context.Background(), Suites["fast"])

	text := out.String()
	if code == 0 {
		t.Fatalf("exit 0:\n%s", text)
	}
	for _, want := range []string{
		"flaky: 1 test(s) passed and failed in the same 5 runs:",
		"mixed: " + pkg + " TestFlaky passed 3 failed 2",
		"budget 3m0s",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "mixed: "+pkg+" TestBroken") || strings.Contains(text, "mixed: "+pkg+" TestSteady") {
		t.Fatalf("only TestFlaky is mixed:\n%s", text)
	}
	if strings.Contains(text, "suite took") {
		t.Fatalf("budget not scaled by -count:\n%s", text)
	}
	run := ex.callsMatching("-test.count=5")
	if len(run) != 1 || !strings.Contains(run[0].String(), "-test.shuffle=on") || !strings.Contains(run[0].String(), "-test.timeout=4m30s") {
		t.Fatalf("go test call = %v", run)
	}
	if n := len(ex.callsMatching("-test.run")); n != 0 {
		t.Fatalf("slow tests are not timed with -count > 1 (%d re-runs)", n)
	}
	if n := len(ex.callsMatching("golangci-lint")); n != 0 {
		t.Fatalf("the linter runs once, not per -count (%d runs)", n)
	}
}

func TestLaterPassDoesNotHideAFailure(t *testing.T) {
	t.Parallel()
	rep := parseEvents([]byte(ev("TestX", "fail", 0.1) + ev("TestX", "pass", 0.1)))
	if got := rep.tests[pkg+".TestX"]; got.Action != "fail" || got.Pass != 1 || got.Fail != 1 {
		t.Fatalf("result = %+v", got)
	}
}

// In CI the slow tests also go to the job summary and to a ::warning.
func TestSlowTestsGoToTheJobSummary(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		_, _ = io.WriteString(out, ev("TestSlow", "pass", 0.25))
		return 0
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)
	var mu sync.Mutex
	files := map[string]string{}
	r.Getenv = func(k string) string {
		if k == "GITHUB_STEP_SUMMARY" {
			return "/summary.md"
		}
		return ""
	}
	r.AppendFile = func(p string, data []byte) error {
		mu.Lock()
		defer mu.Unlock()
		files[p] += string(data)
		return nil
	}

	if code := r.RunSuite(context.Background(), Suites["fast"]); code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}

	mu.Lock()
	defer mu.Unlock()
	if s := files["/summary.md"]; !strings.Contains(s, "### Slow tests") || !strings.Contains(s, "| "+pkg+" | TestSlow |") {
		t.Fatalf("summary = %q", s)
	}
	if !strings.Contains(out.String(), "::warning title=Slow tests::1 fast-suite test(s) above 100ms when run alone: TestSlow") {
		t.Fatalf("no annotation:\n%s", out)
	}
}

func TestOutsideCINoAnnotations(t *testing.T) {
	t.Parallel()
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		_, _ = io.WriteString(out, ev("TestSlow", "pass", 0.25))
		return 0
	}}
	r, out := newRunner(nil, clock.NewFake(today), ex)
	r.Getenv = func(string) string { return "" }
	r.AppendFile = func(string, []byte) error { t.Error("summary written outside CI"); return nil }
	r.RunSuite(context.Background(), Suites["fast"])
	if strings.Contains(out.String(), "::") {
		t.Fatalf("annotation outside CI:\n%s", out)
	}
}

func TestEscapeWorkflowCommands(t *testing.T) {
	t.Parallel()
	if got := escapeData("a%b\nc"); got != "a%25b%0Ac" {
		t.Fatalf("data = %q", got)
	}
	if got := escapeProperty("a:b,c"); got != "a%3Ab%2Cc" {
		t.Fatalf("property = %q", got)
	}
}

// CA-TESTES-06
// The quarantine applies to `affected` too.
func TestAffectedSkipsTheQuarantine(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{QuarantinePath: {Data: []byte("tests:\n  - {test: TestFlaky, issue: '#3', added: 2026-10-10, reason: r}\n")}}
	ex := &fakeExec{handle: func(Cmd, io.Writer) int { return 0 }}
	r, out := newRunner(fsys, clock.NewFake(today), ex)

	if code := r.Affected(context.Background()); code != 0 {
		t.Fatalf("code %d:\n%s", code, out)
	}

	g := ex.callsMatching("go test")
	if len(g) != 1 || !strings.Contains(g[0].String(), "-skip ^(TestFlaky)$") || strings.Contains(g[0].String(), "-count") {
		t.Fatalf("affected go test = %v (must skip the quarantine and keep the test cache)", g)
	}
	if !strings.Contains(out.String(), "skipped (quarantine") {
		t.Fatalf("skipped list missing:\n%s", out)
	}
}

// The nightly quarantine run uses the build tags of the entry's suite.
func TestQuarantineRunsWithTheSuiteTags(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ entry, wantTags string }{
		"default is full": {"{test: TestA, issue: '#1', added: 2026-10-10, reason: r}", "-tags full "},
		"fast":            {"{test: TestA, suite: fast, issue: '#1', added: 2026-10-10, reason: r}", ""},
		"heavy":           {"{test: TestA, suite: heavy, issue: '#1', added: 2026-10-10, reason: r}", "-tags full,heavy "},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{QuarantinePath: {Data: []byte("tests:\n  - " + c.entry + "\n")}}
			ex := &fakeExec{handle: func(Cmd, io.Writer) int { return 0 }}
			r, _ := newRunner(fsys, clock.NewFake(today), ex)

			r.Quarantine(context.Background())

			calls := ex.callsMatching("-count=20")
			if len(calls) != 1 {
				t.Fatalf("calls = %v", ex.calls)
			}
			got := calls[0].String()
			hasTags := strings.Contains(got, "-tags ")
			if (c.wantTags == "") == hasTags || (c.wantTags != "" && !strings.Contains(got, c.wantTags)) {
				t.Fatalf("call = %s, want tags %q", got, c.wantTags)
			}
		})
	}
	bad := fstest.MapFS{QuarantinePath: {Data: []byte("tests:\n  - {test: TestA, suite: nightly, issue: x, added: 2026-10-10, reason: r}\n")}}
	if _, err := loadQuarantine(bad); err == nil {
		t.Fatal("unknown suite accepted")
	}
}

func TestQuarantineSaysWhichTestsMayLeaveInCI(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{QuarantinePath: {Data: []byte("tests:\n  - {test: TestFlaky, issue: '#3', added: 2026-10-10, reason: r}\n")}}
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		_, _ = io.WriteString(out, strings.Repeat(ev("TestFlaky", "pass", 0.01), 20))
		return 0
	}}
	r, out := newRunner(fsys, clock.NewFake(today), ex)
	var summary string
	r.Getenv = func(string) string { return "/s.md" }
	r.AppendFile = func(_ string, data []byte) error { summary += string(data); return nil }

	r.Quarantine(context.Background())

	if !strings.Contains(out.String(), "::notice title=Quarantine::1 quarantined test(s) passed every run and can leave") || !strings.Contains(summary, "TestFlaky") {
		t.Fatalf("out:\n%s\nsummary: %q", out, summary)
	}
}
