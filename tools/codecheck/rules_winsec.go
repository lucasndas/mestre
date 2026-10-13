package main

import (
	"go/ast"
	"strings"
)

const windowsSys = "golang.org/x/sys/windows"

// saArgs says which arguments of a golang.org/x/sys/windows call are the
// security attributes (a descriptor); -1 counts from the end.
var saArgs = map[string][]int{
	"CreateJobObject":     {0},
	"CreateNamedPipe":     {-1},
	"CreateProcess":       {2, 3},
	"CreateProcessAsUser": {3, 4},
	"CreateFile":          {3},
}

// checkNilSA (nil-sa) forbids the literal nil as the security attributes of a
// Job Object, a named pipe, a new file or a new process in the _windows.go
// files of non-test code: with nil the object takes the default descriptor of
// the token, which is not the ACL the design asks for (docs/arquitetura/
// windows.md). Opening something that exists (CreateFile with OPEN_EXISTING or
// TRUNCATE_EXISTING) creates nothing, so nil is fine there. A variable that
// holds nil is out of reach of this rule; the exception is
// `// mestre:allow nil-sa: <reason>`.
func checkNilSA(s *source, add func(Issue)) {
	name := importName(s.file, windowsSys)
	if name == "" {
		return
	}
	ast.Inspect(s.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); !ok || id.Name != name {
			return true
		}
		idx, ok := saArgs[sel.Sel.Name]
		if !ok {
			return true
		}
		if sel.Sel.Name == "CreateFile" && len(call.Args) > 4 && opensExisting(call.Args[4], name) {
			return true
		}
		for _, i := range idx {
			if i < 0 {
				i += len(call.Args)
			}
			if i >= 0 && i < len(call.Args) && isNil(call.Args[i]) {
				add(Issue{s.path, s.line(call.Pos()), ruleNilSA, "windows." + sel.Sel.Name + " with nil security attributes: build the descriptor (an SDDL, as securityAttributes in internal/winsvc does) or write `// mestre:allow nil-sa: <reason>`"})
				break
			}
		}
		return true
	})
}

// opensExisting: the creation disposition of CreateFile creates no file.
func opensExisting(e ast.Expr, pkg string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	id, ok := sel.X.(*ast.Ident)
	return ok && id.Name == pkg && strings.HasSuffix(sel.Sel.Name, "_EXISTING")
}
