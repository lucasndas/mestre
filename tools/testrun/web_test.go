package main

import (
	"context"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

func webFS(extra map[string]string) fstest.MapFS {
	m := fstest.MapFS{
		"web/package.json":                    {Data: []byte("{}")},
		"web/node_modules/.package-lock.json": {Data: []byte("{}")},
	}
	for p, c := range extra {
		m[p] = &fstest.MapFile{Data: []byte(c)}
	}
	return m
}

// vitestReport is the JSON of the Vitest reporter for the given tests: each
// "name:status:ms".
func vitestReport(tests ...string) string {
	return vitestReportSpan(0, tests...)
}

// vitestReportSpan is vitestReport with the tests running for span ms between
// the start and the end of the file (the sum of the tests when span is 0).
func vitestReportSpan(span int, tests ...string) string {
	var as []string
	failed, sum := 0, 0
	for _, t := range tests {
		parts := strings.Split(t, ":")
		name, status := parts[0], parts[1]
		ms, err := strconv.Atoi(parts[2])
		if err != nil {
			panic(err)
		}
		sum += ms
		msgs := "[]"
		if status == "failed" {
			failed++
			msgs = `["AssertionError: boom"]`
		}
		as = append(as, fmt.Sprintf(`{"fullName":%q,"status":%q,"duration":%d,"failureMessages":%s}`, name, status, ms, msgs))
	}
	if span == 0 {
		span = sum
	}
	return fmt.Sprintf(`some noise
{"numFailedTests":%d,"startTime":1000,"testResults":[{"name":"/w/src/a.test.ts","status":"passed","startTime":5000,"endTime":%d,"assertionResults":[%s]}]}
`, failed, 5000+span, strings.Join(as, ","))
}

func isNpx(c Cmd, tool string) bool {
	return c.Name == "npx" && len(c.Args) > 1 && c.Args[1] == tool
}

// webExec answers the three tools of the web side; vitest answers with report.
func webExec(clk *clock.Fake, vitestTook time.Duration, report func() string) *fakeExec {
	return &fakeExec{handle: func(c Cmd, out io.Writer) int {
		switch {
		case isNpx(c, "vitest"):
			clk.Advance(vitestTook)
			_, _ = io.WriteString(out, report())
			if strings.Contains(report(), `"failed"`) {
				return 1
			}
		case isMainRun(c):
			clk.Advance(20 * time.Second)
		}
		return 0
	}}
}

// CA-TESTES-06
func TestWebSideRunsVitestBiomeAndTscSideBySide(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(today)
	ex := webExec(clk, 3*time.Second, func() string { return vitestReport("works:passed:5") })
	r, out := newRunner(webFS(nil), clk, ex)

	if code := r.RunSuite(context.Background(), Suites["fast"]); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	for _, want := range []string{"npx --no-install vitest run --reporter=json", "npx --no-install biome check .", "npx --no-install tsc --noEmit"} {
		calls := ex.callsMatching(want)
		if len(calls) != 1 || calls[0].Dir != "web" {
			t.Errorf("%q: calls %v", want, calls)
		}
	}
	if !strings.Contains(out.String(), "PASS fast web: 1 tests in 5ms (budget 30s)") || len(ex.callsMatching("tool test2json")) != 1 {
		t.Fatalf("output:\n%s", out)
	}

	// Each side alone.
	for _, only := range []string{OnlyGo, OnlyWeb} {
		ex := webExec(clk, time.Second, func() string { return vitestReport("works:passed:5") })
		r, out := newRunner(webFS(nil), clk, ex)
		r.Only = only
		if code := r.RunSuite(context.Background(), Suites["fast"]); code != 0 {
			t.Fatalf("only %s: exit %d:\n%s", only, code, out)
		}
		if got := len(ex.callsMatching("tool test2json")) == 1; got != (only == OnlyGo) {
			t.Errorf("only %s: go side ran = %v", only, got)
		}
		if got := len(ex.callsMatching("npx")) == 3; got != (only == OnlyWeb) {
			t.Errorf("only %s: web side ran = %v", only, got)
		}
	}

	// Without the installed packages the web side asks for npm ci.
	fsys := webFS(nil)
	delete(fsys, "web/node_modules/.package-lock.json")
	r, out = newRunner(fsys, clk, webExec(clk, time.Second, func() string { return "" }))
	r.Only = OnlyWeb
	if code := r.RunSuite(context.Background(), Suites["fast"]); code != 2 || !strings.Contains(out.String(), "npm ci") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
}

// A failing test, a failing linter and a failing type check each fail the
// suite, and a failed test is run once.
func TestWebFailuresFailTheSuite(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(today)
	ex := webExec(clk, time.Second, func() string { return vitestReport("breaks:failed:5") })
	r, out := newRunner(webFS(nil), clk, ex)
	r.Only = OnlyWeb
	if code := r.RunSuite(context.Background(), Suites["fast"]); code != 1 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if !strings.Contains(out.String(), "--- FAIL: /w/src/a.test.ts breaks") || !strings.Contains(out.String(), "AssertionError: boom") || len(ex.callsMatching("vitest")) != 1 {
		t.Fatalf("output:\n%s", out)
	}

	for _, tool := range []string{"biome", "tsc"} {
		ex := &fakeExec{handle: func(c Cmd, w io.Writer) int {
			if isNpx(c, "vitest") {
				_, _ = io.WriteString(w, vitestReport("ok:passed:5"))
			}
			if isNpx(c, tool) {
				_, _ = io.WriteString(w, tool+" says no\n")
				return 1
			}
			return 0
		}}
		r, out := newRunner(webFS(nil), clk, ex)
		r.Only = OnlyWeb
		if code := r.RunSuite(context.Background(), Suites["fast"]); code != 1 || !strings.Contains(out.String(), "FAIL "+tool+" (exit 1)") {
			t.Fatalf("%s: exit %d:\n%s", tool, code, out)
		}
	}
}

// checkGateClock is a fake clock that tells when the Biome and tsc goroutines
// of RunWeb have read their end time (the Since in their closure), so a test
// can move the shared clock only after that and keep their duration at zero.
type checkGateClock struct {
	*clock.Fake
	checksDone sync.WaitGroup
}

func newCheckGateClock(f *clock.Fake) *checkGateClock {
	c := &checkGateClock{Fake: f}
	c.checksDone.Add(2)
	return c
}

func (c *checkGateClock) Now() time.Time {
	now := c.Fake.Now()
	pcs := make([]uintptr, 16)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	var since, check bool
	for {
		f, more := frames.Next()
		since = since || strings.HasSuffix(f.Function, "clock.Since")
		check = check || strings.Contains(f.Function, "RunWeb.func")
		if !more {
			break
		}
	}
	if since && check {
		c.checksDone.Done()
	}
	return now
}

// CA-TESTES-15
func TestWebSideHasItsOwnBudgetAndTheGoOnesStay(t *testing.T) {
	t.Parallel()
	if WebBudget != 30*time.Second || Suites["fast"].Budget != 36*time.Second || Suites["full"].Budget != 216*time.Second {
		t.Fatalf("budgets: web %v, fast %v, full %v", WebBudget, Suites["fast"].Budget, Suites["full"].Budget)
	}
	run := func(wall time.Duration, execMs int) (int, string) {
		fake := clock.NewFake(today)
		clk := newCheckGateClock(fake)
		ex := webExec(fake, wall, func() string { return vitestReportSpan(execMs, "a:passed:300", "b:passed:20") })
		// Vitest moves the clock only after Biome and tsc have read their end
		// time; the checks would count that time.
		inner := ex.handle
		ex.handle = func(c Cmd, out io.Writer) int {
			if isNpx(c, "vitest") {
				clk.checksDone.Wait()
			}
			return inner(c, out)
		}
		r, out := newRunner(webFS(nil), clk, ex)
		return r.RunSuite(context.Background(), Suites["fast"]), out.String()
	}
	// Go takes 20 s (under its 36 s) and the tests 29 s (under 30 s).
	if code, text := run(30*time.Second, 29000); code != 0 || !strings.Contains(text, "PASS fast web") || !strings.Contains(text, "slow: 1 web test(s)") {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	// The preparation does not count: 55 s in all, 29 s of them in the tests.
	code, text := run(55*time.Second, 29000)
	if code != 0 || !strings.Contains(text, "vitest prepare took 26s, not counted in the budget") || !strings.Contains(text, "PASS fast web: 2 tests in 29s (budget 30s)") {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	// The execution does: 31 s in the tests fail even with a short preparation.
	code, text = run(32*time.Second, 31000)
	if code != 1 || !strings.Contains(text, "FAIL web side of fast took 31s, budget 30s. The 10 slowest tests:") || !strings.Contains(text, "a") {
		t.Fatalf("exit %d:\n%s", code, text)
	}
	if strings.Contains(text, "FAIL fast suite took") {
		t.Fatalf("the Go budget failed for the web side:\n%s", text)
	}
}

// CA-TESTES-16
func TestWebQuarantineShuffleCountAndAffected(t *testing.T) {
	t.Parallel()
	q := "tests:\n  - test: src/flaky.test.ts\n    suite: web\n    issue: https://example.test/1\n    added: 2026-10-20\n    reason: unstable\n"
	clk := clock.NewFake(today)
	run := 0
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		if isNpx(c, "vitest") {
			run++
			status := "passed"
			if run%2 == 0 {
				status = "failed"
			}
			_, _ = io.WriteString(out, vitestReport("a b:"+status+":5"))
			if status == "failed" {
				return 1
			}
		}
		return 0
	}}
	r, out := newRunner(webFS(map[string]string{"test/quarantine.yaml": q}), clk, ex)
	r.Only, r.Shuffle, r.Count = OnlyWeb, true, 5
	code := r.RunSuite(context.Background(), Suites["fast"])
	text := out.String()
	vs := ex.callsMatching("vitest run")
	if code != 1 || len(vs) != 5 {
		t.Fatalf("exit %d, %d vitest runs:\n%s", code, len(vs), text)
	}
	if !strings.Contains(vs[0].String(), "--sequence.shuffle") || !strings.Contains(vs[0].String(), "--exclude src/flaky.test.ts") {
		t.Errorf("args: %v", vs[0])
	}
	if !strings.Contains(text, "mixed: /w/src/a.test.ts a_b passed 3 failed 2") || !strings.Contains(text, "budget 2m30s") {
		t.Fatalf("output:\n%s", text)
	}
	if len(ex.callsMatching("biome")) != 0 {
		t.Error("the linter ran with -count")
	}

	// affected: the files of web/ changed since HEAD, and the new ones.
	ex = &fakeExec{handle: func(c Cmd, out io.Writer) int {
		switch {
		case c.Name == "git" && c.Args[0] == "diff":
			_, _ = io.WriteString(out, "web/src/App.tsx\ndocs/x.md\n")
		case c.Name == "git":
			_, _ = io.WriteString(out, "web/src/new.ts\n")
		case isNpx(c, "vitest"):
			_, _ = io.WriteString(out, vitestReport("a:passed:5"))
		}
		return 0
	}}
	r, out = newRunner(webFS(map[string]string{"test/quarantine.yaml": q}), clk, ex)
	r.Only = OnlyWeb
	if code := r.Affected(context.Background()); code != 0 {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	rel := ex.callsMatching("vitest related")
	if len(rel) != 1 || !strings.Contains(rel[0].String(), "--exclude src/flaky.test.ts") || !strings.HasSuffix(rel[0].String(), "src/App.tsx src/new.ts") || !strings.Contains(rel[0].String(), "--run") {
		t.Fatalf("calls %v", rel)
	}
}

// Vitest 5 writes the JSON reporter to the file of --outputFile, not to
// standard output: the report is read from there and the file is removed.
func TestWebReportIsReadFromTheOutputFile(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(today)
	fsys := webFS(nil)
	var removed []string
	ex := &fakeExec{handle: func(c Cmd, out io.Writer) int {
		if isNpx(c, "vitest") {
			for _, a := range c.Args {
				if name, ok := strings.CutPrefix(a, "--outputFile="); ok {
					fsys["web/"+name] = &fstest.MapFile{Data: []byte(vitestReport("from file:failed:5"))}
				}
			}
			return 1
		}
		return 0
	}}
	r, out := newRunner(fsys, clk, ex)
	r.Only = OnlyWeb
	r.Remove = func(p string) error { removed = append(removed, p); return nil }
	if code := r.RunSuite(context.Background(), Suites["fast"]); code != 1 || !strings.Contains(out.String(), "--- FAIL: /w/src/a.test.ts from file") {
		t.Fatalf("exit %d:\n%s", code, out)
	}
	if len(removed) != 1 || !strings.HasPrefix(removed[0], "web/.vitest/testrun-") {
		t.Fatalf("removed %v", removed)
	}
}
