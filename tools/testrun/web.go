package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// WebBudget is the time of the web side of a suite, apart from the Go
// budgets. Provisional: the owner sets the final value after the first
// measurements on the CI (docs/qualidade/testes.md#executor-testrun).
const WebBudget = 15 * time.Second

// webSuite is the quarantine suite name of the entries that name a Vitest
// test file instead of a Go test.
const webSuite = "web"

// Values of Runner.Only.
const (
	OnlyGo  = "go"
	OnlyWeb = "web"
)

// WebMissingMsg is printed when the packages of web/ are not installed.
const WebMissingMsg = "testrun: web/node_modules is missing: run `npm ci` in web/ (docs/desenvolvimento/ambiente.md)"

func (r *Runner) wantGo() bool  { return r.Only != OnlyWeb }
func (r *Runner) wantWeb() bool { return r.Only != OnlyGo && r.hasWeb() }

// npx is a command of web/ run from the installed packages, never fetched.
func npx(args ...string) Cmd {
	return Cmd{Name: "npx", Args: append([]string{"--no-install"}, args...), Dir: "web"}
}

func (r *Runner) hasModules() bool {
	_, err := fs.Stat(r.FS, "web/node_modules")
	return err == nil
}

// vitestJSON is the part of the JSON reporter of Vitest the runner reads.
type vitestJSON struct {
	NumFailedTests int `json:"numFailedTests"`
	TestResults    []struct {
		Name       string `json:"name"`
		Message    string `json:"message"`
		Status     string `json:"status"`
		Assertions []struct {
			FullName string   `json:"fullName"`
			Status   string   `json:"status"`
			Duration float64  `json:"duration"`
			Failures []string `json:"failureMessages"`
		} `json:"assertionResults"`
	} `json:"testResults"`
}

// tally counts the passes and failures of one test over the runs.
type tally struct{ pass, fail int }

type webTest struct {
	File, Name string
	Status     string
	Elapsed    time.Duration
	Failures   []string
}

// parseVitest reads the JSON of one run; the reporter prints it on standard
// output among other lines, so it is cut from the first "{" to the last "}".
func parseVitest(out []byte) (tests []webTest, suiteErrors []string, ok bool) {
	i, j := bytes.IndexByte(out, '{'), bytes.LastIndexByte(out, '}')
	if i < 0 || j < i {
		return nil, nil, false
	}
	var v vitestJSON
	if json.Unmarshal(out[i:j+1], &v) != nil {
		return nil, nil, false
	}
	for _, f := range v.TestResults {
		if f.Status == "failed" && len(f.Assertions) == 0 {
			suiteErrors = append(suiteErrors, f.Name+": "+f.Message)
		}
		for _, a := range f.Assertions {
			tests = append(tests, webTest{
				File: f.Name, Name: a.FullName, Status: a.Status,
				Elapsed:  time.Duration(a.Duration * float64(time.Millisecond)),
				Failures: a.Failures,
			})
		}
	}
	return tests, suiteErrors, true
}

// webQuarantine is the quarantine entries for the Vitest tests: each names a
// test file, relative to web/.
func webQuarantine(q []QuarantineEntry) []QuarantineEntry {
	var out []QuarantineEntry
	for _, e := range q {
		if e.Suite == webSuite {
			out = append(out, e)
		}
	}
	return out
}

// goQuarantine is the entries for Go tests.
func goQuarantine(q []QuarantineEntry) []QuarantineEntry {
	var out []QuarantineEntry
	for _, e := range q {
		if e.Suite != webSuite {
			out = append(out, e)
		}
	}
	return out
}

func (r *Runner) vitestArgs(shuffle bool, q []QuarantineEntry) []string {
	args := []string{"vitest", "run", "--reporter=json"}
	if shuffle {
		args = append(args, "--sequence.shuffle")
	}
	for _, e := range webQuarantine(q) {
		args = append(args, "--exclude", e.Test)
	}
	return args
}

// runVitest runs Vitest with args and returns its exit code and its JSON
// report. Vitest 5 writes the JSON reporter to a file, not to standard
// output, so the report is read from a file of its own and removed after;
// the standard output is the fallback.
func (r *Runner) runVitest(ctx context.Context, args, files []string) (code int, report []byte, err error) {
	name := fmt.Sprintf(".vitest/testrun-%d.json", r.Clock.Now().UnixNano())
	var b bytes.Buffer
	code, err = r.Exec.Run(ctx, npx(append(append(args, "--outputFile="+name), files...)...), &b)
	if data, rerr := fs.ReadFile(r.FS, "web/"+name); rerr == nil {
		if r.Remove != nil {
			_ = r.Remove("web/" + name)
		}
		return code, data, err
	}
	return code, b.Bytes(), err
}

