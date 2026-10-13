package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"strings"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Cmd is one command the runner starts.
type Cmd struct {
	Name string
	Args []string
	Dir  string   // relative to the repository root; "" is the root
	Env  []string // added to the runner environment
}

func (c Cmd) String() string { return strings.TrimSpace(c.Name + " " + strings.Join(c.Args, " ")) }

// Exec runs commands; the tests replace it with recorded output.
type Exec interface {
	// Run runs c, writes its standard output to stdout and returns the
	// exit code. err means the command could not run.
	Run(ctx context.Context, c Cmd, stdout io.Writer) (int, error)
}

// Suite is one test suite (docs/qualidade/testes.md#suítes).
type Suite struct {
	Name     string
	Tags     string
	Budget   time.Duration // 0: no hard limit (measured)
	FlagSlow bool
	Lint     bool   // also run golangci-lint, in parallel with the tests
	Timeout  string // -timeout of go test; "" derives it from Budget; "0" is none
}

// Suites by executor name.
var Suites = map[string]Suite{
	"fast":  {Name: "fast", Budget: 30 * time.Second, FlagSlow: true, Lint: true},
	"full":  {Name: "full", Tags: "full", Budget: 3 * time.Minute, Lint: true},
	"heavy": {Name: "heavy", Tags: "full,heavy", Timeout: "0"}, // measured, no hard limit
}

// RaceFactor multiplies the budget (and the go test timeout) of a suite run
// with -race: the detector slows tests down by several times
// (docs/qualidade/testes.md#executor-testrun).
const RaceFactor = 3

// Limits of the runner.
const (
	slowTest     = 100 * time.Millisecond
	slowestShown = 10
	quarantineN  = 20
	envSuite     = "MESTRE_TEST_SUITE"
	// LintDoc says how to install the pinned golangci-lint.
	LintDoc = "docs/desenvolvimento/ambiente.md"
	// The go test -timeout of a budgeted suite is its budget times 3/2, so
	// a hung test dies soon after the budget.
	timeoutNum, timeoutDen = 3, 2
)

// VitestMsg is printed when web/ exists: the runner does not drive Vitest yet.
const VitestMsg = "testrun: web/ found, but Vitest support is deferred to M6 (it still lacks the quarantine exclude, " +
	"shuffle and count, the slow-test flag and retry: 0; docs/qualidade/testes.md#executor-testrun). " +
	"Run the panel tests by hand until then."

// Runner runs the suites.
type Runner struct {
	Exec      Exec
	Clock     clock.Clock
	FS        fs.FS // repository root
	Out       io.Writer
	WriteFile func(path string, data []byte) error // relative to the root
	Count     int                                  // -count for go test (default 1)
	Shuffle   bool
	Race      bool      // go test -race, with the budget times RaceFactor
	Stdin     io.Reader // the changed files of `prepush`, one per line

	// Getenv reads the CI variables (GITHUB_STEP_SUMMARY); nil reads none.
	Getenv func(string) string
	// AppendFile appends to an absolute file path (the CI job summary).
	AppendFile func(path string, data []byte) error
}

func (r *Runner) printf(format string, args ...any) { fmt.Fprintf(r.Out, format, args...) }

func (r *Runner) hasWeb() bool {
	_, err := fs.Stat(r.FS, "web/package.json")
	return err == nil
}

func (r *Runner) count() int {
	if r.Count < 1 {
		return 1
	}
	return r.Count
}

// runs is how many suite runs the budget covers: -count times the race
// factor.
func (r *Runner) runs() int {
	if r.Race {
		return r.count() * RaceFactor
	}
	return r.count()
}

// goEnv is the environment added to go test. The race detector needs cgo,
// which a developer may have turned off for the release builds.
func (r *Runner) goEnv(suite string) []string {
	env := []string{envSuite + "=" + suite}
	if r.Race {
		env = append(env, "CGO_ENABLED=1")
	}
	return env
}

// goTimeout is the -timeout for runs of the suite: none for the heavy one, a
// little above the budget of that many runs for the others.
func goTimeout(s Suite, runs int) string {
	switch {
	case s.Timeout != "":
		return s.Timeout
	case s.Budget > 0:
		return (s.Budget * time.Duration(runs) * timeoutNum / timeoutDen).String()
	}
	return ""
}

