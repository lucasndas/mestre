package main

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// summaryEnv is the file GitHub Actions gives a step for its job summary.
const summaryEnv = "GITHUB_STEP_SUMMARY"

type slowTestEntry struct {
	time     time.Duration
	r        *result
	measured bool // timed alone; otherwise the time of the suite run
}

// flagSlow warns about every passing fast-suite test above 100 ms when run
// alone. The candidates are the tests above 200 ms in the suite run, timed
// alone to discount CPU contention: one `go test` per package for all its
// candidates, not one per test, the packages with the most slow time first. allowance bounds the time spent (-1: no bound); a
// package that does not fit keeps the time of the suite run and is marked
// "(not re-run)". It only measures; a failure here does not change the suite
// result. In CI the list also goes to the job summary and to a ::warning.
func (r *Runner) flagSlow(ctx context.Context, s Suite, rep *report, allowance time.Duration) {
	byPkg := map[string][]*result{}
	total := map[string]time.Duration{}
	for _, t := range rep.leaves() {
		if t.Action == "pass" && t.Elapsed > rerunTest {
			byPkg[t.Package] = append(byPkg[t.Package], t)
			total[t.Package] += t.Elapsed
		}
	}
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Slice(pkgs, func(i, j int) bool {
		if total[pkgs[i]] != total[pkgs[j]] {
			return total[pkgs[i]] > total[pkgs[j]]
		}
		return pkgs[i] < pkgs[j]
	})
	var flagged []slowTestEntry
	for _, p := range pkgs {
		alone, ok := r.timeAlone(ctx, s, p, byPkg[p], allowance)
		if allowance >= 0 {
			allowance = max(allowance-alone.spent, 0)
		}
		for _, t := range byPkg[p] {
			if d := alone.times[t.id()]; d > slowTest {
				flagged = append(flagged, slowTestEntry{d, t, ok})
			}
		}
	}
	if len(flagged) == 0 {
		return
	}
	sort.Slice(flagged, func(i, j int) bool { return flagged[i].r.id() < flagged[j].r.id() })
	lines := make([]string, len(flagged))
	var md strings.Builder
	fmt.Fprintf(&md, "### Slow tests\n\n%d fast-suite test(s) above %v when run alone:\n\n| Time | Package | Test |\n|---|---|---|\n", len(flagged), slowTest)
	names := make([]string, len(flagged))
	for i, f := range flagged {
		note := ""
		if !f.measured {
			note = " (not re-run: no budget left)"
		}
		lines[i] = fmt.Sprintf("  %8v  %s  %s%s", f.time.Round(time.Millisecond), f.r.Package, f.r.Test, note)
		fmt.Fprintf(&md, "| %v | %s | %s%s |\n", f.time.Round(time.Millisecond), f.r.Package, f.r.Test, note)
		names[i] = f.r.Test
	}
	// The time is the one of the suite run when no test was timed alone.
	how := "when run alone"
	if !slices.ContainsFunc(flagged, func(f slowTestEntry) bool { return f.measured }) {
		how = "in the suite (not re-run)"
	}
	r.printf("warning: %d fast-suite test(s) above %v %s:\n%s\n", len(flagged), slowTest, how, strings.Join(lines, "\n"))
	r.ciReport("warning", "Slow tests", fmt.Sprintf("%d fast-suite test(s) above %v %s: %s", len(flagged), slowTest, how, strings.Join(names, ", ")), md.String())
}

// aloneTimes is the outcome of timing the candidates of one package.
type aloneTimes struct {
	times map[string]time.Duration // by result id
	spent time.Duration
}

// timeAlone runs the candidates of one package once more, in a single go
// test, and returns their times and whether the run completed. A candidate
// whose run fails, does not show up or does not fit in the allowance (-1:
// unbounded) keeps its time from the suite run.
func (r *Runner) timeAlone(ctx context.Context, s Suite, pkg string, ts []*result, allowance time.Duration) (aloneTimes, bool) {
	out := aloneTimes{times: make(map[string]time.Duration, len(ts))}
	for _, t := range ts {
		out.times[t.id()] = t.Elapsed
	}
	if allowance == 0 {
		return out, false
	}
	args := []string{"test", "-json", "-count=1", "-run", runPattern(ts)}
	if s.Tags != "" {
		args = append(args, "-tags", s.Tags)
	}
	args = append(args, pkg)
	rctx, cancel := ctx, context.CancelFunc(func() {})
	if allowance > 0 {
		rctx, cancel = clock.WithTimeout(ctx, r.Clock, allowance)
	}
	defer cancel()
	start := r.Clock.Now()
	var b bytes.Buffer
	_, err := r.Exec.Run(rctx, Cmd{Name: "go", Args: args, Env: []string{envSuite + "=" + s.Name}}, &b)
	out.spent = clock.Since(r.Clock, start)
	if err != nil || rctx.Err() != nil {
		return out, false
	}
	again := parseEvents(b.Bytes())
	for _, t := range ts {
		if a, ok := again.tests[t.id()]; ok && a.Action == "pass" {
			out.times[t.id()] = a.Elapsed
		}
	}
	return out, true
}

// runPattern is the -run value that selects the tests: one anchored
// alternation per level of the subtest path (`^(A|B)$/^(x|y)$`), a plain
// `^A$/^x$` for a single test. With several tests of different parents it
// may also select a subtest or two more, which are only timed and ignored.
func runPattern(ts []*result) string {
	var levels [][]string
	for _, t := range ts {
		for i, part := range strings.Split(t.Test, "/") {
			if i == len(levels) {
				levels = append(levels, nil)
			}
			q := regexp.QuoteMeta(part)
			if !slices.Contains(levels[i], q) {
				levels[i] = append(levels[i], q)
			}
		}
	}
	parts := make([]string, len(levels))
	for i, alts := range levels {
		if len(alts) == 1 {
			parts[i] = "^" + alts[0] + "$"
		} else {
			parts[i] = "^(" + strings.Join(alts, "|") + ")$"
		}
	}
	return strings.Join(parts, "/")
}

// ciReport tells GitHub Actions about a finding: an annotation of the given
// kind (warning, notice; "" for none) and md appended to the job summary. It
// does nothing outside a job that sets GITHUB_STEP_SUMMARY.
func (r *Runner) ciReport(kind, title, msg, md string) {
	if r.Getenv == nil {
		return
	}
	path := r.Getenv(summaryEnv)
	if path == "" {
		return
	}
	if kind != "" {
		r.printf("::%s title=%s::%s\n", kind, escapeProperty(title), escapeData(msg))
	}
	if r.AppendFile != nil && md != "" {
		if err := r.AppendFile(path, []byte(md+"\n")); err != nil {
			r.printf("warning: write the job summary: %v\n", err)
		}
	}
}

// escapeData and escapeProperty follow the workflow command syntax.
func escapeData(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A").Replace(s)
}

func escapeProperty(s string) string {
	return strings.NewReplacer("%", "%25", "\r", "%0D", "\n", "%0A", ":", "%3A", ",", "%2C").Replace(s)
}