// RunWeb runs the web side of a suite: Vitest, Biome and tsc side by side,
// inside the budget of the web side (docs/qualidade/testes.md#executor-testrun).
func (r *Runner) RunWeb(ctx context.Context, s Suite, q []QuarantineEntry) int {
	if !r.hasModules() {
		r.printf("%s\n", WebMissingMsg)
		return 2
	}
	count := r.count()
	start := r.Clock.Now()

	var wg sync.WaitGroup
	type tool struct {
		name string
		cmd  Cmd
		out  bytes.Buffer
		code int
		err  error
		took time.Duration
	}
	var checks []*tool
	if count == 1 { // like the linter of Go, the result does not change from run to run
		checks = []*tool{
			{name: "biome", cmd: npx("biome", "check", ".")},
			{name: "tsc", cmd: npx("tsc", "--noEmit")},
		}
	}
	for _, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			t0 := r.Clock.Now()
			c.code, c.err = r.Exec.Run(ctx, c.cmd, &c.out)
			c.took = clock.Since(r.Clock, t0)
		}()
	}

	code := 0
	byTest := map[string]*tally{}
	var last []webTest
	var vitestWall time.Duration
	for run := 0; run < count; run++ {
		t0 := r.Clock.Now()
		c, report, err := r.runVitest(ctx, r.vitestArgs(r.Shuffle, q), nil)
		vitestWall += clock.Since(r.Clock, t0)
		tests, suiteErrs, ok := parseVitest(report)
		switch {
		case err != nil:
			r.printf("testrun: vitest did not run: %v\n", err)
			code = 1
		case !ok:
			r.printf("FAIL vitest printed no report (exit %d):\n%s", c, report)
			code = 1
		default:
			if c != 0 && len(suiteErrs) == 0 && !anyFailed(tests) {
				r.printf("FAIL vitest exit %d with no failed test:\n%s", c, report)
				code = 1
			}
			for _, e := range suiteErrs {
				r.printf("--- FAIL: (file) %s\n", e)
				code = 1
			}
		}
		for _, t := range tests {
			key := t.File + "\x00" + t.Name
			if byTest[key] == nil {
				byTest[key] = &tally{}
			}
			switch t.Status {
			case "passed":
				byTest[key].pass++
			case "failed":
				byTest[key].fail++
				r.printf("--- FAIL: %s %s\n", t.File, t.Name)
				for _, f := range t.Failures {
					r.printf("    %s\n", f)
				}
				code = 1
			}
		}
		last = tests
	}
	wg.Wait()
	for _, c := range checks {
		switch {
		case c.err != nil:
			r.printf("FAIL %s did not run (%v): run `npm ci` in web/ (docs/desenvolvimento/ambiente.md)\n", c.name, c.err)
			code = 1
		case c.code != 0:
			r.printf("FAIL %s (exit %d):\n%s", c.name, c.code, c.out.Bytes())
			code = 1
		}
	}
	wall := clock.Since(r.Clock, start)

	r.reportWebMixed(byTest, count)
	if count == 1 && !r.Race {
		r.flagWebSlow(last)
	}
	budget := WebBudget * time.Duration(count)
	if wall > budget {
		r.printf("FAIL web side of %s took %v, budget %v. The %d slowest tests:\n", s.Name, wall.Round(time.Millisecond), budget, slowestShown)
		sort.SliceStable(last, func(i, j int) bool { return last[i].Elapsed > last[j].Elapsed })
		for i, t := range last {
			if i == slowestShown {
				break
			}
			r.printf("  %8v  %s  %s\n", t.Elapsed.Round(time.Millisecond), t.File, t.Name)
		}
		code = 1
	}
	var parts []string
	parts = append(parts, fmt.Sprintf("vitest %v", vitestWall.Round(time.Millisecond)))
	for _, c := range checks {
		parts = append(parts, fmt.Sprintf("%s %v", c.name, c.took.Round(time.Millisecond)))
	}
	r.printf("info: %s (side by side)\n", strings.Join(parts, ", "))
	status := "PASS"
	if code != 0 {
		status = "FAIL"
	}
	r.printf("%s %s web: %d tests in %v (budget %v)\n", status, s.Name, len(last), wall.Round(time.Millisecond), budget)
	return code
}

func anyFailed(tests []webTest) bool {
	for _, t := range tests {
		if t.Status == "failed" {
			return true
		}
	}
	return false
}

