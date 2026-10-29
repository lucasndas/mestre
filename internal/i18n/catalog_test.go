package i18n

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var placeholder = regexp.MustCompile(`{{\s*(\w+)\s*}}`)

func placeholders(s string) string {
	var names []string
	for _, m := range placeholder.FindAllStringSubmatch(s, -1) {
		names = append(names, m[1])
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// baseOf is the code of a key without its plural suffix.
func baseOf(key string) (string, string) {
	for _, suf := range []string{"_one", "_other"} {
		if strings.HasSuffix(key, suf) {
			return strings.TrimSuffix(key, suf), suf
		}
	}
	return key, ""
}

// parity returns what is wrong between the two languages of a file: the same keys,
// no empty text, the same {{parameters}} in each text and both forms of the plural.
func parity(file string, pt, en map[string]string) []string {
	var bad []string
	for k, t := range pt {
		e, ok := en[k]
		switch {
		case !ok:
			bad = append(bad, fmt.Sprintf("%s: %q has no text in en", file, k))
			continue
		case strings.TrimSpace(t) == "":
			bad = append(bad, fmt.Sprintf("%s: %q is empty in pt-BR", file, k))
		case strings.TrimSpace(e) == "":
			bad = append(bad, fmt.Sprintf("%s: %q is empty in en", file, k))
		case placeholders(t) != placeholders(e):
			bad = append(bad, fmt.Sprintf("%s: %q has the parameters {%s} in pt-BR and {%s} in en", file, k, placeholders(t), placeholders(e)))
		}
	}
	for k := range en {
		if _, ok := pt[k]; !ok {
			bad = append(bad, fmt.Sprintf("%s: %q has no text in pt-BR", file, k))
		}
	}
	for _, m := range []map[string]string{pt, en} {
		for k := range m {
			if base, suf := baseOf(k); suf != "" {
				other := "_one"
				if suf == "_one" {
					other = "_other"
				}
				if _, ok := m[base+other]; !ok {
					bad = append(bad, fmt.Sprintf("%s: %q has %s but not %s", file, base, suf, other))
				}
			}
		}
	}
	sort.Strings(bad)
	return bad
}

func readAll(t *testing.T) (pt, en map[string]map[string]string) {
	t.Helper()
	pt, en = map[string]map[string]string{}, map[string]map[string]string{}
	for _, f := range Files(PtBR) {
		m, err := ReadFile(PtBR, f)
		if err != nil {
			t.Fatal(err)
		}
		pt[f] = m
	}
	for _, f := range Files(En) {
		m, err := ReadFile(En, f)
		if err != nil {
			t.Fatal(err)
		}
		en[f] = m
	}
	return pt, en
}

// CA-MSG-01
func TestCatalogsHaveParity(t *testing.T) {
	t.Parallel()
	// the checker names the file and the key of what is wrong
	got := strings.Join(parity("decree.json",
		map[string]string{"decree.a": "x", "decree.b": "Olá {{name}}", "decree.c_one": "{{count}} a"},
		map[string]string{"decree.b": "Hello {{nome}}", "decree.c_one": "{{count}} a"}), "\n")
	for _, want := range []string{
		`decree.json: "decree.a" has no text in en`,
		`decree.json: "decree.b" has the parameters {name} in pt-BR and {nome} in en`,
		`decree.json: "decree.c" has _one but not _other`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the checker did not say %q; it said:\n%s", want, got)
		}
	}

	pt, en := readAll(t)
	files := map[string]bool{}
	for f := range pt {
		files[f] = true
	}
	for f := range en {
		files[f] = true
	}
	for f := range files {
		if _, ok := pt[f]; !ok {
			t.Errorf("%s exists in en and not in pt-BR", f)
			continue
		}
		if _, ok := en[f]; !ok {
			t.Errorf("%s exists in pt-BR and not in en", f)
			continue
		}
		area := strings.TrimSuffix(f, ".json")
		for _, msg := range parity(f, pt[f], en[f]) {
			t.Error(msg)
		}
		for k := range pt[f] {
			if !strings.HasPrefix(k, area+".") {
				t.Errorf("%s: %q does not start with %q", f, k, area+".")
			}
		}
	}
}

var codeLiteral = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9_]*$`)

// makers are the functions of the Go code that make a message (or a refusal, or an error of the
// API) from a code: a string literal in their arguments that has the shape of a code and an
// area with a file in the catalogs is a code the code declares.
var makers = map[string]bool{
	"M": true, "Refuse": true, "ErrMsg": true, "Err": true, "refuse": true, "refuseMsg": true,
	"human": true, "FailMsg": true, "Fail": true, "Msg": true, "codedErr": true,
}

func calleeName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return x.Sel.Name
	}
	return ""
}

// codesIn adds to out the codes a Go source (outside tests) declares, whose area is one of areas.
func codesIn(src []byte, rel string, areas map[string]bool, out map[string]string) {
	f, perr := parser.ParseFile(token.NewFileSet(), rel, src, 0)
	if perr != nil {
		return
	}
	see := func(e ast.Expr) {
		lit, ok := e.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return
		}
		s, err := strconv.Unquote(lit.Value)
		if err != nil || !codeLiteral.MatchString(s) {
			return
		}
		if area, _, _ := strings.Cut(s, "."); areas[area] {
			out[s] = rel
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			if makers[calleeName(x.Fun)] {
				for _, a := range x.Args {
					see(a)
				}
			}
		case *ast.CompositeLit:
			if strings.HasSuffix(strings.TrimPrefix(exprName(x.Type), "i18n."), "Msg") {
				for _, el := range x.Elts {
					if kv, ok := el.(*ast.KeyValueExpr); ok {
						if id, ok := kv.Key.(*ast.Ident); ok && id.Name == "Code" {
							see(kv.Value)
						}
					}
				}
			}
		}
		return true
	})
}

// declaredCodes are the codes the Go code (outside tests) declares.
func declaredCodes(t *testing.T, areas map[string]bool) map[string]string {
	t.Helper()
	root := filepath.Join("..", "..")
	out := map[string]string{}
	for _, dir := range []string{"internal", "tools", "cmd"} {
		_ = filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			if src, rerr := os.ReadFile(path); rerr == nil {
				codesIn(src, filepath.ToSlash(path), areas, out)
			}
			return nil
		})
	}
	return out
}

// checkCodes compares the codes the Go code declares (code -> file) with the codes that have a
// text (code -> file of the catalog), in both directions, naming the code in each message.
func checkCodes(declared, have map[string]string) []string {
	var missing, unused []string
	for code, file := range declared {
		if _, ok := have[code]; !ok {
			missing = append(missing, fmt.Sprintf("%s (declared in %s) has no text in the catalogs", code, file))
		}
	}
	for code, file := range have {
		if _, ok := declared[code]; !ok {
			unused = append(unused, fmt.Sprintf("%s (text in %s) has no code declared in the Go code", code, file))
		}
	}
	sort.Strings(missing)
	sort.Strings(unused)
	return append(missing, unused...)
}

func exprName(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.SelectorExpr:
		return exprName(x.X) + "." + x.Sel.Name
	}
	return ""
}

// CA-MSG-02
func TestEveryDeclaredCodeHasATextAndEveryTextACode(t *testing.T) {
	t.Parallel()
	pt, _ := readAll(t)
	areas := map[string]bool{}
	have := map[string]string{} // code -> file
	for f, m := range pt {
		areas[strings.TrimSuffix(f, ".json")] = true
		for k := range m {
			base, _ := baseOf(k)
			have[base] = f
		}
	}
	for _, m := range checkCodes(declaredCodes(t, areas), have) {
		t.Error(m)
	}
}

// CA-MSG-02
// The check names the code in both directions, and the extraction finds the codes a source
// declares (and only the ones of its makers, with an area of the catalogs).
func TestTheCheckOfTheCodesNamesTheCodeInBothDirections(t *testing.T) {
	t.Parallel()
	got := strings.Join(checkCodes(
		map[string]string{"decree.novo": "x.go", "decree.ok": "y.go"},
		map[string]string{"decree.velho": "decree.json", "decree.ok": "decree.json"}), "\n")
	for _, want := range []string{
		`decree.novo (declared in x.go) has no text in the catalogs`,
		`decree.velho (text in decree.json) has no code declared in the Go code`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "decree.ok") || strings.Count(got, "\n") != 1 {
		t.Errorf("a code in both sides must not be reported, and only the two were: %s", got)
	}
	if out := checkCodes(map[string]string{"a.b": "x.go"}, map[string]string{"a.b": "a.json"}); len(out) != 0 {
		t.Errorf("a clean pair reported: %v", out)
	}

	src := []byte(`package p
