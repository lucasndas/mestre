package quests

import (
	"strings"
	"testing"
)

// Part of CA-PROVA-01 (the whole story, with the scroll, is in the full suite):
// the rule that writes a proof on the issue, with no rig. A criterion with a
// proof is checked and gets its line; one without stays as it was; a criterion
// somebody else checked is not taken; and a repeated id keeps the first.
func TestAProofChecksTheItemOfTheCriterionAndLeavesTheOthersAlone(t *testing.T) {
	t.Parallel()
	body := "Login com senha.\n\n- [ ] C1 aceita senha correta\n- [ ] C2 recusa senha vazia\n- [ ] C3 bloqueia depois de cinco erros\n- [x] C4 feito por uma pessoa\n- [ ] C1 repetido\n"
	cs := parseCriteria(body)
	if len(cs) != 4 || cs[0].ID != "C1" || cs[0].Text != "aceita senha correta" || cs[3].ID != "C4" {
		t.Fatalf("criteria = %+v", cs)
	}
	proofs := []Proof{
		{Criterion: "C1", Text: "login_test.go:TestVazia", Origin: ProofDeclared, By: "QA Antigravity 1"},
		{Criterion: "C2", Text: "captura em docs/c2.png", Origin: ProofVerified},
		{Criterion: "C4", Text: "outra", Origin: ProofDeclared, By: "QA Antigravity 1"},
	}
	got, checked := markCriteria(body, cs, proofs)
	for _, want := range []string{
		"- [x] C1 aceita senha correta\n  - prova (declarada por QA Antigravity 1): login_test.go:TestVazia\n",
		"- [x] C2 recusa senha vazia\n  - prova (verificada pelo motor): captura em docs/c2.png\n",
		"- [ ] C3 bloqueia depois de cinco erros\n- [x] C4 feito por uma pessoa\n",
		"Login com senha.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the issue lacks %q:\n%s", want, got)
		}
	}
	if !checked["C1"] || !checked["C2"] || checked["C3"] || checked["C4"] {
		t.Errorf("checked = %v, want C1 and C2 only", checked)
	}
	// the next round without a proof takes back what the engine checked, and only that
	for i := range cs {
		cs[i].Marked = checked[cs[i].ID]
	}
	again, _ := markCriteria(got, cs, nil)
	if !strings.Contains(again, "- [ ] C1 aceita senha correta") || !strings.Contains(again, "- [ ] C2 recusa senha vazia") || !strings.Contains(again, "- [x] C4 feito por uma pessoa") || strings.Contains(again, "prova (") {
		t.Errorf("after a round with no proof:\n%s", again)
	}
}

// Part of CA-PROVA-06 and CA-PROVA-07 (the stories with the returns are in the
// full suite): the print of a failure is the same for the same places written
// another way, differs by step, cause or place, and a return with no place has
// no print, so it can never count as the same failure.
func TestThePrintOfAFailureIsTheSameForTheSamePlaceWrittenAnotherWay(t *testing.T) {
	t.Parallel()
	a, _ := Fingerprint("review", "test", []string{"src/login.go:40"}, "")
	b, places := Fingerprint("review", "test", []string{"./src/login.go:40:7", "src/login.go:40"}, "")
	if a == "" || a != b || len(places) != 1 {
		t.Fatalf("prints %q and %q (places %v), want one and the same", a, b, places)
	}
	for name, other := range map[string][3]string{"cause": {"review", "build", "src/login.go:40"}, "step": {"qa", "test", "src/login.go:40"}, "place": {"review", "test", "src/login.go:52"}} {
		if c, _ := Fingerprint(other[0], other[1], []string{other[2]}, ""); c == a {
			t.Errorf("a different %s has the same print", name)
		}
	}
	if none, places := Fingerprint("review", "test", nil, ""); none != "" || places != nil {
		t.Errorf("no place: print %q, places %v; want none", none, places)
	}
}
