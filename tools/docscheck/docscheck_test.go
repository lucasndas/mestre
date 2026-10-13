package main

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

var today = time.Date(2026, 10, 12, 15, 0, 0, 0, time.UTC)

func hdr(title string) string {
	return "# " + title + "\n\n> **Assunto:** teste · **Componente:** Projeto · **Atualizado:** 2026-10-12\n\n"
}

// tree is a small valid documentation tree in memory.
type tree struct {
	fs fstest.MapFS
	// audit is the date column of the table of milestones, by milestone;
	// extra is text added after that table (the lists of criteria).
	audit map[int]string
	extra string
}

func newTree() *tree {
	return &tree{fs: fstest.MapFS{
		"docs/INDEX.md": {Data: []byte(hdr("Docs") + "Comece aqui.\n\n| Área | O que cobre | Quando ler |\n|---|---|---|\n")},
	}}
}

func (t *tree) appendTo(p, s string) {
	f := t.fs[p]
	f.Data = append(f.Data, s...)
	t.fs[p] = f
}

// area adds docs/<name>/INDEX.md and lists it in the root index.
func (t *tree) area(name string) *tree {
	t.fs["docs/"+name+"/INDEX.md"] = &fstest.MapFile{Data: []byte(hdr(name) + "| Documento | O que cobre | Quando ler |\n|---|---|---|\n")}
	t.appendTo("docs/INDEX.md", fmt.Sprintf("| [%s](%s/INDEX.md) | x | y |\n", name, name))
	return t
}

// add writes a document and lists it in its folder index.
func (t *tree) add(p, content string) *tree {
	t.fs[p] = &fstest.MapFile{Data: []byte(content)}
	t.appendTo(path.Dir(p)+"/INDEX.md", fmt.Sprintf("| [%s](%s) | x | y |\n", path.Base(p), path.Base(p)))
	return t
}

func (t *tree) set(p, content string) *tree {
	t.fs[p] = &fstest.MapFile{Data: []byte(content)}
	return t
}

func feature(title, sigla string, criteria []string, deps ...string) string {
	var b strings.Builder
	b.WriteString(hdr(title))
	b.WriteString("## Objetivo\n\nx\n\n## Comportamento\n\nx\n\n## Regras e erros\n\nx\n\n## Critérios de aceite\n\n")
	if criteria == nil {
		criteria = []string{crit(sigla, "01", "rápida")}
	}
	for _, c := range criteria {
		b.WriteString(c + "\n")
	}
	b.WriteString("\n## Depende de\n\n")
	for _, d := range deps {
		b.WriteString("- [" + path.Base(d) + "](" + d + ")\n")
	}
	return b.String()
}

func crit(sigla, nn, suite string) string {
	return fmt.Sprintf("- **CA-%s-%s** · *%s* — **Dado** a, **quando** b, **então** c.", sigla, nn, suite)
}

// withOrder adds the implementation order with the given map rows.
func (t *tree) withOrder(rows ...string) *tree {
	if _, ok := t.fs["docs/desenvolvimento/INDEX.md"]; !ok {
		t.area("desenvolvimento")
	}
	body := hdr("Ordem") + t.milestones() + "## Mapa de documentos por marco e etapa\n\n| Marco | Etapa | Documentos |\n|---|---|---|\n" + strings.Join(rows, "\n") + "\n"
	if _, ok := t.fs[orderPath]; ok {
		return t.set(orderPath, body)
	}
	return t.add(orderPath, body)
}

// milestones is the table of milestones M0 to M7 with the audit column.
func (t *tree) milestones() string {
	var b strings.Builder
	b.WriteString("## Marcos\n\n| Marco | Nome | Auditoria | O que fica pronto ao final |\n|---|---|---|---|\n")
	for n := 0; n <= 7; n++ {
		fmt.Fprintf(&b, "| M%d | nome | %s | x |\n", n, t.audit[n])
	}
	return b.String() + "\n" + t.extra + "\n"
}

func run1(t *testing.T, tr *tree) Result {
	t.Helper()
	return Check(tr.fs, today)
}

// only returns the errors of one check, as strings.
func only(res Result, check string) []string {
	var out []string
	for _, e := range res.Errors {
		if e.Check == check {
			out = append(out, e.String())
		}
	}
	return out
}

func mustClean(t *testing.T, res Result) {
	t.Helper()
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected errors:\n%s", joinIssues(res.Errors))
	}
}