// RunSuite runs fast, full or heavy and returns the exit code.
func (r *Runner) RunSuite(ctx context.Context, s Suite) int {
	if r.hasWeb() {
		r.printf("%s\n", VitestMsg)
		return 2
	}
	quarantine, err := loadQuarantine(r.FS)
	if err != nil {
		r.printf("testrun: %v\n", err)
		return 2
	}
	count := r.count()
	args := []string{"test", "-json", fmt.Sprintf("-count=%d", count)}
	if r.Shuffle {
		args = append(args, "-shuffle=on")
	}
	if s.Tags != "" {
		args = append(args, "-tags", s.Tags)
	}
	if r.Race {
		args = append(args, "-race")
	}
	if to := goTimeout(s, r.runs()); to != "" {
		args = append(args, "-timeout", to)
	}
	if p := skipPattern(quarantine); p != "" {
		args = append(args, "-skip", p)
	}
	args = append(args, "./...")
	goCmd := Cmd{Name: "go", Args: args, Env: r.goEnv(s.Name)}

	start := r.Clock.Now()
	lintSuite := s
	if count > 1 || r.Race {
		lintSuite.Lint = false // the linter result does not change from run to run, or with -race
	}
	lintDone := r.startLint(ctx, lintSuite)
	var b bytes.Buffer
	goCode, goErr := r.Exec.Run(ctx, goCmd, &b)
	lintFailed := r.reportLint(<-lintDone)

	rep := parseEvents(b.Bytes())
	code := 0
	if r.printFailures(rep, goCode, goErr) {
		code = 1
	}
	if lintFailed {
		code = 1
	}
	r.reportMixed(rep, count)
	budget := s.Budget * time.Duration(r.runs())
	// The slow-test timing runs the tests again, alone. That time is part of
	// the wall time the budget checks, but only what the budget has left
	// (less a margin) may be spent on it: the measuring never makes a suite
	// fail. With -count > 1 the elapsed times mix runs, so nothing is flagged.
	var slowWall time.Duration
	if s.FlagSlow && count == 1 && !r.Race { // with -race the times are inflated
		t0 := r.Clock.Now()
		r.flagSlow(ctx, s, rep, rerunAllowance(budget, clock.Since(r.Clock, start)))
		slowWall = clock.Since(r.Clock, t0)
	}
	wall := clock.Since(r.Clock, start)
	tests := len(rep.leaves())
	if budget > 0 && wall > budget {
		r.printf("FAIL %s suite took %v, budget %v. The %d slowest tests:\n", s.Name, wall.Round(time.Millisecond), budget, slowestShown)
		for _, t := range rep.slowest(slowestShown) {
			r.printf("  %8v  %s  %s\n", t.Elapsed.Round(time.Millisecond), t.Package, t.Test)
		}
		code = 1
	}
	if s.Name == "heavy" {
		r.recordHeavy(start, wall)
	}
	if r.reportQuarantine(s, quarantine) {
		code = 1
	}
	status := "PASS"
	if code != 0 {
		status = "FAIL"
	}
	limit := "no limit, measured"
	if budget > 0 {
		limit = "budget " + budget.String()
		if r.Race {
			limit += fmt.Sprintf(", race x%d", RaceFactor)
		}
	}
	timing := ""
	if slowWall.Round(time.Millisecond) > 0 {
		timing = fmt.Sprintf(", %v of it timing slow tests alone", slowWall.Round(time.Millisecond))
	}
	r.printf("%s %s: %d tests in %v (%s%s)\n", status, s.Name, tests, wall.Round(time.Millisecond), limit, timing)
	return code
}

// rerunAllowance is the time the solo re-runs may take: what is left of the
// budget minus a tenth of it, so the process teardown cannot push the suite
// over. A suite without budget has no limit (-1).
func rerunAllowance(budget, used time.Duration) time.Duration {
	if budget <= 0 {
		return -1
	}
	return max(budget-used-budget/10, 0)
}

