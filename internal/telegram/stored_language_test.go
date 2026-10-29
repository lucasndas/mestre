package telegram_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/users"
)

// CA-MSG-08 (the Telegram side; the panel side is in internal/panel)
func TestTheBlockingNoticeAndTheCardAreSaidInTheLanguageOfEachUserWithTheSameAgentText(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true, approvals: true, mask: func(s string) string { return ghToken.ReplaceAllString(s, "***") }})
	_ = w.pairOwnerTelegram()
	en, enTG := w.user("Ana", users.Administrator, "loja")
	if err := w.users.SetLanguage(w.ctx, en.ID, "en"); err != nil {
		t.Fatal(err)
	}
	pt, ptTG := w.user("Bia", users.Administrator, "loja")
	if err := w.users.SetLanguage(w.ctx, pt.ID, "pt-BR"); err != nil {
		t.Fatal(err)
	}
	n := stripeNotice("loja")
	n.Owner = blocking.NoticeOwner(blocking.Quota, "")
	n.WhyNot = "envolve cobrança ghp_abcdefghijkl"
	if _, sent, err := w.bc.Notify(w.ctx, n); err != nil || sent != 3 {
		t.Fatalf("sent to %d: %v", sent, err)
	}
	w.drain()
	said := func(chat int64) string {
		var out string
		for _, s := range w.sent("sendMessage") {
			if s.ChatID == chat {
				out = s.Text
			}
		}
		return out
	}
	enMsg, ptMsg := said(enTG.ID), said(ptTG.ID)
	for _, want := range []string{"Evidence:", "To unblock:", "Owner: an Administrator", "agent text, not verified", "Why I do not decide: envolve cobrança"} {
		if !strings.Contains(enMsg, want) {
			t.Errorf("en: the notice lacks %q:\n%s", want, enMsg)
		}
	}
	for _, want := range []string{"Evidência:", "Para destravar:", "Dono: um Administrador", "texto do agente, não verificado", "Por que não decido: envolve cobrança"} {
		if !strings.Contains(ptMsg, want) {
			t.Errorf("pt-BR: the notice lacks %q:\n%s", want, ptMsg)
		}
	}
	// masked in both: the secret of the agent is gone and the rest of its text is there
	for lang, msg := range map[string]string{"en": enMsg, "pt-BR": ptMsg} {
		if strings.Contains(msg, "ghp_") || !strings.Contains(msg, "envolve cobrança") || !strings.Contains(msg, "***") {
			t.Errorf("%s: the notice is not masked and whole:\n%s", lang, msg)
		}
	}
	// the card of an approval, too
	r := chain.Request{ID: 8, Workspace: "loja", Type: classes.RequestExtraCost, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople}}
	if err := w.chain.Add(w.ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Justification = "porque sim ghp_abcdefghijkl"
	card := chain.Card(r, chain.Level{Kind: chain.LevelPeople}, allAnswers)
	if err := w.flow.Ask(w.ctx, chain.PeopleQuestion{Request: r, Engine: card.Engine, Agent: card.Agent, Approvals: 1, MinLevel: classes.LevelApprover, Answers: allAnswers}); err != nil {
		t.Fatal(err)
	}
	w.flush()
	w.drain()
	enCard, ptCard := said(enTG.ID), said(ptTG.ID)
	if !strings.Contains(enCard, "Approval request #8") || !strings.Contains(ptCard, "Pedido de aprovação #8") {
		t.Errorf("the frame of the card:\n%s\n---\n%s", enCard, ptCard)
	}
	for _, c := range []string{enCard, ptCard} {
		if strings.Contains(c, "ghp_") || !strings.Contains(c, "porque sim") {
			t.Errorf("the text of the agent is not the same:\n%s", c)
		}
	}
	// the copy for an agent approver is in en: it is delivered through the inbox, which the
	// real chain proves (internal/orchestration/chain, TestABaixoRequestReachesTheTableMasterAsAnApprovalItem...)
}

// CA-IDIOMA-11
func TestTheCardGoesToTheUserInEnAndTheCopyOfTheAgentApproverIsInEn(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	en, enTG := w.user("Ana", users.Approver, "loja")
	if err := w.users.SetLanguage(w.ctx, en.ID, "en"); err != nil {
		t.Fatal(err)
	}
	r := chain.Request{ID: 9, Workspace: "loja", Type: classes.RequestExtraCost, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople}, Justification: "pedido do agente"}
	if err := w.chain.Add(w.ctx, r); err != nil {
		t.Fatal(err)
	}
	card := chain.Card(r, chain.Level{Kind: chain.LevelPeople}, allAnswers)
	if err := w.flow.Ask(w.ctx, chain.PeopleQuestion{Request: r, Engine: card.Engine, Agent: card.Agent, Approvals: 1, MinLevel: classes.LevelApprover, Answers: allAnswers}); err != nil {
		t.Fatal(err)
	}
	w.flush()
	w.drain()
	got := lastText(w, enTG)
	if !strings.Contains(got, "Approval request #9") || !strings.Contains(got, "pedido do agente") || !strings.Contains(got, "Answers of this level (buttons)") {
		t.Errorf("the card of the user:\n%s", got)
	}
	// the copy of the agent approver is delivered through the inbox by the real chain, in en, with
	// the same text of the agent (internal/orchestration/chain, TestABaixoRequestReachesTheTableMaster...)
}
