package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// event is one line of `go test -json` (cmd/test2json).
type event struct {
	Time       time.Time
	Action     string
	Package    string
	ImportPath string // build-output and build-fail events
	Test       string
	Elapsed    float64 // seconds
	Output     string
}

// result is the outcome of one test. With -count > 1 the test finishes once
// per run: Pass, Fail and Skip count the runs, Action is "fail" when any run
// failed (a later pass does not hide it) and Elapsed is the slowest run.
type result struct {
	Package string
	Test    string // "" for the package itself
	Action  string // pass, fail or skip
	Elapsed time.Duration
	Output  []string

	Pass, Fail, Skip int
}

// mixed reports a test that passed in some runs and failed in others.
func (r result) mixed() bool { return r.Pass > 0 && r.Fail > 0 }

func (r result) id() string { return r.Package + "." + r.Test }

// report is what a `go test -json` run produced.
type report struct {
	tests    map[string]*result // by Package + "." + Test
	packages map[string]*result
	build    []string // build errors
	first    time.Time
	last     time.Time
}

func parseEvents(out []byte) *report {
	r := &report{tests: map[string]*result{}, packages: map[string]*result{}}
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 64*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		var ev event
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &ev) != nil {
			if t := strings.TrimSpace(string(line)); t != "" {
				r.build = append(r.build, t) // compiler output outside JSON
			}
			continue
		}
		if !ev.Time.IsZero() {
			if r.first.IsZero() || ev.Time.Before(r.first) {
				r.first = ev.Time
			}
			if ev.Time.After(r.last) {
				r.last = ev.Time
			}
		}
		switch ev.Action {
		case "build-output":
			r.build = append(r.build, strings.TrimRight(ev.Output, "\n"))
			continue
		case "build-fail":
			continue
		}
		res := r.get(ev.Package, ev.Test)
		switch ev.Action {
		case "output":
			res.Output = append(res.Output, strings.TrimRight(ev.Output, "\n"))
		case "pass", "fail", "skip":
			switch ev.Action {
			case "pass":
				res.Pass++
			case "fail":
				res.Fail++
			default:
				res.Skip++
			}
			if res.Action != "fail" {
				res.Action = ev.Action
			}
			res.Elapsed = max(res.Elapsed, time.Duration(ev.Elapsed*float64(time.Second)))
		}
	}
	return r
}

func (r *report) get(pkg, test string) *result {
	m := r.tests
	if test == "" {
		m = r.packages
	}
	key := pkg + "." + test
	res, ok := m[key]
	if !ok {
		res = &result{Package: pkg, Test: test}
		m[key] = res
	}
	return res
}

// leaves returns the tests without subtests.
func (r *report) leaves() []*result {
	parent := map[string]bool{}
	for _, t := range r.tests {
		if i := strings.LastIndex(t.Test, "/"); i > 0 {
			for p := t.Test[:i]; ; {
				parent[t.Package+"."+p] = true
				j := strings.LastIndex(p, "/")
				if j < 0 {
					break
				}
				p = p[:j]
			}
		}
	}
	var out []*result
	for k, t := range r.tests {
		if !parent[k] {
			out = append(out, t)
		}
	}
	sortResults(out)
	return out
}

func (r *report) failed() []*result {
	var out []*result
	for _, t := range r.tests {
		// A test that never finished in a package that failed is where the
		// binary was when it crashed (a panic of a goroutine that outlived its
		// test, a timeout): `go test -json` attributes the panic text to the
		// test that was running, which has no result of its own.
		if t.Action == "fail" || (t.Action == "" && r.packageFailed(t.Package)) {
			out = append(out, t)
		}
	}
	for _, p := range r.packages {
		if p.Action == "fail" && !r.hasFailedTest(p.Package) {
			out = append(out, p) // build or setup failure, panic in TestMain
		}
	}
	sortResults(out)
	return out
}

// mixedTests returns the tests (without subtests) that passed in some runs
// and failed in others, which is what -count > 1 is for.
func (r *report) mixedTests() []*result {
	var out []*result
	for _, t := range r.leaves() {
		if t.mixed() {
			out = append(out, t)
		}
	}
	return out
}

func (r *report) packageFailed(pkg string) bool {
	p := r.packages[pkg+"."]
	return p != nil && p.Action == "fail"
}

func (r *report) hasFailedTest(pkg string) bool {
	for _, t := range r.tests {
		if t.Package == pkg && (t.Action == "fail" || t.Action == "") {
			return true
		}
	}
	return false
}

// slowest returns the n slowest tests (leaves), slowest first.
func (r *report) slowest(n int) []*result {
	ls := r.leaves()
	sort.SliceStable(ls, func(i, j int) bool { return ls[i].Elapsed > ls[j].Elapsed })
	if len(ls) > n {
		ls = ls[:n]
	}
	return ls
}

func sortResults(rs []*result) {
	sort.Slice(rs, func(i, j int) bool { return rs[i].id() < rs[j].id() })
}