func joinIssues(is []Issue) string {
	var b strings.Builder
	for _, i := range is {
		b.WriteString(i.String() + "\n")
	}
	return b.String()
}

func TestValidTreeIsClean(t *testing.T) {
	t.Parallel()
	tr := newTree().area("a").add("docs/a/x.md", feature("X", "X", nil))
	tr.withOrder("| M0 | 1 | [x](../a/x.md) |")
	res := run1(t, tr)
	mustClean(t, res)
	if len(res.Warnings) != 0 {
		t.Fatalf("warnings: %v", res.Warnings)
	}
}

// CA-VERDOCS-01
func TestDocumentMissingFromIndex(t *testing.T) {
	t.Parallel()
	tr := newTree().area("a").set("docs/a/novo.md", hdr("Novo"))
	got := only(run1(t, tr), ckIndex)
	if len(got) != 1 || !strings.HasPrefix(got[0], "docs/a/novo.md:1: índice: docs/a/novo.md não aparece em docs/a/INDEX.md") {
		t.Fatalf("errors = %q", got)
	}
}

// CA-VERDOCS-02
func TestIndexItemThatDoesNotExist(t *testing.T) {
	t.Parallel()
	tr := newTree().area("a")
	tr.appendTo("docs/a/INDEX.md", "| [sumiu.md](sumiu.md) | x | y |\n")
	res := run1(t, tr)
	got := only(res, ckIndexItem)
	if len(got) != 1 || got[0] != "docs/a/INDEX.md:7: item de índice: sumiu.md não existe" {
		t.Fatalf("errors = %q\nall:\n%s", got, joinIssues(res.Errors))
	}
	if len(res.Errors) != 1 {
		t.Fatalf("reported more than once:\n%s", joinIssues(res.Errors))
	}
}

// CA-VERDOCS-03
func TestBrokenAnchorAndLinkInsideCode(t *testing.T) {
	t.Parallel()
	tr := newTree().area("a").area("b").
		add("docs/b/x.md", hdr("X")+"## Parte um\n").
		add("docs/a/y.md", hdr("Y")+"Veja [x](../b/x.md#parte-dois).\n")
	got := only(run1(t, tr), ckLink)
	if len(got) != 1 || !strings.HasPrefix(got[0], "docs/a/y.md:5: link: ../b/x.md#parte-dois") {
		t.Fatalf("errors = %q", got)
	}

	tr = newTree().area("a").area("b").
		add("docs/b/x.md", hdr("X")+"## Parte um\n").
		add("docs/a/y.md", hdr("Y")+"```\nVeja [x](../b/x.md#parte-dois).\n```\nE `[x](../b/x.md#parte-dois)`.\n")
	mustClean(t, run1(t, tr))
}

// CA-VERDOCS-04
func TestSizeWarningAndError(t *testing.T) {
	t.Parallel()
	lines := func(n int) string {
		s := hdr("T")
		for i := strings.Count(s, "\n"); i < n; i++ {
			s += "linha\n"
		}
		return s
	}
	tr := newTree().area("a").add("docs/a/medio.md", lines(151)).add("docs/a/grande.md", lines(201))
	res := run1(t, tr)
	if got := only(res, ckSize); len(got) != 1 || !strings.HasPrefix(got[0], "docs/a/grande.md:201: tamanho: 201 linhas") {
		t.Fatalf("errors = %q", got)
	}
	if len(res.Warnings) != 1 || !strings.HasPrefix(res.Warnings[0].String(), "aviso: docs/a/medio.md:151: tamanho: 151 linhas") {
		t.Fatalf("warnings = %v", res.Warnings)
	}
}

// CA-VERDOCS-05
func TestHeaderComponentAndFutureDate(t *testing.T) {
	t.Parallel()
	tr := newTree().area("a").
		add("docs/a/srv.md", "# S\n\n> **Assunto:** x · **Componente:** Servidor · **Atualizado:** 2026-10-12\n").
		add("docs/a/fut.md", "# F\n\n> **Assunto:** x · **Componente:** Motor · **Atualizado:** 2026-10-13\n").
		add("docs/a/core.md", "# C\n\n> **Assunto:** x · **Componente:** Core · **Atualizado:** 2026-10-12\n").
		add("docs/a/node.md", "# N\n\n> **Assunto:** x · **Componente:** Node · **Atualizado:** 2026-10-12\n")
	got := only(run1(t, tr), ckHeader)
	if len(got) != 4 || !strings.HasPrefix(got[0], `docs/a/core.md:3: cabeçalho: componente "Core" inválido`) ||
		!strings.HasPrefix(got[1], "docs/a/fut.md:3: cabeçalho: data 2026-10-13 está no futuro") ||
		!strings.HasPrefix(got[2], `docs/a/node.md:3: cabeçalho: componente "Node" inválido`) ||
		!strings.HasPrefix(got[3], `docs/a/srv.md:3: cabeçalho: componente "Servidor" inválido`) {
		t.Fatalf("errors = %q", got)
	}
}

