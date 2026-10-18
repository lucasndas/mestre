package main

import (
	"fmt"
	"strings"
	"testing"
)

const audited = "2026-10-01" // before today in docscheck_test.go

// gate builds a tree with a M1 document (CA-A-01 and CA-A-03 fast, CA-A-02
// heavy) and a M2 document (CA-B-01), the audit dates, text after the table
// of milestones and Go files by path.
func gate(audit map[int]string, extra string, goFiles map[string]string) *tree {
	tr := newTree().area("a")
	tr.add("docs/a/m1.md", feature("M1", "A", []string{
		crit("A", "01", "rápida"),
		"- **CA-A-02** · *pesada* — **Dado** a, **quando** b, **então** c (MVP-4).",
		crit("A", "03", "rápida"),
	}))
	tr.add("docs/a/m2.md", feature("M2", "B", nil))
	tr.audit, tr.extra = map[int]string{0: audited}, extra // M0 is behind us
	for n, d := range audit {
		tr.audit[n] = d
	}
	tr.withOrder("| M1 | 1 | [m1](../a/m1.md) |", "| M2 | 1 | [m2](../a/m2.md) |")
	for p, src := range goFiles {
		tr.set(p, src)
	}
	return tr
}

func testFile(markers ...string) map[string]string {
	src := "package x\n\nimport \"testing\"\n"
	for i, m := range markers {
		src += fmt.Sprintf("\n// %s\nfunc TestC%d(t *testing.T) {}\n", m, i)
	}
	return map[string]string{"internal/x/x_test.go": src}
}

const deferredList = "### Critérios em marcos posteriores\n\n| Documento | Critério → marco em que fecha |\n|---|---|\n| [m1](../a/m1.md) | CA-A-01 a CA-A-03 → M5 |\n"

// CA-VERDOCS-14
func TestAuditDatesGateTheNextMilestone(t *testing.T) {
	t.Parallel()
	cases := map[string]struct {
		audit map[int]string
		extra string
		files map[string]string
		want  []string // pieces of the audit errors
		n     int      // number of audit errors
	}{
		"code of M2 and M1 without a date": {
			files: testFile("CA-B-01"),
			want:  []string{"docs/desenvolvimento/ordem-de-implementacao.md:10: auditoria: M1 não tem data de auditoria e há código do M2 (internal/x/x_test.go:5, CA-B-01)"},
			n:     1,
		},
		"M1 audited": {
			audit: map[int]string{1: audited},
			files: testFile("CA-B-01", "CA-A-01", "CA-A-03"),
		},
		"no code of M2 yet": {
			files: testFile("CA-A-01"),
		},
		"M2 audited before M1": {
			audit: map[int]string{2: audited},
			files: testFile("CA-B-01"),
			want:  []string{"M2 tem auditoria e M1 não", "M1 não tem data de auditoria e há código do M2"},
			n:     2,
		},
		"future date": {
			audit: map[int]string{1: "2026-10-13"},
			files: testFile("CA-A-01", "CA-A-03"),
			want:  []string{"M1: auditoria 2026-10-13 está no futuro"},
			n:     1,
		},
		"malformed date": {
			audit: map[int]string{1: "13/10/2026"},
			files: testFile("CA-A-01", "CA-A-03"),
			want:  []string{`M1: auditoria "13/10/2026" inválida`},
			n:     1,
		},
		"early criterion of M2 is not code of M2": {
			extra: "### Critérios antecipados\n\nCA-B-01 já testado.\n",
			files: testFile("CA-B-01"),
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := only(run1(t, gate(c.audit, c.extra, c.files)), ckAudit)
			if len(got) != c.n {
				t.Fatalf("errors = %q, want %d", got, c.n)
			}
			for _, w := range c.want {
				if !strings.Contains(strings.Join(got, "\n"), w) {
					t.Fatalf("errors %q lack %q", got, w)
				}
			}
		})
	}
}

