package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// The suites build every test binary first, outside the budget, and then run
// the binaries, which is the only part the budget times
// (docs/qualidade/testes.md#executor-testrun). Compiling and, above all,
// linking about 80 binaries is a cost of the machine, not of the tests, and
// the link is not cached.

// testListFormat is the `go list` format of the packages: import path, module
// path and the number of test files (in the package and external).
const testListFormat = `{{.ImportPath}}|{{.Module.Path}}|{{len .TestGoFiles}}|{{len .XTestGoFiles}}`

// testPkg is one package with tests and its built binary.
type testPkg struct {
	importPath string
	dir        string // relative to the repository root; "" is the root
	bin        string // absolute path of the test binary
}

// built is the outcome of the build step.
type built struct {
	pkgs      []testPkg
	test2json string // the test2json tool, "" to run it with `go tool`
	took      time.Duration
	cleanup   func()
}

func (b *built) find(importPath string) (testPkg, bool) {
	for _, p := range b.pkgs {
		if p.importPath == importPath {
			return p, true
		}
	}
	return testPkg{}, false
}

// exeSuffix is the extension go gives an executable.
func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

func (r *Runner) mkTemp() (string, func(), error) {
	if r.MkTemp != nil {
		return r.MkTemp()
	}
	dir, err := os.MkdirTemp("", "testrun-bin-")
	if err != nil {
		return "", nil, err
	}
	return dir, func() { _ = os.RemoveAll(dir) }, nil
}

// parseTestList reads the output of go list with testListFormat and returns
// the packages that have test files.
func parseTestList(out string) []testPkg {
	var pkgs []testPkg
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(strings.TrimSpace(line), "|")
		if len(f) != 4 {
			continue
		}
		n1, _ := strconv.Atoi(f[2])
		n2, _ := strconv.Atoi(f[3])
		if n1+n2 == 0 {
			continue
		}
		p := testPkg{importPath: f[0]}
		if f[0] != f[1] {
			p.dir = strings.TrimPrefix(f[0], f[1]+"/")
		}
		pkgs = append(pkgs, p)
	}
	return pkgs
}

// build compiles and links the test binaries of every package of the suite,
// with the tags, race and gcflags of the suite. It prints the time it took
// ("build took ...", not part of the budget) and returns nil with the exit
// code when it fails.
func (r *Runner) build(ctx context.Context, s Suite) (*built, int) {
	start := r.Clock.Now()
	dir, cleanup, err := r.mkTemp()
	if err != nil {
		r.printf("testrun: temporary directory for the test binaries: %v\n", err)
		return nil, 2
	}
	ok := false
	defer func() {
		if !ok {
			cleanup()
		}
	}()
	env := r.goEnv(s.Name)
	listArgs := []string{"list", "-f", testListFormat}
	if s.Tags != "" {
		listArgs = append(listArgs, "-tags", s.Tags)
	}
	var listed bytes.Buffer
	code, err := r.Exec.Run(ctx, Cmd{Name: "go", Args: append(listArgs, "./..."), Env: env, MergeStderr: true}, &listed)
	if err != nil || code != 0 {
		r.printf("FAIL %s: go list failed (exit %d, %s):\n%s", s.Name, code, errOrEmpty(err), listed.String())
		return nil, 1
	}
	pkgs := parseTestList(listed.String())

	// One binary per import path, with a file name of its own: go names a
	// binary after the last element of the path, and two packages may share it.
	base := map[string]int{}
	for _, p := range pkgs {
		base[path.Base(p.importPath)]++
	}
	var shared, unique []string
	for i := range pkgs {
		p := &pkgs[i]
		if base[path.Base(p.importPath)] == 1 {
			p.bin = filepath.Join(dir, path.Base(p.importPath)+".test"+exeSuffix())
			unique = append(unique, p.importPath)
			continue
		}
		name := strings.ReplaceAll(p.dir, "/", "_")
		p.bin = filepath.Join(dir, name+".test"+exeSuffix())
		shared = append(shared, p.importPath)
	}
	buildArgs := func(out string, targets ...string) []string {
		args := []string{"test", "-c", "-o", out}
		if s.Tags != "" {
			args = append(args, "-tags", s.Tags)
		}
		if r.Race {
			args = append(args, "-race", RaceGcflags)
		}
		return append(args, targets...)
	}
	run := func(args []string) bool {
		var b bytes.Buffer
		code, err := r.Exec.Run(ctx, Cmd{Name: "go", Args: args, Env: env, MergeStderr: true}, &b)
		if err != nil || code != 0 {
			r.printf("FAIL %s: building the test binaries failed (exit %d, %s):\n%s", s.Name, code, errOrEmpty(err), b.String())
			return false
		}
		return true
	}
	if len(unique) > 0 && !run(buildArgs(dir+string(filepath.Separator), unique...)) {
		return nil, 1
	}
	// The packages that share a name are built one by one (the compiled
	// code is already cached, only the link is repeated).
	for _, p := range pkgs {
		if slices.Contains(shared, p.importPath) && !run(buildArgs(p.bin, p.importPath)) {
			return nil, 1
		}
	}

	// The tool that turns the output of a binary into JSON; started directly
	// it spares a `go` start per binary.
	b := &built{pkgs: pkgs}
	var tool bytes.Buffer
	if code, err := r.Exec.Run(ctx, Cmd{Name: "go", Args: []string{"tool", "-n", "test2json"}, Env: env}, &tool); err == nil && code == 0 {
		b.test2json = strings.TrimSpace(tool.String())
	}
	b.took = clock.Since(r.Clock, start)
	b.cleanup = cleanup
	ok = true
	r.printf("build took %v (%d test binaries, not counted in the budget)\n", b.took.Round(time.Millisecond), len(pkgs))
	return b, 0
}

// testCmd is the run of the binary of p, from the directory of the package
// (the tests read testdata relative to it), converted to the JSON of
// `go test -json` by test2json. flags are `-test.*` ones.
func (b *built) testCmd(p testPkg, env []string, flags ...string) Cmd {
	args := []string{"-p", p.importPath, "-t", p.bin, "-test.v=test2json", "-test.paniconexit0"}
	args = append(args, flags...)
	if b.test2json != "" {
		return Cmd{Name: b.test2json, Args: args, Dir: p.dir, Env: env}
	}
	return Cmd{Name: "go", Args: append([]string{"tool", "test2json"}, args...), Dir: p.dir, Env: env}
}

// runBinaries runs the test binaries at most GOMAXPROCS at a time, like
// `go test -p`, and returns their JSON output joined in package order, the
// worst exit code and the first error.
func (r *Runner) runBinaries(ctx context.Context, b *built, env []string, flags []string) ([]byte, int, error) {
	outs := make([]bytes.Buffer, len(b.pkgs))
	codes := make([]int, len(b.pkgs))
	errs := make([]error, len(b.pkgs))
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for i, p := range b.pkgs {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			codes[i], errs[i] = r.Exec.Run(ctx, b.testCmd(p, env, flags...), &outs[i])
		}()
	}
	wg.Wait()
	var all bytes.Buffer
	code := 0
	var err error
	for i := range b.pkgs {
		all.Write(outs[i].Bytes())
		code = max(code, codes[i])
		if err == nil && errs[i] != nil {
			err = fmt.Errorf("%s: %w", b.pkgs[i].importPath, errs[i])
		}
	}
	return all.Bytes(), code, err
}