// Header dates are UTC days, whatever the local zone of the clock.
func TestHeaderDateIsComparedInUTC(t *testing.T) {
	t.Parallel()
	saoPaulo := time.FixedZone("BRT", -3*60*60)
	tr := newTree().area("a").
		add("docs/a/hoje.md", "# H\n\n> **Assunto:** x · **Componente:** Motor · **Atualizado:** 2026-10-13\n").
		add("docs/a/amanha.md", "# A\n\n> **Assunto:** x · **Componente:** Motor · **Atualizado:** 2026-10-14\n")
	// 22:00 on Oct 12 in São Paulo is 01:00 on Oct 13 in UTC.
	evening := time.Date(2026, 10, 12, 22, 0, 0, 0, saoPaulo)
	got := only(Check(tr.fs, evening), ckHeader)
	if len(got) != 1 || !strings.HasPrefix(got[0], "docs/a/amanha.md:3: cabeçalho: data 2026-10-14 está no futuro (hoje é 2026-10-13 em UTC)") {
		t.Fatalf("errors = %q", got)
	}
}

// CA-VERDOCS-06
func TestSectionOrderAndIncompleteVerifyItem(t *testing.T) {
	t.Parallel()
	doc := hdr("F") + "## Objetivo\n\nx\n\n## Regras e erros\n\nx\n\n## Comportamento\n\nx\n\n## Critérios de aceite\n\n" +
		crit("F", "01", "rápida") + "\n\n## Depende de\n\n## Verificar na implementação\n\n" +
		"- **Verificar na implementação:** algo. **Padrão:** a. **Se falhar:** b.\n"
	tr := newTree().area("a").add("docs/a/f.md", doc)
	res := run1(t, tr)
	sec, ver := only(res, ckSections), only(res, ckVerify)
	if len(sec) != 1 || !strings.Contains(sec[0], "docs/a/f.md:9: seções:") ||
		!strings.Contains(sec[0], "Objetivo, Comportamento, Regras e erros, Critérios de aceite, Depende de") {
		t.Fatalf("section errors = %q", sec)
	}
	if len(ver) != 1 || ver[0] != "docs/a/f.md:25: verificar na implementação: item incompleto, falta **Onde:**" {
		t.Fatalf("verify errors = %q", ver)
	}
}

// CA-VERDOCS-07
func TestCriteriaErrorsAllAtOnce(t *testing.T) {
	t.Parallel()
	tr := newTree().area("a").
		add("docs/a/a1.md", feature("Um", "UM", nil)).
		add("docs/a/b2.md", feature("Dois", "UM", []string{
			crit("UM", "02", "rápida"),
			"- **CA-UM-03** — **Dado** a, **quando** b, **então** c.",
			crit("OUTRA", "04", "rápida"),
		}))
	got := only(run1(t, tr), ckCriteria)
	want := []string{
		"docs/a/b2.md:19: critérios: sigla UM já é usada em docs/a/a1.md",
		"docs/a/b2.md:20: critérios: CA-UM-03 sem a suíte",
		"docs/a/b2.md:21: critérios: sigla OUTRA diferente de UM",
	}
	if len(got) != len(want) {
		t.Fatalf("errors = %q", got)
	}
	for i := range want {
		if !strings.HasPrefix(got[i], want[i]) {
			t.Fatalf("error %d = %q, want prefix %q", i, got[i], want[i])
		}
	}
}