// plusGate builds a tree with a M4 document (CA-D-01) and a M5 document
// (CA-E-01), M0 to M3 audited, the row M4+ and the audit of M4 and M5 with the
// given dates, and Go files by path.
func plusGate(m4, plus, m5 string, goFiles map[string]string) *tree {
	tr := newTree().area("a")
	tr.add("docs/a/m4.md", feature("M4", "D", nil))
	tr.add("docs/a/m5.md", feature("M5", "E", nil))
	tr.audit = map[int]string{0: audited, 1: audited, 2: audited, 3: audited, 4: m4, 5: m5}
	tr.plus = &plus
	tr.withOrder("| M4 | 1 | [m4](../a/m4.md) |", "| M5 | 1 | [m5](../a/m5.md) |")
	for p, src := range goFiles {
		tr.set(p, src)
	}
	return tr
}

// CA-VERDOCS-19
func TestM4PlusSitsBetweenM4AndM5(t *testing.T) {
	t.Parallel()
	const ordem = "docs/desenvolvimento/ordem-de-implementacao.md"
	until := func(m string) map[string]string {
		return map[string]string{"internal/x/a.go": "package x\n\n// Until " + m + " it connects by pipes.\nfunc F() {}\n"}
	}
	cases := map[string]struct {
		m4, plus, m5 string
		files        map[string]string
		audit        []string // the audit errors, in order
		stale        []string // the stale comment errors, in order
	}{
		"code of M5 and M4+ without a date": {
			m4:    audited,
			files: testFile("CA-E-01"),
			audit: []string{ordem + ":14: auditoria: M4+ não tem data de auditoria e há código do M5 (internal/x/x_test.go:5, CA-E-01): audite o M4+ e registre a data antes"},
		},
		"M4+ audited, code of M5 allowed":  {m4: audited, plus: audited, files: testFile("CA-E-01")},
		"code of M4 does not wait for M4+": {m4: audited, files: testFile("CA-D-01")},
		"M5 audited and M4+ without a date": {
			m4: audited, m5: audited,
			audit: []string{ordem + ":15: auditoria: M5 tem auditoria e M4+ não: os marcos são auditados em ordem"},
		},
		"M4+ audited and M4 without a date": {
			plus:  audited,
			audit: []string{ordem + ":14: auditoria: M4+ tem auditoria e M4 não: os marcos são auditados em ordem"},
		},
		"M4+ and M5 audited": {m4: audited, plus: audited, m5: audited},
		"future date":        {m4: audited, plus: "2026-10-13", audit: []string{"M4+: auditoria 2026-10-13 está no futuro (hoje é 2026-10-12 em UTC)"}},
		"malformed date":     {m4: audited, plus: "13/10/2026", audit: []string{`M4+: auditoria "13/10/2026" inválida`}},
		"comment that waits for M4+ before its audit": {m4: audited, files: until("M4+")},
		"comment that waits for M4+ after its audit": {
			m4: audited, plus: audited, files: until("M4+"),
			stale: []string{`internal/x/a.go:3: comentário velho: "Until M4+": o M4+ já foi auditado (2026-10-01)`},
		},
		"comment that waits for M4 is still stale": {
			m4: audited, files: until("M4"),
			stale: []string{`internal/x/a.go:3: comentário velho: "Until M4": o M4 já foi auditado (2026-10-01)`},
		},
		"comment that waits for M5": {m4: audited, plus: audited, files: until("M5")},
		"a plus without its row is the milestone, as before": {
			m4: audited, files: until("M3+"),
			stale: []string{`internal/x/a.go:3: comentário velho: "Until M3": o M3 já foi auditado (2026-10-01)`},
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			res := run1(t, plusGate(c.m4, c.plus, c.m5, c.files))
			for check, want := range map[string][]string{ckAudit: c.audit, ckStale: c.stale} {
				got := only(res, check)
				if len(got) != len(want) {
					t.Fatalf("%s errors = %q, want %q", check, got, want)
				}
				for i, w := range want {
					if !strings.Contains(got[i], w) {
						t.Fatalf("%s error %d = %q, want %q", check, i, got[i], w)
					}
				}
			}
		})
	}
}

