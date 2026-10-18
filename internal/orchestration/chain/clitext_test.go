package chain_test

import (
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// The texts of the engine to an approver and to who asked speak the CLI that
// docs/cli/grao-mestre.md defines (milestone 5 builds the commands; milestone 4
// writes the texts that name them).

// documentedAnswers are the answers of an agent that docs/cli/grao-mestre.md
// names in `mestre approval decide`.
func documentedAnswers(t *testing.T) []string {
	t.Helper()
	doc, err := os.ReadFile("../../../docs/cli/grao-mestre.md")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(doc), "\n") {
		if !strings.HasPrefix(line, "- `decide`:") {
			continue
		}
		_, list, _ := strings.Cut(line, "de agente:")
		list, _, _ = strings.Cut(list, "Erros:")
		var out []string
		for _, m := range regexp.MustCompile("`([a-z]+(?:-[a-z]+)*)`").FindAllStringSubmatch(list, -1) {
			out = append(out, m[1])
		}
		slices.Sort(out)
		return out
	}
	t.Fatal("the bullet `decide` of docs/cli/grao-mestre.md was not found")
	return nil
}

// listed are the answers an item lists, one per line "- <name>: …".
func listed(text string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`(?m)^- ([a-z-]+): `).FindAllStringSubmatch(text, -1) {
		out = append(out, m[1])
	}
	slices.Sort(out)
	return out
}

// CA-CADEIA-17: the texts of the engine speak the CLI of docs/cli/grao-mestre.md:
// the syntax of `approval decide`, the answers by the names the CLI gives them
// and the options they take; "perguntar de volta" sends the requester to
// `approval reply`; the people, who answer with buttons, are sent to no command.
func TestTheTextsOfTheApprovalSpeakTheCLIOfTheDocument(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	id := w.shell(dev, "npm install lodash")
	items := w.approvalItems(w.master)
	if len(items) != 1 {
		t.Fatalf("%d approval items", len(items))
	}
	text := items[0].Text

	documented := documentedAnswers(t)
	if len(documented) < 9 {
		t.Fatalf("the document names %v, want at least the nine answers of an agent", documented)
	}
	if got := listed(text); !slices.Equal(got, documented) {
		t.Errorf("the item lists the answers %v, the document names %v", got, documented)
	}
	if syntax := fmt.Sprintf(`mestre approval decide %d <resposta> --reason "<motivo>"`, id); !strings.Contains(text, syntax) {
		t.Errorf("the item lacks the syntax %q:\n%s", syntax, text)
	}
	for _, wrong := range []string{"--answer", "--instruction", "--until-mission-end", "approve_", "deny_", "ask_back", "climb"} {
		if strings.Contains(text, wrong) {
			t.Errorf("the item has %q, which the CLI does not have:\n%s", wrong, text)
		}
	}
	for answer, flags := range map[string][]string{
		"approve-for": {"--for", "--until-quest"}, "approve-pattern": {"--pattern", "--for", "--until-quest"}, "deny-with": {"--text"}, "ask-back": {"--text"},
	} {
		line := regexp.MustCompile(`(?m)^- ` + answer + `: .*$`).FindString(text)
		for _, f := range flags {
			if !strings.Contains(line, f) {
				t.Errorf("the line of %s = %q, want the option %s", answer, line, f)
			}
		}
	}

	// the question of "perguntar de volta" says how to answer it
	cli, _ := os.ReadFile("../../../docs/cli/grao-mestre.md")
	reply := fmt.Sprintf(`mestre approval reply %d "<texto>"`, id)
	if !strings.Contains(string(cli), `mestre approval reply <id> "<texto>"`) {
		t.Error(`docs/cli/grao-mestre.md does not define mestre approval reply <id> "<texto>"`)
	}
	if _, err := w.ch.Decide(w.ctx, chain.Decision{Request: id, By: w.master.ID, Answer: chain.AnswerAskBack, Instruction: "para que serve?"}); err != nil {
		t.Fatal(err)
	}
	if qs := w.itemsOf(dev, inbox.KindAnswer); len(qs) != 1 || !strings.Contains(qs[0].Text, reply) {
		t.Errorf("the requester got %+v, want the question and %s", qs, reply)
	}

	// the people answer with buttons
	high := w.shell(dev, `psql -c "DROP TABLE users"`)
	asked := w.users.askedFor(high)
	if len(asked) != 1 {
		t.Fatalf("the people were asked %d times", len(asked))
	}
	if strings.Contains(asked[0].Text, "mestre approval") {
		t.Errorf("the people are sent to a command:\n%s", asked[0].Text)
	}
	for _, label := range []string{"Aprovar agora", "Negar com instrução", "Perguntar de volta"} {
		if !strings.Contains(asked[0].Text, label) {
			t.Errorf("the question to the people lacks the button %q:\n%s", label, asked[0].Text)
		}
	}
}

// CA-CADEIA-17: the refusals that name an option name the options of the CLI.
func TestTheRefusalsOfAnAnswerNameTheOptionsOfTheCLI(t *testing.T) {
	t.Parallel()
	w := newRTM(t)
	dev := w.recruit("Dev")
	id := w.shell(dev, "npm install lodash")
	for _, c := range []struct {
		d      chain.Decision
		wants  []string
		forbid []string
	}{
		{chain.Decision{Answer: chain.AnswerApprovePattern}, []string{"approve-pattern", "--pattern"}, []string{"approve_pattern"}},
		{chain.Decision{Answer: chain.AnswerApproveFor}, []string{"approve-for", "--for", "--until-quest"}, []string{"approve_for", "--until-mission-end"}},
		{chain.Decision{Answer: chain.AnswerAskBack}, []string{"ask-back", "--text"}, []string{"ask_back", "--instruction"}},
	} {
		c.d.Request, c.d.By, c.d.Reason = id, w.master.ID, "motivo"
		ref := w.refusal(c.d)
		for _, s := range c.wants {
			if !strings.Contains(ref.Message, s) {
				t.Errorf("%s: the refusal %q lacks %q", c.d.Answer, ref.Message, s)
			}
		}
		for _, s := range c.forbid {
			if strings.Contains(ref.Message, s) {
				t.Errorf("%s: the refusal %q has %q, which the CLI does not have", c.d.Answer, ref.Message, s)
			}
		}
	}
}
