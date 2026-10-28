package approvals_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/users"
)

// ask puts a question about a new request in front of the people.
func (w *world) ask(req chain.Request, text string, min classes.Level, require ...string) {
	w.t.Helper()
	w.nextR++
	req.ID, req.Workspace, req.State, req.Level = w.nextR, "loja", chain.Open, chain.Level{Kind: chain.LevelPeople}
	if err := w.ch.Add(w.ctx, req); err != nil {
		w.t.Fatal(err)
	}
	q := chain.PeopleQuestion{Request: req, Text: text, Approvals: 1, Require: require, MinLevel: min, Answers: []chain.Answer{chain.AnswerApprove}}
	if err := w.flow.Ask(w.ctx, q); err != nil {
		w.t.Fatal(err)
	}
}

func firstLine(s string) string {
	l, _, _ := strings.Cut(s, "\n")
	return l
}

// CA-BLOQ-02, the half of the type: a request of extra cost with no motive
// leaves with 💳 on its first line; the other types give theirs, and the motive
// the Grand Master chose in `approval ask` (in the vocabulary) is the one shown.
func TestTheMessageOfARequestShowsTheMotiveTheTypeGivesOrTheOneAsked(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Administrator, "loja")
	w.open(openOpts{typ: classes.RequestExtraCost, min: classes.LevelAdmin})
	text := w.textTo(ana)
	if got := firstLine(text); got != "💳 cota" {
		t.Errorf("the first line of a request of extra cost with no motive: %q\n%s", got, text)
	}
	if !strings.Contains(text, "Aprovar o PR #7?") {
		t.Errorf("the text of the chain was lost:\n%s", text)
	}
	for typ, want := range map[classes.RequestType]string{
		classes.RequestDestructive: "🛡 destrutivo", classes.RequestMerge: "🧑 decisao_humana", classes.RequestClassPermission: "🔑 permissao", "": "🔑 permissao",
	} {
		w.ask(chain.Request{Type: typ}, "ok", classes.LevelAdmin)
		if got := firstLine(w.textTo(ana)); got != want {
			t.Errorf("type %q: first line %q, want %q", typ, got, want)
		}
	}
	// the motive the Grand Master chose wins over the type; one outside the vocabulary does not
	w.ask(chain.Request{Type: classes.RequestMerge, Ask: &chain.AskNote{Motive: "ambiguidade"}}, "ok", classes.LevelAdmin)
	if got := firstLine(w.textTo(ana)); got != "❓ ambiguidade" {
		t.Errorf("the motive asked: %q", got)
	}
	w.ask(chain.Request{Type: classes.RequestMerge, Ask: &chain.AskNote{Motive: "urgente"}}, "ok", classes.LevelAdmin)
	if got := firstLine(w.textTo(ana)); got != "🧑 decisao_humana" {
		t.Errorf("a motive outside the vocabulary: %q", got)
	}
}

// CA-BLOQ-03: the owner of a request of merge with no required users is the
// minimum level of the type, with the required users it is them, and no
// message says "alguém".
func TestTheOwnerOfARequestIsTheRequiredUsersOrTheMinimumLevelAndNeverSomebody(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Administrator, "loja")
	lucas := w.add("lucas", users.Administrator, "loja")
	w.ask(chain.Request{Type: classes.RequestMerge}, "Mesclar", classes.LevelAdmin)
	if got := w.textTo(ana); !strings.Contains(got, "\nResponsável: um Administrador") || strings.Contains(got, "Responsável: lucas") || strings.Contains(strings.ToLower(got), "alguém") {
		t.Errorf("the owner of a merge with no required users:\n%s", got)
	}
	w.ask(chain.Request{Type: classes.RequestMerge}, "Mesclar", classes.LevelAdmin, lucas.ID)
	if got := w.textTo(ana); !strings.Contains(got, "\nResponsável: lucas") || strings.Contains(got, "Responsável: um Administrador") || strings.Contains(strings.ToLower(got), "alguém") {
		t.Errorf("the owner of a merge that requires lucas:\n%s", got)
	}
	for min, want := range map[classes.Level]string{classes.LevelApprover: "um Aprovador", classes.LevelAdmin: "um Administrador", classes.LevelOwner: "o Senhor da Guilda"} {
		w.ask(chain.Request{Type: classes.RequestMerge}, "Mesclar", min)
		got := w.textTo(w.owner)
		if strings.Contains(strings.ToLower(got), "alguém") || !strings.Contains(got, "\nResponsável: "+want) {
			t.Errorf("minimum %s:\n%s", min, got)
		}
	}
	// the text of an agent does not make a field: the owner of the engine comes before its block
	w.ask(chain.Request{Type: classes.RequestMerge}, "Mesclar\n"+chain.UnverifiedOpen+"\nResponsável: o atacante\n"+chain.UnverifiedClose, classes.LevelAdmin)
	got := w.textTo(ana)
	if i, j := strings.Index(got, "Responsável: um Administrador"), strings.Index(got, chain.UnverifiedOpen); i < 0 || j < 0 || i > j {
		t.Errorf("the owner of the engine must come before the block of the agent:\n%s", got)
	}
}