func TestM4PlusRowNeedsTheAuditCellToo(t *testing.T) {
	t.Parallel()
	tr := plusGate(audited, "", "", nil)
	order := string(tr.fs[orderPath].Data)
	tr.set(orderPath, strings.Replace(order, "| M4+ | nome |  | x |", "| M4+ | nome |", 1))
	got := only(run1(t, tr), ckAudit)
	if len(got) != 1 || !strings.Contains(got[0], `ordem-de-implementacao.md:14: auditoria: linha do M4+ sem a coluna "Auditoria"`) {
		t.Fatalf("errors = %q", got)
	}
}

// M4+ has no documents: a row of the map with it is not read, so the document
// listed there is outside the map.
func TestM4PlusHasNoDocumentsInTheMap(t *testing.T) {
	t.Parallel()
	plus := audited
	tr := newTree().area("a").add("docs/a/m4.md", feature("M4", "D", nil))
	tr.audit, tr.plus = map[int]string{0: audited, 1: audited, 2: audited, 3: audited, 4: audited}, &plus
	tr.withOrder("| M4+ | 1 | [m4](../a/m4.md) |")
	got := only(run1(t, tr), ckOrder)
	if len(got) != 1 || !strings.Contains(got[0], "docs/a/m4.md:1: ordem: documento de funcionalidade fora do mapa") {
		t.Fatalf("errors = %q", got)
	}
}

func TestTableOfMilestonesNeedsTheAuditColumn(t *testing.T) {
	t.Parallel()
	tr := gate(nil, "", nil)
	tr.set(orderPath, hdr("Ordem")+"## Marcos\n\n| Marco | Nome |\n|---|---|\n| M1 | x |\n\n## Mapa de documentos por marco e etapa\n\n| Marco | Etapa | Documentos |\n|---|---|---|\n| M1 | 1 | [m1](../a/m1.md) |\n| M2 | 1 | [m2](../a/m2.md) |\n")
	got := only(run1(t, tr), ckAudit)
	if len(got) != 1 || !strings.Contains(got[0], `ordem-de-implementacao.md:7: auditoria: a tabela de marcos precisa da coluna "Auditoria"`) {
		t.Fatalf("errors = %q", got)
	}
}

// CA-VERDOCS-15
func TestAuditedMilestoneNeedsATestForEveryCriterion(t *testing.T) {
	t.Parallel()
	closed := map[int]string{1: audited}
	cases := map[string]struct {
		audit map[int]string
		extra string
		files map[string]string
		want  []string // identifiers reported
	}{
		"no markers":                    {audit: closed, want: []string{"CA-A-01", "CA-A-03"}},
		"one marker":                    {audit: closed, files: testFile("CA-A-01"), want: []string{"CA-A-03"}},
		"all markers":                   {audit: closed, files: testFile("CA-A-01", "CA-A-03")},
		"a marker outside a _test.go":   {audit: closed, files: map[string]string{"internal/x/x.go": "package x\n\n// CA-A-01\n// CA-A-03\nfunc F() {}\n"}, want: []string{"CA-A-01", "CA-A-03"}},
		"milestone without a date":      {files: nil},
		"listed as coming later":        {audit: closed, extra: deferredList},
		"a range counts for every item": {audit: closed, extra: deferredList},
		"listed under another heading": {audit: closed, extra: "### Outra lista\n\n| [m1](../a/m1.md) | CA-A-01 a CA-A-03 |\n",
			want: []string{"CA-A-01", "CA-A-03"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			got := only(run1(t, gate(c.audit, c.extra, c.files)), ckCoverage)
			if len(got) != len(c.want) {
				t.Fatalf("errors = %q, want %v", got, c.want)
			}
			for i, id := range c.want {
				if !strings.HasPrefix(got[i], "docs/a/m1.md:") || !strings.Contains(got[i], id+" (M1, *rápida*) não tem teste marcado") {
					t.Fatalf("error %d = %q, want %s", i, got[i], id)
				}
			}
		})
	}
}

