package main

import (
	"bufio"
	"bytes"
	"context"
	"path"
	"sort"
	"strings"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// listFormat prints, for each package: its import path, its module and the
// packages it imports (also from its tests), comma separated.
const listFormat = `{{.ImportPath}}|{{.Module.Path}}|{{join .Imports ","}},{{join .TestImports ","}},{{join .XTestImports ","}}`

// ignorable says whether a change cannot break a Go test: the documents
// (checked by the fast suite on commit), the CI files, the packaging and the
// hooks.
func ignorable(file string) bool {
	switch {
	case strings.HasSuffix(file, ".md"), file == "LICENSE", file == ".golangci.yml", file == "test/quarantine.yaml":
		return true
	}
	for _, dir := range []string{"docs/", ".github/", "packaging/", "tools/githooks/", "web/"} {
		if strings.HasPrefix(file, dir) {
			return true
		}
	}
	return false
}

type pkgInfo struct {
	importPath string
	dir        string // relative to the module root; "." for the root
	imports    []string
}

// Prepush runs the full suite, without the race detector, on the packages the
// pushed commits changed and the ones that import them
// (docs/qualidade/testes.md#antes-do-push). The changed files come on stdin,
// one per line, from the pre-push hook. It honours the quarantine and has no
// budget of its own: it prints the time.
func (r *Runner) Prepush(ctx context.Context) int {
	files := r.changedFiles()
	var relevant []string
	for _, f := range files {
		if !ignorable(f) {
			relevant = append(relevant, f)
		}
	}
	start := r.Clock.Now()
	if len(relevant) == 0 {
		r.printf("prepush: only documents and CI files changed (%d file(s)), nothing to run\n", len(files))
		return 0
	}
	quarantine, err := loadQuarantine(r.FS)
	if err != nil {
		r.printf("testrun: %v\n", err)
		return 2
	}

	var listed bytes.Buffer
	code, err := r.Exec.Run(ctx, Cmd{Name: "go", Args: []string{"list", "-tags", "full", "-f", listFormat, "./..."}}, &listed)
	if err != nil || code != 0 {
		r.printf("testrun: go list failed (exit %d, %s):\n%s", code, errOrEmpty(err), listed.String())
		return 2
	}
	pkgs, module := parseList(listed.String())
	changed, all, notes := affectedPackages(pkgs, module, relevant)
	for _, n := range notes {
		r.printf("prepush: %s\n", n)
	}
	var targets []string
	if all {
		targets = []string{"./..."}
		r.printf("prepush: go.mod, go.sum or a file outside every package changed: running every package\n")
	} else {
		targets = dependents(pkgs, changed)
		if len(targets) == 0 {
			r.printf("prepush: no package changed, nothing to run\n")
			return 0
		}
		r.printf("prepush: %d changed package(s), %d to run with their dependents\n", len(changed), len(targets))
	}

	full := Suites["full"]
	args := []string{"test", "-json", "-count=1", "-tags", full.Tags}
	if to := goTimeout(full, r.runs()); to != "" {
		args = append(args, "-timeout", to)
	}
	if p := skipPattern(quarantine); p != "" {
		args = append(args, "-skip", p)
	}
	args = append(args, targets...)
	var b bytes.Buffer
	goCode, goErr := r.Exec.Run(ctx, Cmd{Name: "go", Args: args, Env: r.goEnv(full.Name)}, &b)
	rep := parseEvents(b.Bytes())
	result := 0
	if r.printFailures(rep, goCode, goErr) {
		result = 1
	}
	status := "PASS"
	if result != 0 {
		status = "FAIL"
	}
	r.printf("%s prepush: %d tests in %d package(s) in %v (tags %s)\n", status, len(rep.leaves()), len(targets), clock.Since(r.Clock, start).Round(1e6), full.Tags)
	return result
}

// changedFiles reads the file names from stdin.
func (r *Runner) changedFiles() []string {
	if r.Stdin == nil {
		return nil
	}
	var out []string
	sc := bufio.NewScanner(r.Stdin)
	for sc.Scan() {
		if f := strings.TrimSpace(sc.Text()); f != "" {
			out = append(out, f)
		}
	}
	return out
}

// parseList reads the output of go list with listFormat.
func parseList(out string) ([]pkgInfo, string) {
	var pkgs []pkgInfo
	module := ""
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(f) != 3 {
			continue
		}
		module = f[1]
		p := pkgInfo{importPath: f[0], dir: "."}
		if f[0] != module {
			p.dir = strings.TrimPrefix(f[0], module+"/")
		}
		for _, imp := range strings.Split(f[2], ",") {
			if imp != "" {
				p.imports = append(p.imports, imp)
			}
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, module
}

// affectedPackages maps changed files to the packages they belong to. all is
// set when a change can reach every package (go.mod, go.sum, a file that
// belongs to no package); notes tell what was left out.
func affectedPackages(pkgs []pkgInfo, module string, files []string) (changed map[string]bool, all bool, notes []string) {
	byDir := map[string]string{}
	for _, p := range pkgs {
		byDir[p.dir] = p.importPath
	}
	changed = map[string]bool{}
	for _, f := range files {
		if f == "go.mod" || f == "go.sum" {
			return nil, true, nil
		}
		if ip, ok := byDir[path.Dir(f)]; ok {
			changed[ip] = true
			continue
		}
		if strings.HasSuffix(f, ".go") {
			// A package that cannot be built here (only for Windows, or behind
			// a tag the full suite does not use): the CI runs it.
			notes = append(notes, f+" is in no package of this system: left to the CI")
			continue
		}
		// A data file (testdata, embedded files): the package above it.
		found := false
		for d := path.Dir(f); d != "." && d != "/"; d = path.Dir(d) {
			if ip, ok := byDir[d]; ok {
				changed[ip], found = true, true
				break
			}
		}
		if !found {
			return nil, true, nil
		}
	}
	return changed, false, notes
}

// dependents is the changed packages plus every package that imports one of
// them, directly or not, sorted.
func dependents(pkgs []pkgInfo, changed map[string]bool) []string {
	importers := map[string][]string{} // import path -> packages importing it
	for _, p := range pkgs {
		for _, imp := range p.imports {
			importers[imp] = append(importers[imp], p.importPath)
		}
	}
	seen := map[string]bool{}
	var queue []string
	for ip := range changed {
		seen[ip] = true
		queue = append(queue, ip)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, up := range importers[cur] {
			if !seen[up] {
				seen[up] = true
				queue = append(queue, up)
			}
		}
	}
	out := make([]string, 0, len(seen))
	for ip := range seen {
		out = append(out, ip)
	}
	sort.Strings(out)
	return out
}
