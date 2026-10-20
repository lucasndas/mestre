package tables_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/tables"
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
		"deeper heading ends it": {"## Critérios\n- [ ] a\n### Notas\n- [ ] b\n", []string{"a"}},
	} {
		if got := tables.ParseCriteria(tc.body); !slices.Equal(got, tc.want) {
			t.Errorf("%s: %q; want %q", name, got, tc.want)
		}
	}
}

// CA-GM-16 (the second half; the model of the instructions is tested in
// internal/sessionzero)
func TestTableStatusGivesTheEpicWithItsNumberItsTitleAndTheThreeCriteria(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	w.activate()
	w.gh.add(42, "Login", "epico")
	w.gh.setBody(42, "Quero login por e-mail.\n\n## Critérios\n\n- [ ] Dado um e-mail válido, quando entra, então vê o painel\n- [ ] Dado senha errada, quando entra, então vê o erro\n- [ ] Dado 5 erros, quando tenta de novo, então espera 15 min\n")
	w.open(42)
	r := w.run(w.gm.ID, "table status", nil, "42-login")
	if !r.OK {
		t.Fatal(r.Error)
	}
	epic, _ := dataOf(t, r)["epic"].(map[string]any)
	crit, _ := epic["criteria"].([]any)
	if epic["number"] != float64(42) || epic["title"] != "Login" || len(crit) != 3 || crit[2] != "Dado 5 erros, quando tenta de novo, então espera 15 min" || epic["criteria_error"] != nil {
		t.Errorf("epic = %v", epic)
	}

	// An epic that cannot be read is not an epic without criteria.
	w.gh.mu.Lock()
	w.gh.err = errors.New("github is down")
	w.gh.mu.Unlock()
	r = w.run(w.gm.ID, "table status", nil, "42-login")
	if !r.OK {
		t.Fatal(r.Error)
	}
	epic, _ = dataOf(t, r)["epic"].(map[string]any)
	if epic["number"] != float64(42) || epic["title"] != "Login" || epic["criteria_error"] == nil {
		t.Errorf("epic with GitHub down = %v", epic)
	}
}
