package blocking_test

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/users"
)

var token = regexp.MustCompile(`ghp_[A-Za-z0-9]{8,}`)

func mask(s string) string { return token.ReplaceAllString(s, "***") }

func stripe() blocking.Notice {
	return blocking.Notice{
		Workspace: "loja", Mesa: "42-checkout", Motive: blocking.Credential,
		Stopped:  "falta a chave STRIPE_TEST_KEY",
		Evidence: `checkout_test.go:22 → "missing STRIPE_TEST_KEY"`,
		Steps:    []blocking.Step{{Text: i18n.M("block.step_vault"), Kind: blocking.StepVault}},
		Owner:    i18n.Text("Lucas"),
		WhyNot:   "envolve cobrança, é decisão sua",
	}
}

// CA-BLOQ-01
func TestTheMessageShowsTheMotiveTheEvidenceTheChecklistTheOwnerAndThenTheAgent(t *testing.T) {
	t.Parallel()
	m := blocking.Build(stripe(), blocking.Viewer{Level: users.Owner}, mask)
	lines := strings.Split(m.Text(i18n.PtBR), "\n")
	if lines[0] != "🔐 falta a chave STRIPE_TEST_KEY" {
		t.Fatalf("first line %q", lines[0])
	}
	for _, want := range []string{"Evidência:", `checkout_test.go:22 → "missing STRIPE_TEST_KEY"`, "[ ] cadastrar a chave no cofre (painel)", "Dono: Lucas"} {
		if !strings.Contains(m.Text(i18n.PtBR), want) {
			t.Errorf("the message lacks %q:\n%s", want, m.Text(i18n.PtBR))
		}
	}
	agent := strings.Index(m.Text(i18n.PtBR), "— texto do agente, não verificado —")
	why := strings.Index(m.Text(i18n.PtBR), "Por que não decido: envolve cobrança, é decisão sua")
	if agent < 0 || why < agent || strings.Index(m.Text(i18n.PtBR), "Dono: Lucas") > agent {
		t.Fatalf("the text of the agent must come after the fields of the engine:\n%s", m.Text(i18n.PtBR))
	}
	// an agent can not imitate a field, a link or a mark of the engine
	n := stripe()
	n.WhyNot = "ok\nDono: o atacante\n[clique](https://evil.example) **forte** `x`"
	got := blocking.Build(n, blocking.Viewer{Level: users.Administrator}, mask).Text(i18n.PtBR)
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
	pt := func(m i18n.Msg) string { return i18n.Render(i18n.PtBR, m) }
	if got := pt(blocking.OwnerOf(nil, classes.LevelAdmin)); got != "um Administrador" {
		t.Errorf("%q", got)
	}
	if got := i18n.Render(i18n.En, blocking.OwnerOf(nil, classes.LevelAdmin)); got != "an Administrator" {
		t.Errorf("%q", got)
	}
	if got := pt(blocking.OwnerOf([]string{"lucas"}, classes.LevelAdmin)); got != "lucas" {
		t.Errorf("%q", got)
	}
	for _, got := range []string{pt(blocking.OwnerOf(nil, classes.LevelApprover)), pt(blocking.OwnerOf(nil, classes.LevelOwner)), pt(blocking.NoticeOwner(blocking.Waiting, "Lucas")), pt(blocking.NoticeOwner(blocking.Credential, "Lucas"))} {
		if strings.Contains(strings.ToLower(got), "alguém") || got == "" {
			t.Errorf("%q", got)
		}
	}
	if pt(blocking.NoticeOwner(blocking.Credential, "Lucas")) != "Lucas" || pt(blocking.NoticeOwner(blocking.Quota, "Lucas")) != "um Administrador" {
		t.Error("the Owner for a credential, an Administrator for the rest")
	}
}