func TestCoverageErrorPointsToTheCriterionLine(t *testing.T) {
	t.Parallel()
	tr := gate(map[int]string{1: audited}, "", testFile("CA-A-03"))
	got := only(run1(t, tr), ckCoverage)
	// 4 lines of header and 3 sections of 4 lines, then the heading of the
	// criteria and a blank: the first criterion is on line 19.
	if len(got) != 1 || !strings.HasPrefix(got[0], "docs/a/m1.md:19: cobertura: CA-A-01") {
		t.Fatalf("errors = %q", got)
	}
}

// CA-VERDOCS-16
func TestMarkerNamesAnExistingCriterion(t *testing.T) {
	t.Parallel()
	files := map[string]string{
		"internal/x/x_test.go": "package x\n\n// CA-ZZZ-99\nfunc TestA() {}\n\nvar s = \"// CA-YYY-98\"\n\n// CA-A-01 is real\nfunc TestB() {}\n",
		"internal/y/y.go":      "package y\n\n/* CA-XXX-97 */\n\n// not a marker CA-WWW-96\nfunc F() {}\n",
	}
	got := only(run1(t, gate(nil, "", files)), ckCoverage)
	if len(got) != 1 || !strings.HasPrefix(got[0], "internal/x/x_test.go:3: cobertura: o marcador CA-ZZZ-99 não nomeia nenhum critério") {
		t.Fatalf("errors = %q", got)
	}
}

// CA-VERDOCS-17
func TestStaleMilestoneComments(t *testing.T) {
	t.Parallel()
	spikes := "### Spikes do M1\n\n| Spike | Resultado |\n|---|---|\n| `mestre-pty` no Windows (spike 5): **feito** (2026-10-13) | passou |\n| outro (spike 6) | a fazer |\n"
	cases := map[string]struct {
		file, src string
		wantLine  int // 0: no error
	}{
		"arrives":               {"internal/x/a.go", "package x\n\n// The queue arrives in M1.\nfunc F() {}\n", 3},
		"arrives later word":    {"internal/x/a.go", "package x\n\n// photos and files arrive with the bot in M1.\nfunc F() {}\n", 3},
		"wrapped phrase":        {"internal/x/a.go", "package x\n\n// the poller does\n// not exist yet: it arrives\n// in M1 as a whole\nfunc F() {}\n", 4},
		"until":                 {"internal/x/a.go", "package x\n\n// Until M1 it connects by pipes.\nfunc F() {}\n", 3},
		"waits":                 {"internal/x/a.go", "package x\n\n// waits for M1\nfunc F() {}\n", 3},
		"pending":               {"internal/x/a.go", "package x\n\n// pending M1\nfunc F() {}\n", 3},
		"portuguese chega":      {"internal/x/a.go", "package x\n\n// A fila chega no M1.\nfunc F() {}\n", 3},
		"portuguese fica":       {"internal/x/a.go", "package x\n\n// Fica para o M1.\nfunc F() {}\n", 3},
		"portuguese até":        {"internal/x/a.go", "package x\n\n// Vale até o M1.\nfunc F() {}\n", 3},
		"block comment":         {"internal/x/a.go", "package x\n\n/*\n until M1 */\nfunc F() {}\n", 4},
		"test file":             {"internal/x/a_test.go", "package x\n\n// arrives in M1\nfunc TestA() {}\n", 3},
		"spike done":            {"internal/x/a.go", "package x\n\n// waits for spike 5 to be done\nfunc F() {}\n", 3},
		"spike open":            {"internal/x/a.go", "package x\n\n// waits for spike 6\nfunc F() {}\n", 0},
		"milestone not audited": {"internal/x/a.go", "package x\n\n// arrives in M2\nfunc F() {}\n", 0},
		"past tense":            {"internal/x/a.go", "package x\n\n// Done in M1; the queue exists.\nfunc F() {}\n", 0},
		"sentence apart":        {"internal/x/a.go", "package x\n\n// It arrives. M1 has more.\nfunc F() {}\n", 0},
		"lower case":            {"internal/x/a.go", "package x\n\n// arrives in m1\nfunc F() {}\n", 0},
		"in a string":           {"internal/x/a.go", "package x\n\nvar s = \"arrives in M1\"\n", 0},
		"spike reference":       {"tools/spikes/linux/a/main.go", "package main\n\n// arrives in M1\nfunc main() {}\n", 0},
		"testdata":              {"internal/x/testdata/a.go", "package x\n\n// arrives in M1\n", 0},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := gate(map[int]string{1: audited}, spikes, map[string]string{c.file: c.src})
			got := only(run1(t, tr), ckStale)
			if c.wantLine == 0 {
				if len(got) != 0 {
					t.Fatalf("errors = %q", got)
				}
				return
			}
			if want := fmt.Sprintf("%s:%d: comentário velho: ", c.file, c.wantLine); len(got) != 1 || !strings.HasPrefix(got[0], want) {
				t.Fatalf("errors = %q, want one at %q", got, want)
			}
		})
	}
}

