package glossary_test

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/glossary"
)

const doc = `# Glossário

## Formato (lido por máquina)

| not | the | table |

## Termos

| Termo | Ajuda curta | No mundo real | Detalhes |
|---|---|---|---|
| Mesa | Um pedido em andamento. | um pedido de trabalho | [mesas](../orquestracao/mesas.md) |
| Torre | Uma máquina da Guilda que não está no comando. | uma máquina de apoio | [Guilda](../guilda/guilda.md#formação) |
`

func TestParseReadsTerms(t *testing.T) {
	t.Parallel()
	terms, problems := glossary.Parse(strings.NewReader(doc))
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	if len(terms) != 2 || terms[1].Name != "Torre" || terms[1].Detail != "../guilda/guilda.md#formação" || terms[1].Line != 12 || terms[1].Real != "uma máquina de apoio" {
		t.Fatalf("terms = %+v", terms)
	}
}

func TestParseReportsFormatProblems(t *testing.T) {
	t.Parallel()
	bad := strings.Replace(doc, "| Torre | Uma máquina da Guilda que não está no comando. | uma máquina de apoio | [Guilda](../guilda/guilda.md#formação) |",
		"| torre | "+strings.Repeat("x", 121)+" | "+strings.Repeat("y", 81)+" | https://example.com |\n| Mesa | Veja [aqui](x.md). | ok | [a](a.md) [b](b.md) |\n| A | b |", 1)
	_, problems := glossary.Parse(strings.NewReader(bad))
	want := []string{"capital letter", "121 characters", "exactly one link", "has a link", "repeated", "2 columns", "81 characters"}
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
	for _, want := range []string{"DO NOT EDIT", "package glossary", `Name: "Mesa"`, `Real: "um pedido de trabalho"`, `Detail: "../guilda/guilda.md#formação"`} {
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
func TestGeneratedFilesMatchTheGlossary(t *testing.T) {
	t.Parallel()
	terms, err := glossary.Load("../../docs/produto/glossario.md", "../../docs/produto/glossario-en.md")
	if err != nil {
		t.Fatal(err)
	}
	want, err := glossary.Generate(terms)
	if err != nil {
		t.Fatal(err)
	}
	wantTS, err := glossary.GenerateTS(terms)
	if err != nil {
		t.Fatal(err)
	}
	for file, w := range map[string][]byte{glossary.GeneratedFile: want, "../../" + glossary.GeneratedTS: wantTS} {
		have, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%v: run `go generate ./internal/glossary`", err)
		}
		// A Windows checkout with autocrlf has CRLF; the content is the same.
		have = bytes.ReplaceAll(have, []byte("\r\n"), []byte("\n"))
		if !bytes.Equal(have, w) {
			t.Fatalf("%s is stale: run `go generate ./internal/glossary` and commit it", file)
		}
	}
	if len(glossary.Terms) != len(terms) {
		t.Fatalf("Terms has %d entries, the glossary %d", len(glossary.Terms), len(terms))
	}
}

func TestEnglishTableMustPairWithTheTerms(t *testing.T) {
	t.Parallel()
	terms, _ := glossary.Parse(strings.NewReader(doc))
	rows, problems := glossary.ParseEnglish(strings.NewReader("## Terms\n\n| Termo | Term | Short help | In the real world |\n|---|---|---|---|\n| Mesa | Table | A request. | a work request |\n"))
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	_, pairing := glossary.Combine(terms, rows)
	if len(pairing) != 1 || !strings.Contains(pairing[0].Msg, `"Torre" has no row`) {
		t.Fatalf("pairing = %v", pairing)
	}
}
