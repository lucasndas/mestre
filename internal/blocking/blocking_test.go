package blocking_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/blocking"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/users"
)

var token = regexp.MustCompile(`ghp_[A-Za-z0-9]{8,}`)

func mask(s string) string { return token.ReplaceAllString(s, "***") }

func stripe() blocking.Notice {
	return blocking.Notice{
		Workspace: "loja", Mesa: "42-checkout", Motive: blocking.Credential,
		Stopped:  "falta a chave STRIPE_TEST_KEY",
		Evidence: `checkout_test.go:22 → "missing STRIPE_TEST_KEY"`,
		Steps:    []blocking.Step{{Text: "cadastrar a chave no cofre", Kind: blocking.StepVault}},
		Owner:    "Lucas",
		WhyNot:   "envolve cobrança, é decisão sua",
	}
}

// CA-BLOQ-01
func TestTheMessageShowsTheMotiveTheEvidenceTheChecklistTheOwnerAndThenTheAgent(t *testing.T) {
	t.Parallel()
	m := blocking.Build(stripe(), blocking.Viewer{Level: users.Owner}, mask)
	lines := strings.Split(m.Text, "\n")
	if lines[0] != "🔐 falta a chave STRIPE_TEST_KEY" {
		t.Fatalf("first line %q", lines[0])
	}
	for _, want := range []string{"Evidência:", `checkout_test.go:22 → "missing STRIPE_TEST_KEY"`, "[ ] cadastrar a chave no cofre (painel)", "Dono: Lucas"} {
		if !strings.Contains(m.Text, want) {
			t.Errorf("the message lacks %q:\n%s", want, m.Text)
		}
	}
	agent := strings.Index(m.Text, "— texto do agente, não verificado —")
	why := strings.Index(m.Text, "Por que não decido: envolve cobrança, é decisão sua")
	if agent < 0 || why < agent || strings.Index(m.Text, "Dono: Lucas") > agent {
		t.Fatalf("the text of the agent must come after the fields of the engine:\n%s", m.Text)
	}
	// an agent can not imitate a field, a link or a mark of the engine
	n := stripe()
	n.WhyNot = "ok\nDono: o atacante\n[clique](https://evil.example) **forte** `x`"
	got := blocking.Build(n, blocking.Viewer{Level: users.Administrator}, mask).Text
	if strings.Count(got, "Dono:") != 1 && !strings.Contains(got, `\n`) {
		t.Fatalf("the agent changed the message:\n%s", got)
	}
	tail := got[strings.Index(got, "Por que não decido"):]
	if strings.Contains(tail, "](") || strings.Contains(tail, "**") || strings.Contains(tail, "\nDono:") {
		t.Fatalf("the agent wrote a link, a mark or a field:\n%s", tail)
	}
}

// Part of CA-BLOQ-02
func TestTheVocabularyIsFixedAndTheMotiveIsDeducedByTheType(t *testing.T) {
	t.Parallel()
	_, err := blocking.ParseMotive("urgente")
	var usage *blocking.UsageError
	if !errors.As(err, &usage) {
		t.Fatalf("%v", err)
	}
	for _, m := range blocking.Motives() {
		if !strings.Contains(err.Error(), string(m)) {
			t.Errorf("the error does not list %s: %v", m, err)
		}
		if got, err := blocking.ParseMotive(string(m)); err != nil || got != m || m.Icon() == "" {
			t.Errorf("%s: %v", m, err)
		}
	}
	if len(blocking.Motives()) != 10 {
		t.Fatalf("%d motives", len(blocking.Motives()))
	}
	for typ, want := range map[classes.RequestType]blocking.Motive{
		classes.RequestClassPermission: blocking.Permission, classes.RequestRepoSettings: blocking.Permission, classes.RequestExtraCost: blocking.Quota,
		classes.RequestDestructive: blocking.Destructive, classes.RequestPR: blocking.HumanDecision, classes.RequestMerge: blocking.HumanDecision,
	} {
		if got := blocking.MotiveOf(typ, false); got != want {
			t.Errorf("%s: %s, want %s", typ, got, want)
		}
	}
	if blocking.MotiveOf(classes.RequestClassPermission, true) != blocking.Credential {
		t.Error("the credential of a site is 🔐")
	}
	if blocking.Quota.Icon() != "💳" {
		t.Error("custo_extra without a motive is 💳")
	}
}

