package main

import (
	"go/ast"
	"go/token"
	"path"
	"strings"
)

// printing are the methods fmt, slog and the encoders call to show a value.
// On a pointer receiver they only apply to the pointer: fmt.Sprint(*secret),
// a struct holding the value and a copy printed with %v would show the secret.
var printing = map[string]bool{"String": true, "GoString": true, "Format": true, "LogValue": true}

// encoding methods that matter only when they redact.
var encoding = map[string]bool{"MarshalJSON": true, "MarshalText": true}

// checkSecretReceivers (secret-receiver) flags a printing method on a pointer
// receiver when its type is a secret: it has a Reveal method (the only way to
// read the value), or the method itself returns the redaction marker
// (logging.Redacted, "[segredo]"). Encoding methods count only in the second
// case, since a secret that travels encrypted reveals itself there.
func checkSecretReceivers(srcs []*source, add func(Issue)) {
	type typeKey struct{ dir, name string }
	reveal := map[typeKey]bool{}
	var files []*source
	for _, s := range srcs {
		if s.isTest() || s.isSpike() {
			continue
		}
		files = append(files, s)
		for _, d := range s.file.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv != nil && fn.Name.Name == "Reveal" {
				reveal[typeKey{path.Dir(s.path), receiver(fn).name}] = true
			}
		}
	}
	for _, s := range files {
		for _, d := range s.file.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Body == nil {
				continue
			}
			name := fn.Name.Name
			if !printing[name] && !encoding[name] {
				continue
			}
			r := receiver(fn)
			if !r.pointer {
				continue
			}
			switch {
			case printing[name] && reveal[typeKey{path.Dir(s.path), r.name}]:
				add(Issue{s.path, s.line(fn.Pos()), ruleSecret, r.name + " has a Reveal method, so " + name + " must have a value receiver: with *" + r.name + " a copy of the secret prints in the clear"})
			case redacts(fn.Body):
				add(Issue{s.path, s.line(fn.Pos()), ruleSecret, name + " of *" + r.name + " returns the redaction marker on a pointer receiver: use a value receiver, or fmt.Sprint(*v) prints the secret"})
			}
		}
	}
}

type recv struct {
	name    string
	pointer bool
}

// receiver is the type name of the receiver, without the star and the type
// parameters.
func receiver(fn *ast.FuncDecl) recv {
	var r recv
	e := fn.Recv.List[0].Type
	if star, ok := e.(*ast.StarExpr); ok {
		r.pointer, e = true, star.X
	}
	switch t := e.(type) {
	case *ast.IndexExpr:
		e = t.X
	case *ast.IndexListExpr:
		e = t.X
	}
	if id, ok := e.(*ast.Ident); ok {
		r.name = id.Name
	}
	return r
}

// redacts says whether the body mentions the redaction marker: the Redacted
// constant of internal/logging or its text.
func redacts(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.SelectorExpr:
			found = found || n.Sel.Name == "Redacted"
		case *ast.Ident:
			found = found || n.Name == "Redacted"
		case *ast.BasicLit:
			found = found || (n.Kind == token.STRING && strings.Contains(n.Value, "[segredo]"))
		}
		return !found
	})
	return found
}
