// Command codecheck enforces the code rules golangci-lint does not cover
// (docs/desenvolvimento/convencoes.md#estilo). Each rule lives in one tool:
// time, testkit and guardian imports and the t.Parallel of TestXxx
// functions are in .golangci.yml; this command keeps only
//
//   - subtests: every func literal passed to t.Run calls t.Parallel
//     (paralleltest does not report subtests), in TestXxx functions and in
//     any helper that takes a *testing.T.
//
// It runs as TestCodeRules and alone as `go run ./tools/codecheck`.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"strings"
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

// Check walks the test files of the module at the root of fsys.
func Check(fsys fs.FS) ([]Issue, error) {
	var issues []Issue
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			// Like the go tool: folders starting with "." or "_" (.git,
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
		if !strings.HasSuffix(p, "_test.go") {
			return nil
		}
		src, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, src, parser.SkipObjectResolution)
		if err != nil {
			return fmt.Errorf("parse %s: %w", p, err)
		}
		issues = append(issues, checkSubtests(fset, p, f)...)
		return nil
	})
	return issues, err
}

// checkSubtests requires t.Parallel() in every func literal passed to t.Run
// inside a function or method with a *testing.T parameter: TestXxx(t
// *testing.T) and helpers such as runCases(t *testing.T, cases []c).
func checkSubtests(fset *token.FileSet, p string, f *ast.File) []Issue {
	var issues []Issue
	for _, decl := range f.Decls {
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
				issues = append(issues, Issue{p, fset.Position(call.Pos()).Line, "parallel", "subtest of " + fn.Name.Name + " does not call " + sub + ".Parallel()"})
			}
			return true
		})
	}
	return issues
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