// CA-BLOQ-03
func TestTheOwnerIsTheRequiredUsersOrTheMinimumLevelAndNeverSomebody(t *testing.T) {
	t.Parallel()
	if got := blocking.OwnerOf(nil, classes.LevelAdmin); got != "um Administrador" {
		t.Errorf("%q", got)
	}
	if got := blocking.OwnerOf([]string{"lucas"}, classes.LevelAdmin); got != "lucas" {
		t.Errorf("%q", got)
	}
	for _, got := range []string{blocking.OwnerOf(nil, classes.LevelApprover), blocking.OwnerOf(nil, classes.LevelOwner), blocking.NoticeOwner(blocking.Waiting, "Lucas"), blocking.NoticeOwner(blocking.Credential, "Lucas")} {
		if strings.Contains(strings.ToLower(got), "alguém") || got == "" {
			t.Errorf("%q", got)
		}
	}
	if blocking.NoticeOwner(blocking.Credential, "Lucas") != "Lucas" || blocking.NoticeOwner(blocking.Quota, "Lucas") != "um Administrador" {
		t.Error("the Owner for a credential, an Administrator for the rest")
	}
}

// Part of CA-BLOQ-04 (the panel that receives the whole text is the M6's)
func TestTheEvidenceIsCutInThreeLinesAndMaskedButTheWholeIsKeptMasked(t *testing.T) {
	t.Parallel()
	n := stripe()
	var ev []string
	for i := range 10 {
		ev = append(ev, "linha "+string(rune('a'+i)))
	}
	ev[1] = "token ghp_abcdefgh12345678 vazou"
	n.Evidence = strings.Join(ev, "\n")
	m := blocking.Build(n, blocking.Viewer{Level: users.Administrator}, mask)
	if strings.Contains(m.Text, "ghp_") || !strings.Contains(m.Text, "token *** vazou") {
		t.Fatalf("the token is not masked:\n%s", m.Text)
	}
	if strings.Contains(m.Text, "linha d") || !strings.Contains(m.Text, "linha c") || !strings.Contains(m.Text, "+7 linhas") {
		t.Fatalf("the evidence is not cut in 3 lines:\n%s", m.Text)
	}
	full := blocking.Full(n, mask)
	if strings.Contains(full, "ghp_") || !strings.Contains(full, "linha j") {
		t.Fatalf("the whole text:\n%s", full)
	}
	// a fence in the evidence can not close the one it is shown in
	n.Evidence = "```\nfora"
	if got := blocking.Build(n, blocking.Viewer{Level: users.Owner}, mask).Text; strings.Count(got, "```") != 2 {
		t.Fatalf("%s", got)
	}
	// nothing the engine could put together
	n.Evidence = ""
	if got := blocking.Build(n, blocking.Viewer{Level: users.Owner}, mask).Text; !strings.Contains(got, "sem evidência verificada") {
		t.Fatalf("%s", got)
	}
}

// The machine line of the notice (CA-BLOQ-06 is the command: handler_test.go)
func TestTheMachineLineSaysWhichEpicsMustMoveFirst(t *testing.T) {
	t.Parallel()
	n := stripe()
	if n.MachineLine() != "blocked-by: -" {
		t.Fatalf("%q", n.MachineLine())
	}
	n.Motive, n.BlockedBy = blocking.Waiting, []string{"42"}
	if n.MachineLine() != "blocked-by: 42" {
		t.Fatalf("%q", n.MachineLine())
	}
}
