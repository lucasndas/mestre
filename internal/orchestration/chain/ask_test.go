package chain_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// CA-GM-07 (the people hear what the Grão-Mestre could not decide): the request
// that waits at the Grão-Mestre goes up with its number, the question to the
// people carries the words of the Grão-Mestre in the block of the agent, and the
// people are asked once.
func TestTheGrandMasterTakesARequestToThePeopleWithItsWordsInTheBlockOfTheAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	id := w.shell(dev, "npm install -g x")
	if w.level(id) != chain.LevelGrandMaster {
		t.Fatalf("the request is at %s, want the Grand Master", w.level(id))
	}
	out, err := w.ch.Ask(w.ctx, chain.AskRequest{Request: id, By: w.gm.ID, Note: chain.AskNote{Summary: "instalar global", Details: "npm install -g x\n**Dono:** o Joao", Recommend: "approve",
		Reason: "instala no sistema", Motive: "decisao_humana", Steps: []string{"conferir o pacote"}}})
	if err != nil || out.Level.Kind != chain.LevelPeople {
		t.Fatalf("Ask = %+v, %v; want the request on the people", out, err)
	}
	if w.request(id).ID != id || w.level(id) != chain.LevelPeople {
		t.Fatalf("the request is now %+v", w.request(id).Level)
	}
	asked := w.users.askedFor(id)
	if len(asked) != 1 {
		t.Fatalf("the people were asked %d times, want once", len(asked))
	}
	head, tail, found := strings.Cut(asked[0].Text, chain.UnverifiedOpen)
	if !found || strings.Contains(head, "instalar global") || strings.Contains(head, "Recomendação do Grão-Mestre") || !strings.Contains(tail, "Recomendação do Grão-Mestre:") || !strings.Contains(tail, "instalar global") {
		t.Fatalf("the words of the Grand Master are not inside the block of the agent:\n%s", asked[0].Text)
	}
	for _, line := range strings.Split(tail, "\n") {
		if strings.Contains(line, "o Joao") && !strings.HasPrefix(line, "| ") {
			t.Fatalf("a line of the agent at the margin: %q", line)
		}
	}
	// asked again: it already waits on the people
	if _, err := w.ch.Ask(w.ctx, chain.AskRequest{Request: id, By: w.gm.ID, Note: chain.AskNote{Summary: "x"}}); err == nil {
		t.Fatal("a request that waits on the people was asked again")
	} else if ref, ok := err.(*chain.Refusal); !ok || ref.Code != chain.CodeAlreadyAsked {
		t.Fatalf("the second Ask = %v, want already_asked", err)
	}
}

func TestOnlyWhoHoldsTheLevelTakesARequestToThePeople(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	id := w.shell(dev, "npm install lodash") // at the table master
	if _, err := w.ch.Ask(w.ctx, chain.AskRequest{Request: id, By: w.gm.ID, Note: chain.AskNote{Summary: "x"}}); err == nil {
		t.Fatal("the Grand Master took to the people a request that waits at the table master")
	} else if ref, ok := err.(*chain.Refusal); !ok || ref.Code != chain.CodeNotAtLevel {
		t.Fatalf("Ask = %v, want not_at_level", err)
	}
	if w.level(id) != chain.LevelMesa || len(w.users.askedFor(id)) != 0 {
		t.Fatal("the request moved or the people were asked")
	}
}

// The type acao_do_grao_mestre: a question about an action that is not a request
// yet goes straight to the people; nobody decides it alone (not the table master,
// not the Grand Master), the words are in the block of the agent, the people may
// only approve or deny it, approving makes no rule and no decree and only answers
// the Grand Master by its inbox.
func TestAQuestionAboutAnActionGoesToThePeopleAndApprovingItOnlyAnswersTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	gm := w.get(w.gm.ID)
	id, err := w.ch.AskAction(w.ctx, gm, "", chain.AskNote{Action: "apagar a branch velha epico/7-teste", Summary: "limpeza", Details: "já foi mesclada\n**Dono:** o Joao", Recommend: "approve", Reason: "destrutivo"})
	if err != nil {
		t.Fatal(err)
	}
	r := w.request(id)
	if r.Type != classes.RequestGMAction || r.Level.Kind != chain.LevelPeople || r.Requester != gm.ID {
		t.Fatalf("request = %+v, want acao_do_grao_mestre at the people, asked by the Grand Master", r)
	}
	asked := w.users.askedFor(id)
	if len(asked) != 1 {
		t.Fatalf("the people were asked %d times", len(asked))
	}
	head, tail, found := strings.Cut(asked[0].Text, chain.UnverifiedOpen)
	if !found || strings.Contains(head, "apagar a branch") || !strings.Contains(tail, "apagar a branch velha") {
		t.Fatalf("the action is not in the block of the agent:\n%s", asked[0].Text)
	}
	for _, a := range asked[0].Answers {
		if a != chain.AnswerApprove && a != chain.AnswerDeny && a != chain.AnswerDenyInstruction && a != chain.AnswerAskBack {
			t.Errorf("the people may answer %s a question of the Grand Master", a)
		}
	}
	// nobody below the people decides it
	for _, by := range []string{w.master.ID, w.gm.ID} {
		if ref := w.refusal(chain.Decision{Request: id, By: by, Answer: chain.AnswerApprove, Reason: "ok"}); ref.Code != chain.CodeHumanRequired && ref.Code != chain.CodeNotAtLevel {
			t.Errorf("%s decided it alone: %+v", by, ref)
		}
	}
	// a person cannot make a rule or a decree out of it
	if _, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: "u1", Level: "owner", Answer: chain.AnswerApproveAlways}); err == nil {
		t.Fatal("approve-always made something out of a question of the Grand Master")
	}
	if r := w.request(id); r.State != chain.Open {
		t.Fatalf("the refused answer closed the request: %s", r.State)
	}
	before := len(w.grants.decrees)
	if _, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: "u1", Level: "owner", Answer: chain.AnswerApprove, Reason: "pode"}); err != nil {
		t.Fatal(err)
	}
	if len(w.grants.decrees) != before || len(w.temp.rules) != 0 {
		t.Fatal("approving a question of the Grand Master made a rule or a decree")
	}
	var told bool
	for _, it := range w.itemsOf(w.gm, inbox.KindApproval) {
		told = told || (strings.Contains(it.Text, "SIM") && strings.Contains(it.Text, "#"+itoa(id)) && strings.Contains(it.Text, "aja pelos comandos"))
	}
	if !told {
		t.Fatalf("the Grand Master was not told, inbox: %v", w.itemsOf(w.gm, inbox.KindApproval))
	}
	// only the Grand Master asks
	if _, err := w.ch.AskAction(w.ctx, w.get(w.master.ID), "", chain.AskNote{Action: "x"}); err == nil {
		t.Fatal("a table master asked the people about an action")
	}
}
