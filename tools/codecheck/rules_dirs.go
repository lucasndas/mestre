package main

import (
	"go/ast"
	"strconv"
	"strings"
)

// privateDirPkg is the one place that may create a 0o700 folder: it checks the
// owner and the mode of a folder that already exists, which os.MkdirAll does
// not (it only sets the mode of what it creates).
const privateDirPkg = "internal/privdir/"

// checkPrivateDirs (private-dir) forbids os.MkdirAll and os.Mkdir with 0o700
// outside internal/privdir, in non-test code. The _windows.go files are left
// out: a folder has no mode there, and the ACL is internal/winsvc's.
func checkPrivateDirs(s *source, add func(Issue)) {
	if strings.HasPrefix(s.path, privateDirPkg) {
		return
	}
	osName := importName(s.file, "os")
	if osName == "" {
		return
	}
	ast.Inspect(s.file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		if !selector(call.Fun, osName, "MkdirAll") && !selector(call.Fun, osName, "Mkdir") {
			return true
		}
		if isOctal700(call.Args[1]) {
			add(Issue{s.path, s.line(call.Pos()), rulePrivateDir, "os.MkdirAll or os.Mkdir with 0o700 outside internal/privdir: call privdir.Ensure, which also checks the owner and the mode of a folder that was already there"})
		}
		return true
	})
}

// isOctal700 recognizes 0o700 (and 0700, 448) and os.FileMode(0o700).
func isOctal700(e ast.Expr) bool {
	if call, ok := e.(*ast.CallExpr); ok && len(call.Args) == 1 {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == "FileMode" {
			e = call.Args[0]
		}
	}
	lit, ok := e.(*ast.BasicLit)
	if !ok {
		return false
	}
	v, err := strconv.ParseInt(strings.ReplaceAll(lit.Value, "_", ""), 0, 64)
	return err == nil && v == 0o700
}