// reportMixed names every test that passed in some runs and failed in others
// (a flaky one), which only happens with -count > 1. Each line starts with
// "mixed: " so the nightly job can open one issue per test.
func (r *Runner) reportMixed(rep *report, count int) {
	mixed := rep.mixedTests()
	if len(mixed) == 0 {
		return
	}
	r.printf("flaky: %d test(s) passed and failed in the same %d runs:\n", len(mixed), count)
	var md strings.Builder
	md.WriteString("### Flaky tests (mixed results)\n\n| Package | Test | Passed | Failed |\n|---|---|---|---|\n")
	names := make([]string, len(mixed))
	for i, t := range mixed {
		r.printf("mixed: %s %s passed %d failed %d\n", t.Package, t.Test, t.Pass, t.Fail)
		fmt.Fprintf(&md, "| %s | %s | %d | %d |\n", t.Package, t.Test, t.Pass, t.Fail)
		names[i] = t.Test
	}
	r.ciReport("warning", "Flaky tests", fmt.Sprintf("%d test(s) with mixed results: %s", len(mixed), strings.Join(names, ", ")), md.String())
}

func errOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// lintResult is the outcome of one golangci-lint run.
type lintResult struct {
	ran  bool
	out  []byte
	code int
	err  error
}

// startLint runs golangci-lint in the background when the suite asks for it,
// with the suite build tags so tagged files are linted too.
func (r *Runner) startLint(ctx context.Context, s Suite) <-chan lintResult {
	done := make(chan lintResult, 1)
	if !s.Lint {
		done <- lintResult{}
		return done
	}
	args := []string{"run"}
	if s.Tags != "" {
		args = append(args, "--build-tags", s.Tags)
	}
	args = append(args, "./...")
	go func() {
		var b bytes.Buffer
		code, err := r.Exec.Run(ctx, Cmd{Name: "golangci-lint", Args: args}, &b)
		done <- lintResult{ran: true, out: b.Bytes(), code: code, err: err}
	}()
	return done
}

// reportLint prints a lint failure and says whether there was one. A missing
// binary points to the setup document.
func (r *Runner) reportLint(l lintResult) bool {
	switch {
	case !l.ran:
		return false
	case l.err != nil:
		r.printf("FAIL golangci-lint did not run (%v): install the pinned version as in %s\n", l.err, LintDoc)
		return true
	case l.code != 0:
		r.printf("FAIL golangci-lint (exit %d):\n%s", l.code, l.out)
		return true
	}
	return false
}

// printFailures prints every failed test once, with its output; it never
// runs a failed test again (docs/qualidade/testes.md#regras-e-erros).
func (r *Runner) printFailures(rep *report, code int, err error) bool {
	failed := rep.failed()
	for _, t := range failed {
		name := t.Test
		if name == "" {
			name = "(package)"
		}
		r.printf("--- FAIL: %s %s (%v)\n", t.Package, name, t.Elapsed.Round(time.Millisecond))
		for _, l := range t.Output {
			r.printf("    %s\n", l)
		}
	}
	if len(rep.build) > 0 && (code != 0 || err != nil) {
		r.printf("build output:\n  %s\n", strings.Join(rep.build, "\n  "))
	}
	if err != nil {
		r.printf("testrun: go test did not run: %v\n", err)
		return true
	}
	return len(failed) > 0 || code != 0
}

// reportQuarantine lists the skipped tests; an expired entry fails the full
// suite (and only warns in the others).
func (r *Runner) reportQuarantine(s Suite, q []QuarantineEntry) bool {
	if len(q) == 0 {
		return false
	}
	today := r.Clock.Now()
	r.printf("skipped (quarantine, %s):\n", QuarantinePath)
	fail := false
	for _, e := range q {
		note := ""
		if e.expired(today) {
			note = fmt.Sprintf(" EXPIRED: more than %d days since %s", QuarantineDays, e.Added)
			if s.Name == "full" {
				fail = true
			}
		}
		r.printf("  %s (%s, since %s: %s)%s\n", e.Test, e.Issue, e.Added, e.Reason, note)
	}
	if fail {
		r.printf("FAIL quarantine has expired entries: fix or delete those tests\n")
	}
	return fail
}

func (r *Runner) recordHeavy(start time.Time, wall time.Duration) {
	h, err := loadHistory(r.FS)
	if err != nil {
		r.printf("warning: %v\n", err)
	}
	if w := heavyWarning(h, wall); w != "" {
		r.printf("warning: %s\n", w)
	}
	h = append(h, heavyRun{At: start.UTC(), Seconds: wall.Seconds()})
	b, _ := json.MarshalIndent(h, "", "  ")
	if r.WriteFile != nil {
		if err := r.WriteFile(HistoryPath, b); err != nil {
			r.printf("warning: save %s: %v\n", HistoryPath, err)
		}
	}
}

