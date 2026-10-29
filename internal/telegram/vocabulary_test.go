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

	"github.com/mestre-ia/mestre/internal/i18n"
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

// CA-JORN-13 (the catalogs): the texts the Telegram says come from the catalogs of the engine, in both
// languages, and they keep the vocabulary too: pt-BR never says "Workspace", "Party" or "Dono"; en never says
// "Workspace" or "Owner" (the words are Campanha, Companhia and Senhor da Guilda; Campaign, Party and
// Guildmaster).
func TestNoTextOfTheCatalogsUsesTheOldNames(t *testing.T) {
	t.Parallel()
	bad := map[string]*regexp.Regexp{
		i18n.PtBR: regexp.MustCompile(`\b(Workspaces?|workspaces?|Party|Dono)\b`),
		i18n.En:   regexp.MustCompile(`\b(Workspaces?|workspaces?|Owner)\b`),
	}
	for lang, re := range bad {
		cat := i18n.Catalog(lang)
		if len(cat) < 100 {
			t.Fatalf("only %d texts in %s", len(cat), lang)
		}
		for code, text := range cat {
			if t := strings.TrimSpace(text); t == "Dono: {{owner}}" || t == "Owner: {{owner}}" {
				continue // the label of the field of the owner of a request, allowed as in the panel
			}
			if m := re.FindString(regexp.MustCompile(`{{\s*\w+\s*}}`).ReplaceAllString(text, "")); m != "" {
				t.Errorf("%s: %q says %q in %s", code, text, m, lang)
			}
		}
	}
}