// CA-VERDOCS-08
func TestHeavyCriterionNeedsMVP(t *testing.T) {
	t.Parallel()
	bad := newTree().area("a").add("docs/a/p.md", feature("P", "P", []string{crit("P", "01", "pesada")}))
	if got := only(run1(t, bad), ckCriteria); len(got) != 1 || !strings.Contains(got[0], "não cita um critério global") {
		t.Fatalf("errors = %q", got)
	}
	ok := "- **CA-P-01** · *pesada* — **Dado** a, **quando** b, **então** c (MVP-4)."
	mustClean(t, run1(t, newTree().area("a").add("docs/a/p.md", feature("P", "P", []string{ok}))))
	last := "- **CA-P-01** · *pesada* — **Dado** a, **quando** b, **então** c (MVP-13, MVP-15)."
	mustClean(t, run1(t, newTree().area("a").add("docs/a/p.md", feature("P", "P", []string{last}))))
	beyond := "- **CA-P-01** · *pesada* — **Dado** a, **quando** b, **então** c (MVP-16)."
	if got := only(run1(t, newTree().area("a").add("docs/a/p.md", feature("P", "P", []string{beyond}))), ckCriteria); len(got) != 1 {
		t.Fatalf("MVP-16 must be rejected, errors = %q", got)
	}
}

// CA-VERDOCS-09
func TestReportAndAccentedNames(t *testing.T) {
	t.Parallel()
	tr := newTree().area("a").add("docs/a/PROGRESS.md", hdr("P")).add("docs/a/configuração.md", hdr("C"))
	got := strings.Join(only(run1(t, tr), ckName), "\n")
	for _, want := range []string{"docs/a/PROGRESS.md:1: nome: nome de relatório", "docs/a/PROGRESS.md:1: nome: arquivo fora do padrão", "docs/a/configuração.md:1: nome: arquivo fora do padrão"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in:\n%s", want, got)
		}
	}
}

// Folders the go tool ignores (.claude/worktrees with agent checkouts) are not
// part of the repository tree.
func TestNamesSkipDotAndUnderscoreFolders(t *testing.T) {
	t.Parallel()
	tr := newTree().set(".claude/worktrees/a/SUMMARY.md", "x").set("_old/PROGRESS.md", "x").set("src/SUMMARY.md", "x")
	got := strings.Join(only(run1(t, tr), ckName), "\n")
	if !strings.Contains(got, "src/SUMMARY.md") || strings.Contains(got, ".claude") || strings.Contains(got, "_old") {
		t.Fatalf("names = \n%s", got)
	}
}

// CA-VERDOCS-10
//
// The budget is on the check itself, not on the machine's load: the test runs
// in parallel with the rest of the suite, so it times up to three runs and
// takes the fastest. A check that is really slow is slow in all three.
func TestDocs(t *testing.T) {
	t.Parallel()
	const runs = 3
	wall := clock.Real{}
	best := time.Duration(math.MaxInt64)
	var out bytes.Buffer
	for range runs {
		out.Reset()
		start := wall.Now()
		code := run(os.DirFS("../.."), wall, &out)
		best = min(best, clock.Since(wall, start))
		if code != 0 {
			t.Fatalf("documentation check failed:\n%s", out.String())
		}
		if best < time.Second {
			break // within the budget: no need to measure again
		}
	}
	if best >= time.Second {
		t.Fatalf("documentation check took %v at its fastest of %d runs, budget 1s", best, runs)
	}
	t.Log(strings.TrimSpace(out.String()))
}

// CA-VERDOCS-11
func TestDependencyCycle(t *testing.T) {
	t.Parallel()
	tr := newTree().area("m").
		add("docs/m/a.md", feature("A", "A", nil, "b.md")).
		add("docs/m/b.md", feature("B", "B", nil, "a.md")).
		withOrder("| M3 | 1 | [a](../m/a.md) · [b](../m/b.md) |")
	got := strings.Join(only(run1(t, tr), ckOrder), "\n")
	if !strings.Contains(got, "ciclo em \"Depende de\": m/a.md → m/b.md → m/a.md") {
		t.Fatalf("errors:\n%s", got)
	}
}

