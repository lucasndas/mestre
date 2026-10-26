package tables_test

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/tables"
)

func TestParseCriteriaReadsOnlyTheItemsOfTheSectionInOrder(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		body string
		want []string
	}{
		"three items": {"Texto original.\n\n## Critérios\n\n- [ ] Dado A, quando B, então C\n- [x] Dado D, quando E, então F\n* [ ]   Dado G, quando H, então I  \n\n## Outro\n- [ ] não é critério\n",
			[]string{"Dado A, quando B, então C", "Dado D, quando E, então F", "Dado G, quando H, então I"}},
		"no section":             {"- [ ] solto\n## Contexto\n- [ ] também solto\n", []string{}},
		"empty":                  {"", []string{}},
		"fenced code":            {"## Criterios\n```\n- [ ] dentro do código\n```\n- [ ] fora\n", []string{"fora"}},
		"crlf":                   {"## Critérios\r\n- [ ] um\r\n- [ ] dois\r\n", []string{"um", "dois"}},
		"plain bullets":          {"## Critérios\n- sem caixa\n- [ ]\n- [ ] com caixa\n", []string{"com caixa"}},
		"blank item":             {"## Critérios\n- [ ]  \n- [ ]\t\n- [ ] real\n", []string{"real"}},
		"issue reference":        {"## critérios\n- [ ] a\n#42 depende disto\n- [ ] b\n", []string{"a", "b"}},
		"indented and colon":     {"   ## Critérios:\n- [ ] a\n", []string{"a"}},
		"control and format":     {"## Critérios\n- [ ] a\x1b[31m\U0000202Eb\U0000200Bc\U000E0041d\n", []string{"a[31mbcd"}},
		"comment hides heading":  {"<!--\n## Critérios\n- [ ] escondido\n-->\n## Critérios\n- [ ] visível\n", []string{"visível"}},
		"deeper heading ends it": {"## Critérios\n- [ ] a\n### Notas\n- [ ] b\n", []string{"a"}},
		"long item":              {"## Critérios\n- [ ] " + strings.Repeat("é", 600) + "\n", []string{strings.Repeat("é", 500) + "…"}},
	} {
		got, err := tables.ParseCriteria(tc.body)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q, %v; want %q", name, got, err, tc.want)
		}
	}
}

func TestParseCriteriaRefusesAnAmbiguousEpicInsteadOfAnEmptyList(t *testing.T) {
	t.Parallel()
	many := "## Critérios\n" + strings.Repeat("- [ ] x\n", tables.MaxCriteria+1)
	for name, body := range map[string]string{
		"two sections":   "## Critérios\n- [ ] a\n## Critérios\n- [ ] b\n",
		"open fence":     "## Critérios\n- [ ] a\n```\n- [ ] b\n",
		"too many items": many,
	} {
		got, err := tables.ParseCriteria(body)
		if !errors.Is(err, tables.ErrCriteria) || len(got) != 0 || got == nil {
			t.Errorf("%s: %q, %v; want an error and no criteria", name, got, err)
		}
	}
	if got, err := tables.ParseCriteria("## Critérios\n" + strings.Repeat("- [ ] x\n", tables.MaxCriteria)); err != nil || len(got) != tables.MaxCriteria {
		t.Errorf("the limit itself: %d, %v", len(got), err)
	}
}

type fakeProofs []tables.ProofState

func (f fakeProofs) Of(context.Context, string, string) ([]tables.ProofState, error) { return f, nil }

// CA-GM-16 (the second half; the model of the instructions is tested in
// internal/sessionzero)
func TestTableStatusGivesTheEpicWithItsNumberItsTitleAndTheThreeCriteria(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{proofs: fakeProofs{{Quest: "login", Criterion: "C1", Text: "entra", Marked: true, Origin: "verificada pelo motor", Proof: "go test"}, {Quest: "login", Criterion: "C2", Text: "erro", Origin: "sem prova"}}})
	w.activate()
	w.gh.add(42, "Login\U000E0041\U0000202E", "epico")
	w.gh.setBody(42, "Quero login por e-mail.\n\n## Critérios\n\n- [ ] Dado um e-mail válido, quando entra, então vê o painel\n- [ ] Dado senha errada, quando entra, então vê o erro\n- [ ] Dado 5 erros, quando tenta de novo, então espera 15 min\n")
	w.open(42)
	r := w.run(w.gm.ID, "table status", nil, "42-login")
	if !r.OK {
		t.Fatal(r.Error)
	}
	d := dataOf(t, r)
	epic, _ := d["epic"].(map[string]any)
	crit, _ := epic["criteria"].([]any)
	wantCrit := []any{"Dado um e-mail válido, quando entra, então vê o painel", "Dado senha errada, quando entra, então vê o erro", "Dado 5 erros, quando tenta de novo, então espera 15 min"}
	if epic["number"] != float64(42) || epic["title"] != "Login" || !slices.Equal(crit, wantCrit) || epic["criteria_error"] != nil {
		t.Errorf("epic = %v; want the 3 criteria %q in order", epic, wantCrit)
	}
	proofs, _ := d["proofs"].([]any)
	if len(proofs) != 2 || proofs[0].(map[string]any)["origin"] != "verificada pelo motor" || proofs[0].(map[string]any)["marked"] != true || proofs[1].(map[string]any)["origin"] != "sem prova" {
		t.Errorf("proofs = %v", d["proofs"])
	}

	// An epic that is ambiguous, or cannot be read, is not an epic without criteria.
	w.gh.setBody(42, "## Critérios\n- [ ] a\n## Critérios\n- [ ] b\n")
	r = w.run(w.gm.ID, "table status", nil, "42-login")
	epic, _ = dataOf(t, r)["epic"].(map[string]any)
	if c, ok := epic["criteria"].([]any); epic["criteria_error"] == nil || !ok || len(c) != 0 {
		t.Errorf("epic with two sections = %v", epic)
	}
	// ... and one that hangs is not waited for.
	w.gh.mu.Lock()
	w.gh.hang = true
	w.gh.mu.Unlock()
	done := make(chan map[string]any)
	go func() {
		r := w.run(w.gm.ID, "table status", nil, "42-login")
		e, _ := dataOf(t, r)["epic"].(map[string]any)
		done <- e
	}()
	var hung map[string]any
	for hung == nil {
		w.clk.Advance(time.Second)
		select {
		case hung = <-done:
		default:
			runtime.Gosched()
		}
	}
	if hung["criteria_error"] == nil || len(hung["criteria"].([]any)) != 0 {
		t.Errorf("epic with a hanging GitHub = %v", hung)
	}
	w.gh.mu.Lock()
	w.gh.hang = false
	w.gh.err = errors.New("github is down")
	w.gh.mu.Unlock()
	r = w.run(w.gm.ID, "table status", nil, "42-login")
	if !r.OK {
		t.Fatal(r.Error)
	}
	epic, _ = dataOf(t, r)["epic"].(map[string]any)
	if c, ok := epic["criteria"].([]any); epic["number"] != float64(42) || epic["title"] != "Login" || epic["criteria_error"] == nil || !ok || len(c) != 0 {
		t.Errorf("epic with GitHub down = %v", epic)
	}
}
