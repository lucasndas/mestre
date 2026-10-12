package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"regexp"
	"strings"
	"sync"
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
}

// Suites by executor name.
var Suites = map[string]Suite{
	"fast":  {Name: "fast", Budget: 30 * time.Second, FlagSlow: true},
	"full":  {Name: "full", Tags: "full", Budget: 3 * time.Minute},
	"heavy": {Name: "heavy", Tags: "full,heavy"},
}

// Limits of the runner.
const (
	slowTest     = 100 * time.Millisecond
	slowestShown = 10
	quarantineN  = 20
	envSuite     = "MESTRE_TEST_SUITE"
)

// Runner runs the suites.
type Runner struct {
	Exec      Exec
	Clock     clock.Clock
	FS        fs.FS // repository root
	Out       io.Writer
	WriteFile func(path string, data []byte) error // relative to the root
	Count     int                                  // -count for go test (default 1)
	Shuffle   bool
}

func (r *Runner) printf(format string, args ...any) { fmt.Fprintf(r.Out, format, args...) }

func (r *Runner) hasWeb() bool {
	_, err := fs.Stat(r.FS, "web/package.json")
	return err == nil
}

// RunSuite runs fast, full or heavy and returns the exit code.
func (r *Runner) RunSuite(ctx context.Context, s Suite) int {
	quarantine, err := loadQuarantine(r.FS)
	if err != nil {
		r.printf("testrun: %v\n", err)
		return 2
	}
	count := r.Count
	if count == 0 {
		count = 1
	}
	args := []string{"test", "-json", fmt.Sprintf("-count=%d", count)}
	if r.Shuffle {
		args = append(args, "-shuffle=on")
	}
	if s.Tags != "" {
		args = append(args, "-tags", s.Tags)
	}
	if p := skipPattern(quarantine); p != "" {
		args = append(args, "-skip", p)
	}
	args = append(args, "./...")
	goCmd := Cmd{Name: "go", Args: args, Env: []string{envSuite + "=" + s.Name}}

	start := r.Clock.Now()
	goOut, goCode, goErr, webOut, webCode, webErr := r.runParallel(ctx, goCmd, r.vitestRun())
	wall := clock.Since(r.Clock, start)

	rep := parseEvents(goOut)
	addVitest(rep, webOut)
	code := 0
	if r.printFailures(rep, goCode, goErr) {
		code = 1
	}
	if webErr != nil || webCode != 0 {
		r.printf("FAIL vitest (exit %d) %v\n", webCode, errOrEmpty(webErr))
		code = 1
	}
	tests := len(rep.leaves())
	if s.Budget > 0 && wall > s.Budget {
		r.printf("FAIL %s suite took %v, budget %v. The %d slowest tests:\n", s.Name, wall.Round(time.Millisecond), s.Budget, slowestShown)
		for _, t := range rep.slowest(slowestShown) {
			r.printf("  %8v  %s  %s\n", t.Elapsed.Round(time.Millisecond), t.Package, t.Test)
		}
		code = 1
	}
	if s.FlagSlow {
		r.flagSlow(ctx, s, rep)
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
	budget := "no limit, measured"
	if s.Budget > 0 {
		budget = "budget " + s.Budget.String()
	}
	r.printf("%s %s: %d tests in %v (%s)\n", status, s.Name, tests, wall.Round(time.Millisecond), budget)
	return code
}

func errOrEmpty(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// vitestRun is the panel command of the suites, or nil without web/.
func (r *Runner) vitestRun() *Cmd {
	if !r.hasWeb() {
		return nil
	}
	return &Cmd{Name: "npx", Args: []string{"vitest", "run", "--reporter=json"}, Dir: "web"}
}

func (r *Runner) runParallel(ctx context.Context, goCmd Cmd, web *Cmd) (goOut []byte, goCode int, goErr error, webOut []byte, webCode int, webErr error) {
	var wg sync.WaitGroup
	if web != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var b bytes.Buffer
			webCode, webErr = r.Exec.Run(ctx, *web, &b)
			webOut = b.Bytes()
		}()
	}
	var b bytes.Buffer
	goCode, goErr = r.Exec.Run(ctx, goCmd, &b)
	goOut = b.Bytes()
	wg.Wait()
	return
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

// flagSlow warns about every passing fast-suite test above 100 ms, after
// timing it alone once to discount CPU contention. It only measures; a
// failure here does not change the suite result.
func (r *Runner) flagSlow(ctx context.Context, s Suite, rep *report) {
	var flagged []string
	for _, t := range rep.leaves() {
		if t.Action != "pass" || t.Elapsed <= slowTest || t.Package == "vitest" {
			continue
		}
		alone := r.timeAlone(ctx, s, t)
		if alone > slowTest {
			flagged = append(flagged, fmt.Sprintf("  %8v  %s  %s", alone.Round(time.Millisecond), t.Package, t.Test))
		}
	}
	if len(flagged) > 0 {
		r.printf("warning: %d fast-suite test(s) above %v when run alone:\n%s\n", len(flagged), slowTest, strings.Join(flagged, "\n"))
	}
}

func (r *Runner) timeAlone(ctx context.Context, s Suite, t *result) time.Duration {
	parts := strings.Split(t.Test, "/")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	args := []string{"test", "-json", "-count=1", "-run", strings.Join(parts, "/")}
	if s.Tags != "" {
		args = append(args, "-tags", s.Tags)
	}
	args = append(args, t.Package)
	var b bytes.Buffer
	if _, err := r.Exec.Run(ctx, Cmd{Name: "go", Args: args, Env: []string{envSuite + "=" + s.Name}}, &b); err != nil {
		return t.Elapsed
	}
	if again, ok := parseEvents(b.Bytes()).tests[t.id()]; ok && again.Action == "pass" {
		return again.Elapsed
	}
	return t.Elapsed
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

// addVitest merges the Vitest JSON report into rep, under package "vitest".
func addVitest(rep *report, out []byte) {
	if len(out) == 0 {
		return
	}
	var v struct {
		TestResults []struct {
			Name             string `json:"name"`
			AssertionResults []struct {
				FullName string  `json:"fullName"`
				Status   string  `json:"status"`
				Duration float64 `json:"duration"` // ms
			} `json:"assertionResults"`
		} `json:"testResults"`
	}
	if json.Unmarshal(out, &v) != nil {
		return
	}
	for _, f := range v.TestResults {
		for _, a := range f.AssertionResults {
			res := rep.get("vitest", f.Name+" > "+a.FullName)
			res.Elapsed = time.Duration(a.Duration * float64(time.Millisecond))
			switch a.Status {
			case "passed":
				res.Action = "pass"
			case "failed":
				res.Action = "fail"
			default:
				res.Action = "skip"
			}
		}
	}
}

// Affected runs only what the changes may have broken
// (docs/qualidade/testes.md#testes-afetados).
func (r *Runner) Affected(ctx context.Context) int {
	goCmd := Cmd{Name: "go", Args: []string{"test", "-json", "./..."}, Env: []string{envSuite + "=fast"}}
	var web *Cmd
	if r.hasWeb() {
		files, err := r.changedFiles(ctx)
		if err != nil {
			r.printf("testrun: %v\n", err)
			return 2
		}
		var related []string
		for _, f := range files {
			if rel, ok := strings.CutPrefix(f, "web/"); ok {
				related = append(related, rel)
			}
		}
		if len(related) > 0 {
			web = &Cmd{Name: "npx", Args: append([]string{"vitest", "related", "--run", "--reporter=json"}, related...), Dir: "web"}
		}
	}
	start := r.Clock.Now()
	goOut, goCode, goErr, webOut, webCode, webErr := r.runParallel(ctx, goCmd, web)
	rep := parseEvents(goOut)
	addVitest(rep, webOut)
	code := 0
	if r.printFailures(rep, goCode, goErr) {
		code = 1
	}
	if webErr != nil || webCode != 0 {
		r.printf("FAIL vitest related (exit %d) %v\n", webCode, errOrEmpty(webErr))
		code = 1
	}
	r.printf("affected: %d tests in %v (cached packages are not run again)\n", len(rep.leaves()), clock.Since(r.Clock, start).Round(time.Millisecond))
	return code
}

// changedFiles lists modified and new files, relative to the root.
func (r *Runner) changedFiles(ctx context.Context) ([]string, error) {
	var files []string
	for _, c := range []Cmd{
		{Name: "git", Args: []string{"diff", "--name-only", "HEAD"}},
		{Name: "git", Args: []string{"ls-files", "--others", "--exclude-standard"}},
	} {
		var b bytes.Buffer
		code, err := r.Exec.Run(ctx, c, &b)
		if err != nil || code != 0 {
			return nil, fmt.Errorf("%s: exit %d %v", c, code, errOrEmpty(err))
		}
		for _, l := range strings.Split(b.String(), "\n") {
			if l = strings.TrimSpace(l); l != "" {
				files = append(files, l)
			}
		}
	}
	return files, nil
}

// Quarantine runs each quarantined test 20 times and says which may leave.
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
	for _, e := range q {
		var b bytes.Buffer
		c := Cmd{Name: "go", Args: []string{"test", "-json", fmt.Sprintf("-count=%d", quarantineN), "-tags", "full", "-run", "^" + e.Test + "$", "./..."}, Env: []string{envSuite + "=full"}}
		if _, err := r.Exec.Run(ctx, c, &b); err != nil {
			r.printf("%s: %v\n", e.Test, err)
			continue
		}
		pass, fail := countRuns(b.Bytes(), e.Test)
		if fail == 0 && pass >= quarantineN {
			r.printf("%s passed %d/%d: it can leave the quarantine\n", e.Test, pass, quarantineN)
		} else {
			r.printf("%s passed %d, failed %d: keep it in quarantine\n", e.Test, pass, fail)
		}
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