// CA-VERDOCS-12
func TestLaterMilestoneAndDocumentOutsideMap(t *testing.T) {
	t.Parallel()
	build := func(dep string) *tree {
		return newTree().area("m").area("arquitetura").
			add("docs/arquitetura/seguranca.md", hdr("Segurança")).
			add("docs/m/m3.md", feature("M3", "TRES", nil, dep)).
			add("docs/m/m5.md", feature("M5", "CINCO", nil)).
			withOrder("| M3 | 1 | [m3](../m/m3.md) |", "| M5 | 1 | [m5](../m/m5.md) |")
	}
	tr := build("m5.md").add("docs/m/solto.md", feature("Solto", "SOLTO", nil))
	got := only(run1(t, tr), ckOrder)
	if len(got) != 2 || !strings.HasPrefix(got[0], "docs/m/m3.md:23: ordem: m/m3.md (M3, etapa 1) depende de m/m5.md (M5, etapa 1), de marco posterior") ||
		!strings.HasPrefix(got[1], "docs/m/solto.md:1: ordem: documento de funcionalidade fora do mapa") {
		t.Fatalf("errors = %q", got)
	}
	mustClean(t, run1(t, build("../arquitetura/seguranca.md")))
}

// CA-VERDOCS-13
func TestStageMustFollowDependencies(t *testing.T) {
	t.Parallel()
	build := func(stage int) *tree {
		return newTree().area("m").
			add("docs/m/mesa.md", feature("Mesa", "MESA", nil, "fichas.md")).
			add("docs/m/fichas.md", feature("Fichas", "FICHAS", nil)).
			withOrder(fmt.Sprintf("| M4 | %d | [mesa](../m/mesa.md) |", stage), "| M4 | 5 | [fichas](../m/fichas.md) |")
	}
	got := only(run1(t, build(3)), ckOrder)
	if len(got) != 1 || !strings.Contains(got[0], "m/mesa.md (M4, etapa 3) depende de m/fichas.md (M4, etapa 5)") {
		t.Fatalf("errors = %q", got)
	}
	mustClean(t, run1(t, build(6)))
}

func TestSlugFollowsGitHub(t *testing.T) {
	t.Parallel()
	d := parseDoc("x.md", []byte("# T\n## Modo máquina única\n## Telegram e GitHub na pesada\n## `testrun` (executor)\n## Regras\n## Regras\n## [Link](a.md) e mais\n"))
	for _, a := range []string{"modo-máquina-única", "telegram-e-github-na-pesada", "testrun-executor", "regras", "regras-1", "link-e-mais"} {
		if !d.anchorSet()[a] {
			t.Errorf("anchor %q missing in %v", a, d.anchorSet())
		}
	}
}

func TestADRSectionsAndIndexBody(t *testing.T) {
	t.Parallel()
	tr := newTree().area("decisoes").
		add("docs/decisoes/0001-x.md", hdr("ADR")+"## Status\n\n## Contexto\n\n## Decisão\n").
		add("docs/decisoes/sem-numero.md", hdr("ADR")+"## Status\n\n## Contexto\n\n## Opções consideradas\n\n## Decisão\n\n## Consequências\n\n## Fontes\n")
	tr.appendTo("docs/decisoes/INDEX.md", "\nTexto solto.\n")
	res := run1(t, tr)
	sec := strings.Join(only(res, ckSections), "\n")
	if !strings.Contains(sec, "docs/decisoes/0001-x.md:9: seções: ADR") || !strings.Contains(sec, "docs/decisoes/INDEX.md") {
		t.Fatalf("section errors:\n%s", sec)
	}
	if got := only(res, ckName); len(got) != 1 || !strings.Contains(got[0], "ADR começa com quatro dígitos") {
		t.Fatalf("name errors = %q", got)
	}
}

func TestGlossaryFormatIsChecked(t *testing.T) {
	t.Parallel()
	tr := newTree().area("produto").add("docs/produto/glossario.md", hdr("Glossário")+
		"## Termos\n\n| Termo | Ajuda curta | Detalhes |\n|---|---|---|\n| mesa | ok. | [x](glossario.md) |\n")
	got := only(run1(t, tr), ckGlossary)
	if len(got) != 1 || !strings.Contains(got[0], "docs/produto/glossario.md:9: glossário: term \"mesa\" must start with a capital letter") {
		t.Fatalf("errors = %q", got)
	}
}

func TestMainOutputFormat(t *testing.T) {
	t.Parallel()
	tr := newTree().area("a").set("docs/a/novo.md", hdr("Novo"))
	var out bytes.Buffer
	code := run(tr.fs, clock.NewFake(today), &out)
	if code != 1 || !strings.Contains(out.String(), "docs/a/novo.md:1: índice:") || !strings.Contains(out.String(), "docscheck: 1 erro(s), 0 aviso(s)") {
		t.Fatalf("code %d, output:\n%s", code, out.String())
	}
}
