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
)

// summaryEnv is the file GitHub Actions gives a step for its job summary.
const summaryEnv = "GITHUB_STEP_SUMMARY"

type slowTestEntry struct {
	alone time.Duration
	r     *result
}

// flagSlow warns about every passing fast-suite test above 100 ms, after
// timing the candidates alone to discount CPU contention: one `go test` per
// package for all its candidates, not one per test. It only measures; a
// failure here does not change the suite result. In CI the list also goes to
// the job summary and to a ::warning annotation.
func (r *Runner) flagSlow(ctx context.Context, s Suite, rep *report) {
	byPkg := map[string][]*result{}
	for _, t := range rep.leaves() {
		if t.Action == "pass" && t.Elapsed > slowTest {
			byPkg[t.Package] = append(byPkg[t.Package], t)
		}
	}
	pkgs := make([]string, 0, len(byPkg))
	for p := range byPkg {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	var flagged []slowTestEntry
	for _, p := range pkgs {
		alone := r.timeAlone(ctx, s, p, byPkg[p])
		for _, t := range byPkg[p] {
			if d := alone[t.id()]; d > slowTest {
				flagged = append(flagged, slowTestEntry{d, t})
			}
		}
	}
	if len(flagged) == 0 {
		return
	}
	lines := make([]string, len(flagged))
	var md strings.Builder
	fmt.Fprintf(&md, "### Slow tests\n\n%d fast-suite test(s) above %v when run alone:\n\n| Time | Package | Test |\n|---|---|---|\n", len(flagged), slowTest)
	names := make([]string, len(flagged))
	for i, f := range flagged {
		lines[i] = fmt.Sprintf("  %8v  %s  %s", f.alone.Round(time.Millisecond), f.r.Package, f.r.Test)
		fmt.Fprintf(&md, "| %v | %s | %s |\n", f.alone.Round(time.Millisecond), f.r.Package, f.r.Test)
		names[i] = f.r.Test
	}
	r.printf("warning: %d fast-suite test(s) above %v when run alone:\n%s\n", len(flagged), slowTest, strings.Join(lines, "\n"))
	r.ciReport("warning", "Slow tests", fmt.Sprintf("%d fast-suite test(s) above %v when run alone: %s", len(flagged), slowTest, strings.Join(names, ", ")), md.String())
}

// timeAlone runs the candidates of one package once more, in a single go
// test, and returns their times by result id. A candidate whose run fails or
// does not show up keeps its time from the suite run.
func (r *Runner) timeAlone(ctx context.Context, s Suite, pkg string, ts []*result) map[string]time.Duration {
	args := []string{"test", "-json", "-count=1", "-run", runPattern(ts)}
	if s.Tags != "" {
		args = append(args, "-tags", s.Tags)
	}
	args = append(args, pkg)
	times := make(map[string]time.Duration, len(ts))
	for _, t := range ts {
		times[t.id()] = t.Elapsed
	}
	var b bytes.Buffer
	if _, err := r.Exec.Run(ctx, Cmd{Name: "go", Args: args, Env: []string{envSuite + "=" + s.Name}}, &b); err != nil {
		return times
	}
	again := parseEvents(b.Bytes())
	for _, t := range ts {
		if a, ok := again.tests[t.id()]; ok && a.Action == "pass" {
			times[t.id()] = a.Elapsed
		}
	}
	return times
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
