package glossary_test

import (
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