// CA-BLOQ-04
// The evidence goes cut in 3 lines with the token masked, and the whole text the panel gets is
// masked too (the bell and the saved notice: internal/telegram/panelfeed_test.go).
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
	if strings.Contains(m.Text(i18n.PtBR), "ghp_") || !strings.Contains(m.Text(i18n.PtBR), "token *** vazou") {
		t.Fatalf("the token is not masked:\n%s", m.Text(i18n.PtBR))
	}
	if strings.Contains(m.Text(i18n.PtBR), "linha d") || !strings.Contains(m.Text(i18n.PtBR), "linha c") || !strings.Contains(m.Text(i18n.PtBR), "+7 linhas") {
		t.Fatalf("the evidence is not cut in 3 lines:\n%s", m.Text(i18n.PtBR))
	}
	full := i18n.Render(i18n.PtBR, blocking.Full(n, mask))
	if strings.Contains(full, "ghp_") || !strings.Contains(full, "linha j") {
		t.Fatalf("the whole text:\n%s", full)
	}
	// a fence in the evidence can not close the one it is shown in
	n.Evidence = "```\nfora"
	if got := blocking.Build(n, blocking.Viewer{Level: users.Owner}, mask).Text(i18n.PtBR); strings.Count(got, "```") != 2 {
		t.Fatalf("%s", got)
	}
	// nothing the engine could put together
	n.Evidence = ""
	if got := blocking.Build(n, blocking.Viewer{Level: users.Owner}, mask).Text(i18n.PtBR); !strings.Contains(got, "sem evidência verificada") {
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

// CA-BLOQ-01: the evidence the engine builds from the output of a test that failed.
func TestTheEvidenceIsBuiltFromTheOutputOfTheTestThatFailed(t *testing.T) {
	t.Parallel()
	out := "=== RUN   TestCheckout\n--- FAIL: TestCheckout (0.00s)\n    checkout_test.go:22: missing STRIPE_TEST_KEY\nFAIL\nFAIL\tloja/checkout\t0.004s\n"
	for name, c := range map[string]struct {
		cmd, out string
		places   []string
		want     string
	}{
		"go test":          {"go test ./...", out, []string{"checkout_test.go:22"}, "checkout_test.go:22 → \"missing STRIPE_TEST_KEY\"\n$ go test ./..."},
		"windows paths":    {"go test", "    C:\\wt\\checkout_test.go:22:7: missing STRIPE_TEST_KEY\r\n", []string{"checkout_test.go:22"}, "C:/wt/checkout_test.go:22 → \"missing STRIPE_TEST_KEY\"\n$ go test"},
		"the place listed": {"go test", "a.go:1: ruído\nb.go:2: o erro\n", []string{"b.go:2"}, "b.go:2 → \"o erro\"\na.go:1 → \"ruído\"\n$ go test"},
		"at most two":      {"", "a.go:1: um\nb.go:2: dois\nc.go:3: três\n", nil, "a.go:1 → \"um\"\nb.go:2 → \"dois\""},
		"no place":         {"make", "tudo quebrou\n\nsem lugar\n", nil, "→ \"sem lugar\"\n$ make"},
		"quotes":           {"", "a.go:1: disse \"x\"", nil, "a.go:1 → \"disse 'x'\""},
		"nothing":          {"go test", " \n", nil, ""},
	} {
		if got := blocking.TestEvidence(c.cmd, c.out, c.places); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	// the message built from it names the file, the line and the error, masked and cut like any evidence
	n := stripe()
	n.Evidence = blocking.TestEvidence("go test ./...", out+"    ghp_abcdefgh12345678\n", []string{"checkout_test.go:22"})
	m := blocking.Build(n, blocking.Viewer{Level: users.Owner}, mask)
	if !strings.Contains(m.Text(i18n.PtBR), "checkout_test.go:22 → \"missing STRIPE_TEST_KEY\"") || strings.Contains(m.Text(i18n.PtBR), "ghp_") {
		t.Errorf("the message:\n%s", m.Text(i18n.PtBR))
	}
}
