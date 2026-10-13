package main

import (
	"go/ast"
)

// checkTestCode applies the rules of test code and of the packages that only
// exist for tests (internal/testkit, cmd/fakeagent):
//
//   - bare-select: `select {}` blocks a helper program for ever, and on
//     Windows a Go program with nothing else running dies of "all goroutines
//     are asleep". The helper waits on a pending timer instead: the block()
//     function of internal/terminal/harness_full_test.go.
//   - child-stdio: a child process that gets the os.Stdout or os.Stderr of the
//     test keeps the pipe of `go test` open after the test ends (and, when it
//     outlives the test, hangs the run). Give it a buffer, a file or
//     io.Discard.
func checkTestCode(s *source, add func(Issue)) {
	osName := importName(s.file, "os")
	ast.Inspect(s.file, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectStmt:
			if len(n.Body.List) == 0 {
				add(Issue{s.path, s.line(n.Pos()), ruleSelect, "bare select {} in test code: on Windows the program dies of \"all goroutines are asleep\"; wait like block() in internal/terminal/harness_full_test.go (a pending timer)"})
			}
		case *ast.AssignStmt:
			if osName == "" {
				return true
			}
			for i, lhs := range n.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || (sel.Sel.Name != "Stdout" && sel.Sel.Name != "Stderr") {
					continue
				}
				rhs := n.Rhs[0]
				if len(n.Rhs) == len(n.Lhs) {
					rhs = n.Rhs[i]
				}
				if mentionsStdStream(rhs, osName) {
					add(Issue{s.path, s.line(n.Pos()), ruleStdio, "a child process of a test gets the os.Stdout or os.Stderr of the test: use a buffer, a file or io.Discard"})
				}
			}
		case *ast.CompositeLit:
			if osName == "" {
				return true
			}
			checkStdioLiteral(s, n, osName, add)
		}
		return true
	})
}

// checkStdioLiteral covers exec.Cmd{Stdout: os.Stdout} and
// os.ProcAttr{Files: []*os.File{os.Stdin, os.Stdout, os.Stderr}}.
func checkStdioLiteral(s *source, lit *ast.CompositeLit, osName string, add func(Issue)) {
	typ, ok := lit.Type.(*ast.SelectorExpr)
	if !ok {
		return
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		isCmd := typ.Sel.Name == "Cmd" && (key.Name == "Stdout" || key.Name == "Stderr")
		isProc := typ.Sel.Name == "ProcAttr" && key.Name == "Files"
		if (isCmd || isProc) && mentionsStdStream(kv.Value, osName) {
			add(Issue{s.path, s.line(kv.Pos()), ruleStdio, "a child process of a test gets the os.Stdout or os.Stderr of the test: use a buffer, a file or io.Discard"})
		}
	}
}

// mentionsStdStream says whether e contains os.Stdout or os.Stderr.
func mentionsStdStream(e ast.Expr, osName string) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if x, ok := n.(ast.Expr); ok && (selector(x, osName, "Stdout") || selector(x, osName, "Stderr")) {
			found = true
		}
		return !found
	})
	return found
}