func f() {
	_ = i18n.M("decree.x")
	_ = i18n.Msg{Code: "decree.y"}
	_ = refuse("decree.z")
	_ = other("decree.not_a_maker")
	_ = i18n.M("nonarea.code")
	_ = i18n.M("not a code")
}`)
	out := map[string]string{}
	codesIn(src, "p/p.go", map[string]bool{"decree": true}, out)
	want := map[string]string{"decree.x": "p/p.go", "decree.y": "p/p.go", "decree.z": "p/p.go"}
	if len(out) != len(want) {
		t.Fatalf("declared = %v, want %v", out, want)
	}
	for k, v := range want {
		if out[k] != v {
			t.Errorf("declared = %v, want %v", out, want)
		}
	}
}

var (
	ptBad = regexp.MustCompile(`\b(Workspaces?|workspaces?|Dono)\b`)
	enBad = regexp.MustCompile(`\b(Workspaces?|workspaces?|Owner)\b`)
)

// vocabularyViolations lists the texts (file -> key -> text) that say what the vocabulary
// forbids, naming the file and the key. The one exception: the label of the field of the owner of
// a request that blocks, as the panel allows it too ("Dono: {{owner}}"; in English "Owner: {{owner}}").
func vocabularyViolations(pt, en map[string]map[string]string) []string {
	var out []string
	label := func(text string) bool {
		t := strings.TrimSpace(text)
		return t == "Dono: {{owner}}" || t == "Owner: {{owner}}"
	}
	for f, m := range pt {
		for k, text := range m {
			if label(text) {
				continue
			}
			if loc := ptBad.FindString(placeholder.ReplaceAllString(text, "")); loc != "" {
				out = append(out, fmt.Sprintf("%s: %q says %q in Portuguese (the campaign, the Senhor da Guilda): %q", f, k, loc, text))
			}
		}
	}
	for f, m := range en {
		for k, text := range m {
			if label(text) {
				continue
			}
			if loc := enBad.FindString(placeholder.ReplaceAllString(text, "")); loc != "" {
				out = append(out, fmt.Sprintf("%s: %q says %q in English (the campaign, the Guildmaster): %q", f, k, loc, text))
			}
		}
	}
	sort.Strings(out)
	return out
}

// CA-MSG-11
func TestCatalogsOfTheEngineKeepTheVocabulary(t *testing.T) {
	t.Parallel()
	pt, en := readAll(t)
	for _, v := range vocabularyViolations(pt, en) {
		t.Error(v)
	}
	// the guard catches what it should, naming the file and the key; the label alone is let through,
	// and the same words inside a sentence are not
	got := vocabularyViolations(
		map[string]map[string]string{"x.json": {"x.k": "Dono da credencial", "x.label": "Dono: {{owner}}", "x.inside": "Veja Dono: {{owner}}"}},
		map[string]map[string]string{"y.json": {"y.k": "Owner of it", "y.label": "Owner: {{owner}}", "y.ws": "the workspace"}})
	if len(got) != 4 {
		t.Fatalf("violations = %q, want the 4 bad texts", got)
	}
	for _, want := range []string{`x.json: "x.k"`, `x.json: "x.inside"`, `y.json: "y.k"`, `y.json: "y.ws"`} {
		found := false
		for _, g := range got {
			found = found || strings.Contains(g, want)
		}
		if !found {
			t.Errorf("no violation names %s: %q", want, got)
		}
	}
	if got := Render(PtBR, M("level.owner")); got != "Senhor da Guilda" {
		t.Errorf("level.owner in pt-BR = %q", got)
	}
	if got := Render(En, M("level.owner")); got != "Guildmaster" {
		t.Errorf("level.owner in en = %q", got)
	}
}
