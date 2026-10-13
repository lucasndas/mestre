package glossary_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/glossary"
)

const doc = `# Glossário

## Formato (lido por máquina)

| not | the | table |

## Termos

| Termo | Ajuda curta | Detalhes |
|---|---|---|
| Mesa | Um pedido em andamento. | [mesas](../orquestracao/mesas.md) |
| Torre | Uma máquina da Guilda que não está no comando. | [Guilda](../guilda/guilda.md#formação) |
`

func TestParseReadsTerms(t *testing.T) {
	t.Parallel()
	terms, problems := glossary.Parse(strings.NewReader(doc))
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(terms) != 2 || terms[1].Name != "Torre" || terms[1].Detail != "../guilda/guilda.md#formação" || terms[1].Line != 12 {
		t.Fatalf("terms = %+v", terms)
	}
}

func TestParseReportsFormatProblems(t *testing.T) {
	t.Parallel()
	bad := strings.Replace(doc, "| Torre | Uma máquina da Guilda que não está no comando. | [Guilda](../guilda/guilda.md#formação) |",
		"| torre | "+strings.Repeat("x", 121)+" | https://example.com |\n| Mesa | Veja [aqui](x.md). | [a](a.md) [b](b.md) |\n| A | b |", 1)
	_, problems := glossary.Parse(strings.NewReader(bad))
	want := []string{"capital letter", "121 characters", "exactly one link", "has a link", "repeated", "2 columns"}
	all := ""
	for _, p := range problems {
		all += p.Msg + "\n"
	}
	for _, w := range want {
		if !strings.Contains(all, w) {
			t.Errorf("missing %q in:\n%s", w, all)
		}
	}
}

func TestGenerateIsDeterministicAndKeepsOrder(t *testing.T) {
	t.Parallel()
	terms, problems := glossary.Parse(strings.NewReader(doc))
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	a, err := glossary.Generate(terms)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := glossary.Generate(terms)
	if !bytes.Equal(a, b) {
		t.Fatal("two runs differ")
	}
	got := string(a)
	for _, want := range []string{"DO NOT EDIT", "package glossary", `Name: "Mesa"`, `Detail: "../guilda/guilda.md#formação"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Index(got, `"Mesa"`) > strings.Index(got, `"Torre"`) {
		t.Errorf("terms out of document order:\n%s", got)
	}
}

// The generated help texts are committed; regenerating in memory must give
// the same bytes (docs/desenvolvimento/convencoes.md#código-gerado).
func TestGeneratedFileMatchesTheGlossary(t *testing.T) {
	t.Parallel()
	f, err := os.Open("../../docs/produto/glossario.md")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	terms, problems := glossary.Parse(f)
	if len(problems) != 0 {
		t.Fatalf("glossary problems: %v", problems)
	}
	want, err := glossary.Generate(terms)
	if err != nil {
		t.Fatal(err)
	}
	have, err := os.ReadFile(glossary.GeneratedFile)
	if err != nil {
		t.Fatalf("%v: run `go generate ./internal/glossary`", err)
	}
	// A Windows checkout with autocrlf has CRLF; the content is the same.
	have = bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n"))
	if !bytes.Equal(have, want) {
		t.Fatalf("%s is stale: run `go generate ./internal/glossary` and commit it", glossary.GeneratedFile)
	}
	if len(glossary.Terms) != len(terms) {
		t.Fatalf("Terms has %d entries, the glossary %d", len(glossary.Terms), len(terms))
	}
}