// reportWebMixed names the Vitest tests that passed in some runs and failed
// in others, in the same "mixed:" lines as the Go ones.
func (r *Runner) reportWebMixed(res map[string]*tally, count int) {
	var keys []string
	for k, v := range res {
		if v.pass > 0 && v.fail > 0 {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return
	}
	sort.Strings(keys)
	r.printf("flaky: %d web test(s) passed and failed in the same %d runs:\n", len(keys), count)
	var md strings.Builder
	md.WriteString("### Flaky web tests (mixed results)\n\n| File | Test | Passed | Failed |\n|---|---|---|---|\n")
	names := make([]string, len(keys))
	for i, k := range keys {
		file, name, _ := strings.Cut(k, "\x00")
		v := res[k]
		// One word, so the report job reads the line like a Go one.
		word := strings.ReplaceAll(strings.TrimSpace(name), " ", "_")
		r.printf("mixed: %s %s passed %d failed %d\n", file, word, v.pass, v.fail)
		fmt.Fprintf(&md, "| %s | %s | %d | %d |\n", file, name, v.pass, v.fail)
		names[i] = word
	}
	r.ciReport("warning", "Flaky web tests", fmt.Sprintf("%d web test(s) with mixed results: %s", len(keys), strings.Join(names, ", ")), md.String())
}

// flagWebSlow warns about the Vitest tests above the slow limit, as the Go
// ones: a warning, never a failure. The time is the one in the suite run.
func (r *Runner) flagWebSlow(tests []webTest) {
	var slow []webTest
	for _, t := range tests {
		if t.Elapsed > slowTest {
			slow = append(slow, t)
		}
	}
	if len(slow) == 0 {
		return
	}
	sort.SliceStable(slow, func(i, j int) bool { return slow[i].Elapsed > slow[j].Elapsed })
	var md strings.Builder
	md.WriteString("### Slow web tests (above 100 ms in the suite)\n\n| Time | File | Test |\n|---|---|---|\n")
	r.printf("slow: %d web test(s) above %v in the suite:\n", len(slow), slowTest)
	for _, t := range slow {
		r.printf("  %8v  %s  %s\n", t.Elapsed.Round(time.Millisecond), t.File, t.Name)
		fmt.Fprintf(&md, "| %v | %s | %s |\n", t.Elapsed.Round(time.Millisecond), t.File, t.Name)
	}
	r.ciReport("warning", "Slow web tests", fmt.Sprintf("%d web test(s) above %v", len(slow), slowTest), md.String())
}

// AffectedWeb runs `vitest related` on the files of web/ the working tree
// changed (git diff against HEAD plus the new files).
func (r *Runner) AffectedWeb(ctx context.Context, q []QuarantineEntry) int {
	if !r.hasModules() {
		r.printf("%s\n", WebMissingMsg)
		return 2
	}
	var files []string
	for _, g := range [][]string{
		{"diff", "--name-only", "HEAD", "--", "web"},
		{"ls-files", "--others", "--exclude-standard", "--", "web"},
	} {
		var b bytes.Buffer
		if _, err := r.Exec.Run(ctx, Cmd{Name: "git", Args: g}, &b); err != nil {
			r.printf("testrun: git %s: %v\n", g[0], err)
			return 2
		}
		for _, l := range strings.Split(b.String(), "\n") {
			if l = strings.TrimSpace(l); strings.HasPrefix(l, "web/") {
				files = append(files, strings.TrimPrefix(l, "web/"))
			}
		}
	}
	if len(files) == 0 {
		r.printf("affected web: no changed file under web/\n")
		return 0
	}
	args := []string{"vitest", "related", "--run", "--reporter=json", "--passWithNoTests"}
	for _, e := range webQuarantine(q) {
		args = append(args, "--exclude", e.Test)
	}
	start := r.Clock.Now()
	c, report, err := r.runVitest(ctx, args, files)
	tests, suiteErrs, ok := parseVitest(report)
	code := 0
	switch {
	case err != nil:
		r.printf("testrun: vitest did not run: %v\n", err)
		return 1
	case !ok && c != 0:
		r.printf("FAIL vitest printed no report (exit %d):\n%s", c, report)
		return 1
	}
	for _, e := range suiteErrs {
		r.printf("--- FAIL: (file) %s\n", e)
		code = 1
	}
	for _, t := range tests {
		if t.Status == "failed" {
			r.printf("--- FAIL: %s %s\n", t.File, t.Name)
			for _, f := range t.Failures {
				r.printf("    %s\n", f)
			}
			code = 1
		}
	}
	if c != 0 && code == 0 {
		code = 1
	}
	r.printf("affected web: %d tests in %v\n", len(tests), clock.Since(r.Clock, start).Round(time.Millisecond))
	return code
}
