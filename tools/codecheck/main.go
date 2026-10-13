// Command codecheck enforces the code rules golangci-lint does not cover
// (docs/qualidade/regras-de-codigo.md). Each rule lives in one tool: time,
// testkit and guardian imports and the t.Parallel of TestXxx functions are in
// .golangci.yml; the rules that read the structure of the code, or need an
// exception with a reason, are here:
//
//   - parallel: every func literal passed to t.Run calls t.Parallel;
//   - bare-select: no bare `select {}` in test code and test helpers;
//   - child-stdio: a child process of a test never gets os.Stdout or os.Stderr;
//   - nil-sa: no nil security attributes where a descriptor matters, in
//     _windows.go files;
//   - private-dir: os.MkdirAll and os.Mkdir with 0o700 only in internal/privdir;
//   - secret-receiver: the printing methods of a secret use value receivers.
//
// A violation is excused by `// mestre:allow <rule>: <reason>` on its line or
// the line above. The command runs as TestCodeRules and alone as
// `go run ./tools/codecheck`.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// Rule names, as printed and as written in mestre:allow.
const (
	ruleParallel   = "parallel"
	ruleSelect     = "bare-select"
	ruleStdio      = "child-stdio"
	ruleNilSA      = "nil-sa"
	rulePrivateDir = "private-dir"
	ruleSecret     = "secret-receiver"
)

// Issue is one violation.
type Issue struct {
	File string
	Line int
	Rule string
	Msg  string
}

func (i Issue) String() string { return fmt.Sprintf("%s:%d: %s: %s", i.File, i.Line, i.Rule, i.Msg) }

func main() {
	issues, err := Check(os.DirFS("."))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, i := range issues {
		fmt.Println(i)
	}
	if len(issues) > 0 {
		os.Exit(1)
	}
}

// source is one parsed Go file.
type source struct {
	path string
	fset *token.FileSet
	file *ast.File
	err  error
}

func (s *source) isTest() bool    { return strings.HasSuffix(s.path, "_test.go") }
func (s *source) isWindows() bool { return strings.HasSuffix(s.path, "_windows.go") }

// isTestCode: test files and the packages that only exist for tests.
func (s *source) isTestCode() bool {
	return s.isTest() || strings.HasPrefix(s.path, "internal/testkit/") || strings.HasPrefix(s.path, "cmd/fakeagent/")
}

// isSpike: spike code is kept as reference, outside the lint and these rules
// (docs/desenvolvimento/convencoes.md).
func (s *source) isSpike() bool {
	return strings.HasPrefix(s.path, "tools/spikes/") || strings.HasPrefix(s.path, "tools/winsandbox/spikes/")
}

func (s *source) line(p token.Pos) int { return s.fset.Position(p).Line }

// Check walks the Go files of the module at the root of fsys.
func Check(fsys fs.FS) ([]Issue, error) {
	var paths []string
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Like the go tool: folders starting with "." or "_" (VCS data,
			// .claude/worktrees, ...) are not part of the module.
			if p != "." && (strings.HasPrefix(d.Name(), ".") || strings.HasPrefix(d.Name(), "_")) {
				return fs.SkipDir
			}
			switch d.Name() {
			case "node_modules", "testdata", "web":
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			paths = append(paths, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	srcs := parseAll(fsys, paths)
	for _, s := range srcs {
		if s.err != nil {
			return nil, s.err
		}
	}

	byFile := map[string][]Issue{}
	add := func(i Issue) { byFile[i.File] = append(byFile[i.File], i) }
	for _, s := range srcs {
		checkSubtests(s, add)
		if s.isSpike() {
			continue
		}
		if s.isTestCode() {
			checkTestCode(s, add)
		}
		if s.isWindows() && !s.isTest() {
			checkNilSA(s, add)
		}
		if !s.isTest() && !s.isWindows() {
			checkPrivateDirs(s, add)
		}
	}
	checkSecretReceivers(srcs, add)

	var issues []Issue
	for _, s := range srcs {
		issues = append(issues, applyAllows(s, byFile[s.path])...)
	}
	sort.SliceStable(issues, func(i, j int) bool {
		if issues[i].File != issues[j].File {
			return issues[i].File < issues[j].File
		}
		return issues[i].Line < issues[j].Line
	})
	return issues, nil
}

// parseAll parses the files in parallel, in the order of paths.
func parseAll(fsys fs.FS, paths []string) []*source {
	fset := token.NewFileSet()
	out := make([]*source, len(paths))
	var next atomic.Int64
	var wg sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), len(paths)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				i := int(next.Add(1)) - 1
				if i >= len(paths) {
					return
				}
				s := &source{path: paths[i], fset: fset}
				src, err := fs.ReadFile(fsys, paths[i])
				if err == nil {
					s.file, err = parser.ParseFile(fset, paths[i], src, parser.SkipObjectResolution|parser.ParseComments)
				}
				if err != nil {
					s.err = fmt.Errorf("parse %s: %w", paths[i], err)
				}
				out[i] = s
			}
		}()
	}
	wg.Wait()
	return out
}

// importName is the name the file gives to the package with that import
// path, or "" when it does not import it.
func importName(f *ast.File, importPath string) string {
	for _, im := range f.Imports {
		if strings.Trim(im.Path.Value, "\"`") != importPath {
			continue
		}
		if im.Name != nil {
			return im.Name.Name
		}
		return importPath[strings.LastIndex(importPath, "/")+1:]
	}
	return ""
}

// selector reports whether e is pkg.name.
func selector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg
}

func isNil(e ast.Expr) bool {
	id, ok := e.(*ast.Ident)
	return ok && id.Name == "nil"
}

// checkSubtests requires t.Parallel() in every func literal passed to t.Run
// inside a function or method with a *testing.T parameter: TestXxx(t
// *testing.T) and helpers such as runCases(t *testing.T, cases []c).
func checkSubtests(s *source, add func(Issue)) {
	if !s.isTest() {
		return
	}
	for _, decl := range s.file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || !hasTestingParam(fn.Type) {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Run" {
				return true
			}
			lit, ok := call.Args[1].(*ast.FuncLit)
			if !ok {
				return true
			}
			if sub := testingParam(lit.Type); sub != "" && !callsParallel(lit.Body, sub) {
				add(Issue{s.path, s.line(call.Pos()), ruleParallel, "subtest of " + fn.Name.Name + " does not call " + sub + ".Parallel()"})
			}
			return true
		})
	}
}

// testingParam returns the name of a single *testing.T parameter.
func testingParam(ft *ast.FuncType) string {
	if ft.Params == nil || len(ft.Params.List) != 1 || len(ft.Params.List[0].Names) != 1 {
		return ""
	}
	if !isTestingT(ft.Params.List[0].Type) {
		return ""
	}
	return ft.Params.List[0].Names[0].Name
}

// hasTestingParam says whether any parameter is a *testing.T.
func hasTestingParam(ft *ast.FuncType) bool {
	if ft.Params == nil {
		return false
	}
	for _, field := range ft.Params.List {
		if isTestingT(field.Type) {
			return true
		}
	}
	return false
}

func isTestingT(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if !ok {
		return false
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == "testing"
}

// callsParallel looks for <t>.Parallel() among the top-level statements.
func callsParallel(body *ast.BlockStmt, t string) bool {
	for _, st := range body.List {
		es, ok := st.(*ast.ExprStmt)
		if !ok {
			continue
		}
		call, ok := es.X.(*ast.CallExpr)
		if !ok {
			continue
		}
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "Parallel" {
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == t {
				return true
			}
		}
	}
	return false
}
