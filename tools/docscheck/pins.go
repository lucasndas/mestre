package main

import (
	"io/fs"
	"regexp"
	"strings"
)

// Where the pinned versions of go.mod are written down (check 14).
const (
	goModPath   = "go.mod"
	envPath     = "docs/desenvolvimento/ambiente.md"
	pinsHeader  = "Pinos do `go.mod`"
	pinsAnchor  = "pinos-do-gomod"
	pinsMissing = "tabela de pinos não encontrada"
)

var pinRowRe = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|\\s*`([^`]+)`\\s*\\|")

type pinned struct {
	value string
	line  int
}

// readGoMod returns the `go` and `toolchain` directives and the version of
// every required module, by name.
func readGoMod(b []byte) map[string]pinned {
	out := map[string]pinned{}
	inRequire := false
	for i, raw := range strings.Split(string(b), "\n") {
		line, _, _ := strings.Cut(raw, "//")
		f := strings.Fields(line)
		switch {
		case len(f) == 0:
		case inRequire && f[0] == ")":
			inRequire = false
		case f[0] == "require" && len(f) == 2 && f[1] == "(":
			inRequire = true
		case len(f) == 2 && (f[0] == "go" || f[0] == "toolchain"):
			out[f[0]] = pinned{f[1], i + 1}
		case inRequire && len(f) == 2:
			out[f[0]] = pinned{f[1], i + 1}
		case f[0] == "require" && len(f) == 3:
			out[f[1]] = pinned{f[2], i + 1}
		}
	}
	return out
}

// Check 14: go.mod keeps the values the environment document fixes.
func (c *checker) checkGoPins() {
	raw, err := fs.ReadFile(c.fsys, goModPath)
	if err != nil {
		return // a tree without a module (the tests of the checks)
	}
	env := c.docs[envPath]
	if env == nil {
		c.errf(goModPath, 1, ckPin, "%s não existe: é onde os pinos do go.mod estão escritos", envPath)
		return
	}
	start, end, ok := env.body(pinsHeader)
	if !ok {
		c.errf(envPath, 1, ckPin, "%s: falta a seção %q", pinsMissing, pinsHeader)
		return
	}
	want := map[string]string{}
	var order []string
	for i := start; i < end; i++ {
		if m := pinRowRe.FindStringSubmatch(env.lines[i]); m != nil {
			want[m[1]] = m[2]
			order = append(order, m[1])
		}
	}
	if len(order) == 0 {
		c.errf(envPath, start, ckPin, "%s: a seção %q não tem linhas `nome` | `valor`", pinsMissing, pinsHeader)
		return
	}
	have := readGoMod(raw)
	for _, name := range order {
		got, ok := have[name]
		switch {
		case !ok:
			c.errf(goModPath, 1, ckPin, "%s está fixado em %s#%s (%s) e não aparece no go.mod", name, envPath, pinsAnchor, want[name])
		case got.value != want[name]:
			c.errf(goModPath, got.line, ckPin, "%s é %s, mas %s#%s fixa %s: se a mudança é de propósito, mude o ambiente na mesma alteração (um `go get` pode subir a diretiva `go` sozinho)", name, got.value, envPath, pinsAnchor, want[name])
		}
	}
}