const ambiente = "## Pinos do `go.mod`\n\nTexto.\n\n| Pino | Valor |\n|---|---|\n| `go` | `1.25.0` |\n| `toolchain` | `go1.27.2` |\n| `golang.org/x/sys` | `v0.47.0` |\n"

// CA-VERDOCS-18
func TestGoModKeepsThePinnedVersions(t *testing.T) {
	t.Parallel()
	mod := func(goVer, sys string) string {
		return "module example.com/m\n\ngo " + goVer + "\n\ntoolchain go1.27.2\n\nrequire (\n\tgolang.org/x/sys " + sys + "\n\tother.org/x v1.0.0 // indirect\n)\n"
	}
	cases := map[string]struct {
		goMod string
		env   string
		want  string // piece of the only error
	}{
		"as pinned":           {goMod: mod("1.25.0", "v0.47.0")},
		"go directive raised": {goMod: mod("1.26.0", "v0.47.0"), want: "go.mod:3: pino: go é 1.26.0, mas docs/desenvolvimento/ambiente.md#pinos-do-gomod fixa 1.25.0"},
		"x/sys updated":       {goMod: mod("1.25.0", "v0.49.0"), want: "go.mod:8: pino: golang.org/x/sys é v0.49.0"},
		"x/sys one-line form": {goMod: "module m\n\ngo 1.25.0\ntoolchain go1.27.2\nrequire golang.org/x/sys v0.47.0\n"},
		"x/sys dropped":       {goMod: "module m\n\ngo 1.25.0\ntoolchain go1.27.2\n", want: "golang.org/x/sys está fixado"},
		"toolchain changed":   {goMod: strings.Replace(mod("1.25.0", "v0.47.0"), "go1.27.2", "go1.28.0", 1), want: "toolchain é go1.28.0"},
		"no table in the doc": {goMod: mod("1.25.0", "v0.47.0"), env: "## Outra\n", want: "tabela de pinos não encontrada"},
		"empty table":         {goMod: mod("1.25.0", "v0.47.0"), env: "## Pinos do `go.mod`\n\n| Pino | Valor |\n|---|---|\n", want: "tabela de pinos não encontrada"},
		"no go.mod, no check": {goMod: "", env: "## Outra\n"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			tr := newTree().area("desenvolvimento")
			env := c.env
			if env == "" {
				env = ambiente
			}
			tr.add(envPath, hdr("Ambiente")+env)
			if c.goMod != "" {
				tr.set(goModPath, c.goMod)
			}
			got := only(run1(t, tr), ckPin)
			if c.want == "" {
				if len(got) != 0 {
					t.Fatalf("errors = %q", got)
				}
				return
			}
			if len(got) != 1 || !strings.Contains(got[0], c.want) {
				t.Fatalf("errors = %q, want %q", got, c.want)
			}
		})
	}
}