// Affected runs only what the changes may have broken
// (docs/qualidade/testes.md#testes-afetados). It honours the quarantine.
func (r *Runner) Affected(ctx context.Context) int {
	if r.hasWeb() {
		r.printf("%s\n", VitestMsg)
		return 2
	}
	quarantine, err := loadQuarantine(r.FS)
	if err != nil {
		r.printf("testrun: %v\n", err)
		return 2
	}
	args := []string{"test", "-json", "-timeout", goTimeout(Suites["fast"], 1)}
	if p := skipPattern(quarantine); p != "" {
		args = append(args, "-skip", p)
	}
	args = append(args, "./...")
	goCmd := Cmd{Name: "go", Args: args, Env: []string{envSuite + "=fast"}}
	start := r.Clock.Now()
	var b bytes.Buffer
	goCode, goErr := r.Exec.Run(ctx, goCmd, &b)
	rep := parseEvents(b.Bytes())
	code := 0
	if r.printFailures(rep, goCode, goErr) {
		code = 1
	}
	r.printf("affected: %d tests in %v (cached packages are not run again)\n", len(rep.leaves()), clock.Since(r.Clock, start).Round(time.Millisecond))
	if len(quarantine) > 0 {
		r.printf("skipped (quarantine, %s): %s\n", QuarantinePath, strings.Join(quarantineNames(quarantine), ", "))
	}
	return code
}

// Quarantine runs each quarantined test 20 times, with the build tags of its
// suite, and says which may leave.
func (r *Runner) Quarantine(ctx context.Context) int {
	q, err := loadQuarantine(r.FS)
	if err != nil {
		r.printf("testrun: %v\n", err)
		return 2
	}
	if len(q) == 0 {
		r.printf("quarantine is empty\n")
		return 0
	}
	var md strings.Builder
	md.WriteString("### Quarantine (20 runs each)\n\n| Test | Result |\n|---|---|\n")
	leaving := 0
	for _, e := range q {
		s := Suites[e.suiteName()]
		args := []string{"test", "-json", fmt.Sprintf("-count=%d", quarantineN)}
		if s.Tags != "" {
			args = append(args, "-tags", s.Tags)
		}
		// The timeout covers all 20 runs of a test of that suite.
		if to := goTimeout(s, quarantineN); to != "" {
			args = append(args, "-timeout", to)
		}
		args = append(args, "-run", "^"+e.Test+"$", "./...")
		var b bytes.Buffer
		if _, err := r.Exec.Run(ctx, Cmd{Name: "go", Args: args, Env: []string{envSuite + "=" + s.Name}}, &b); err != nil {
			r.printf("%s: %v\n", e.Test, err)
			continue
		}
		pass, fail := countRuns(b.Bytes(), e.Test)
		if fail == 0 && pass >= quarantineN {
			r.printf("%s passed %d/%d: it can leave the quarantine\n", e.Test, pass, quarantineN)
			fmt.Fprintf(&md, "| %s | passed %d/%d: can leave the quarantine (%s) |\n", e.Test, pass, quarantineN, e.Issue)
			leaving++
		} else {
			r.printf("%s passed %d, failed %d: keep it in quarantine\n", e.Test, pass, fail)
			fmt.Fprintf(&md, "| %s | passed %d, failed %d: keep |\n", e.Test, pass, fail)
		}
	}
	if leaving > 0 {
		r.ciReport("notice", "Quarantine", fmt.Sprintf("%d quarantined test(s) passed every run and can leave", leaving), md.String())
	} else {
		r.ciReport("", "", "", md.String())
	}
	return 0
}

func countRuns(out []byte, test string) (pass, fail int) {
	for _, line := range bytes.Split(out, []byte("\n")) {
		var ev event
		if json.Unmarshal(line, &ev) != nil || ev.Test != test {
			continue
		}
		switch ev.Action {
		case "pass":
			pass++
		case "fail":
			fail++
		}
	}
	return pass, fail
}
