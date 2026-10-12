// Command codecheck enforces the code rules that golangci-lint enforces in
// CI (.golangci.yml), so the fast suite catches them even where the linter
// is not installed (docs/qualidade/testes.md#regras-e-erros,
// docs/desenvolvimento/convencoes.md#estilo):
//
//   - time: time.Sleep, Now, After, NewTimer, NewTicker, AfterFunc and Since
//     only in pkg/clock (forbidigo);
//   - testkit: production code never imports internal/testkit (depguard);
//   - guardian: guardian/ imports only the standard library, pkg/clock and
//     the allowed modules (depguard);
//   - parallel: every Test function and every t.Run subtest calls
//     t.Parallel (paralleltest).
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
	"path"
	"regexp"
	"slices"
	"strconv"
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

var (
	forbiddenTime = []string{"Sleep", "Now", "After", "NewTimer", "NewTicker", "AfterFunc", "Since"}
	clockDir      = "pkg/clock"
	// Folders where test doubles may be imported outside _test.go files.
	testkitAllowed = []string{"internal/testkit", "cmd/fakeagent", "tools"}
	guardianAllow  = []string{"pkg/clock", "guardian", "golang.org/x/sys", "golang.org/x/crypto",
		"github.com/kardianos/service", "github.com/coder/websocket", "github.com/coreos/go-systemd",
		"aead.dev/minisign", "github.com/brutella/dnssd"}
	moduleRe = regexp.MustCompile(`(?m)^module\s+(\S+)`)
)

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

// Check walks the module at the root of fsys.
func Check(fsys fs.FS) ([]Issue, error) {
	gomod, err := fs.ReadFile(fsys, "go.mod")
	if err != nil {
		return nil, fmt.Errorf("read go.mod: %w", err)
	}
	m := moduleRe.FindSubmatch(gomod)
	if m == nil {
		return nil, fmt.Errorf("go.mod has no module line")
	}
	module := string(m[1])
	var issues []Issue
	err = fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "node_modules", "testdata", "web":
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") {
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
		issues = append(issues, checkFile(fset, p, f, module)...)
		return nil
	})
	return issues, err
}

func under(p, dir string) bool { return p == dir || strings.HasPrefix(p, dir+"/") }

func checkFile(fset *token.FileSet, p string, f *ast.File, module string) []Issue {
	var issues []Issue
	add := func(pos token.Pos, rule, format string, args ...any) {
		issues = append(issues, Issue{File: p, Line: fset.Position(pos).Line, Rule: rule, Msg: fmt.Sprintf(format, args...)})
	}
	isTest := strings.HasSuffix(p, "_test.go")
	timeName := ""
	for _, imp := range f.Imports {
		ip, _ := strconv.Unquote(imp.Path.Value)
		if ip == "time" {
			timeName = "time"
			if imp.Name != nil {
				timeName = imp.Name.Name
			}
		}
		if rel, ok := strings.CutPrefix(ip, module+"/"); ok && under(rel, "internal/testkit") && !isTest &&
			!slices.ContainsFunc(testkitAllowed, func(d string) bool { return under(path.Dir(p), d) }) {
			add(imp.Pos(), "testkit", "production code imports %s (test doubles are for tests only)", ip)
		}
		if under(path.Dir(p), "guardian") && !isTest && !guardianAllowed(ip, module) {
			add(imp.Pos(), "guardian", "guardian/ may not import %s", ip)
		}
	}
	if timeName != "" && timeName != "_" && !under(path.Dir(p), clockDir) {
		ast.Inspect(f, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == timeName && slices.Contains(forbiddenTime, sel.Sel.Name) {
				add(sel.Pos(), "time", "time.%s outside pkg/clock: use the injectable clock", sel.Sel.Name)
			}
			return true
		})
	}
	if isTest {
		issues = append(issues, checkParallel(fset, p, f)...)
	}
	return issues
}

func guardianAllowed(ip, module string) bool {
	first, _, _ := strings.Cut(ip, "/")
	if !strings.Contains(first, ".") {
		return true // standard library
	}
	if rel, ok := strings.CutPrefix(ip, module+"/"); ok {
		ip = rel
	}
	return slices.ContainsFunc(guardianAllow, func(a string) bool { return under(ip, a) })
}

// checkParallel requires t.Parallel() in every TestXxx(t *testing.T) and in
// every func literal passed to t.Run.
func checkParallel(fset *token.FileSet, p string, f *ast.File) []Issue {
	var issues []Issue
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Name.Name == "TestMain" || fn.Body == nil {
			continue
		}
		t := testingParam(fn.Type)
		if t == "" {
			continue
		}
		if !callsParallel(fn.Body, t) {
			issues = append(issues, Issue{p, fset.Position(fn.Pos()).Line, "parallel", fn.Name.Name + " does not call " + t + ".Parallel()"})
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
	star, ok := ft.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return ""
	}
	sel, ok := star.X.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "T" {
		return ""
	}
	if id, ok := sel.X.(*ast.Ident); !ok || id.Name != "testing" {
		return ""
	}
	return ft.Params.List[0].Names[0].Name
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
