package telegram_test

import (
	"go/ast"
	"go/parser"
	gotoken "go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// CA-JORN-13: no text the Telegram or the CLI shows a person uses "Workspace", "Party" or "Dono" as a
// name (the words are "Campanha", "Companhia" and "Senhor da Guilda", ADR 0029). The sweep reads the
// string literals of the code of both channels; the technical identifiers (field names, struct tags,
// the identifiers themselves) are not literals shown to anyone, and a literal that is only the
// identifier (a key, a column) is told apart by having no space in it.
func TestNoTextOfTheTelegramOrTheCLIUsesTheOldNames(t *testing.T) {
	t.Parallel()
	old := regexp.MustCompile(`\b(Workspace|Party|Dono)\b`)
	fset := gotoken.NewFileSet()
	var scanned int
	for _, root := range []string{".", filepath.Join("..", "cli")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			scanned++
			tags := map[*ast.BasicLit]bool{}
			ast.Inspect(f, func(n ast.Node) bool {
				if fl, ok := n.(*ast.Field); ok && fl.Tag != nil {
					tags[fl.Tag] = true
				}
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != gotoken.STRING || tags[lit] {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil || !strings.ContainsAny(s, " \n") {
					return true // an identifier, a key or a path, not a sentence
				}
				if m := old.FindString(s); m != "" {
					t.Errorf("%s: the text %q uses %q as a name", fset.Position(lit.Pos()), s, m)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if scanned < 20 {
		t.Fatalf("only %d files were swept", scanned)
	}
}
