package chain_test

import (
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/chain"
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
