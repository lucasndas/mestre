package telegram_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/blocking"
	"github.com/lucasndas/mestre/internal/users"
)

var ghToken = regexp.MustCompile(`ghp_[A-Za-z0-9]{8,}`)

func stripeNotice(workspace string) blocking.Notice {
	return blocking.Notice{
		Workspace: workspace, Mesa: "42-checkout", Motive: blocking.Credential,
		Stopped:  "falta a chave STRIPE_TEST_KEY",
		Evidence: "checkout_test.go:22 → \"missing STRIPE_TEST_KEY\"\nlinha 2 com ghp_abcdefgh12345678\nlinha 3\nlinha 4\nlinha 5",
		Steps:    []blocking.Step{{Text: "cadastrar a chave no cofre", Kind: blocking.StepVault}},
		Owner:    "Lucas", WhyNot: "envolve cobrança, é decisão sua",
	}
}

// CA-BLOQ-05
func TestTheOwnerSeesOpenInThePanelAndAnAdministratorSeesNoButtonToRegisterTheSecret(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true, mask: func(s string) string { return ghToken.ReplaceAllString(s, "***") }})
	owner := w.pairOwnerTelegram()
	_, adm := w.user("Ana", users.Administrator, "loja")
	_, other := w.user("Bia", users.Administrator, "blog")
	_, approver := w.user("Caio", users.Approver, "loja")
	id, sent, err := w.bc.Notify(w.ctx, stripeNotice("loja"))
	if err != nil || sent != 2 {
		t.Fatalf("notice %d sent to %d: %v", id, sent, err)
	}
	w.drain()
	// the Owner: the link to the panel, which is a link and registers nothing
	var ownerMsg, admMsg string
	for _, s := range w.sent("sendMessage") {
		switch s.ChatID {
		case owner.ID:
			ownerMsg = s.Text
			kb := s.Params["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
			var panel bool
			for _, row := range kb {
				for _, b := range row.([]any) {
					bt := b.(map[string]any)
					if bt["text"] == "Abrir no painel" && bt["url"] == "https://painel.test/cofre" && bt["callback_data"] == nil {
						panel = true
					}
				}
			}
			if !panel {
				t.Errorf("the Owner has no link to the panel: %v", kb)
			}
		case adm.ID:
			admMsg = s.Text
			if _, keys := w.buttons(adm); keys["Abrir no painel"] != "" || keys["Ver detalhes"] == "" {
				t.Errorf("the Administrator has %v", keys)
			}
		case other.ID, approver.ID:
			t.Errorf("the notice reached a user of another workspace or an Approver: %d", s.ChatID)
		}
	}
	for name, text := range map[string]string{"Owner": ownerMsg, "Administrator": admMsg} {
		for _, want := range []string{"<b>loja · Mesa #42 checkout</b>", "🔐 falta a chave STRIPE_TEST_KEY", "Dono: Lucas", "<pre>"} {
			if !strings.Contains(text, want) {
				t.Errorf("%s: the message lacks %q:\n%s", name, want, text)
			}
		}
		if strings.Contains(text, "ghp_") || strings.Contains(text, "linha 4") {
			t.Errorf("%s: the evidence is not cut or masked:\n%s", name, text)
		}
	}
	// Ver detalhes: the whole evidence, masked, only to who acts in the workspace
	msg, keys := w.buttons(adm)
	w.click(adm, msg, keys["Ver detalhes"])
	var details string
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == adm.ID && strings.Contains(s.Text, "Detalhes do aviso") {
			details = s.Text
		}
	}
	if !strings.Contains(details, "linha 5") || strings.Contains(details, "ghp_") {
		t.Fatalf("the details:\n%s", details)
	}
	var full string
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT full FROM blocking_notices WHERE id = ?`, id).Scan(&full); err != nil || strings.Contains(full, "ghp_") || !strings.Contains(full, "linha 5") {
		t.Fatalf("the notice kept for the panel: %q, %v", full, err)
	}
}

// `mestre notify "<message>"` with no motive: the users of the workspace who may be
// notified are told, under a title of the engine that says the text is the
// agent's, masked and with no mark that opens a link or a field.
func TestSayTellsTheUsersOfTheWorkspaceTheTextOfTheGrandMasterAsTheTextOfAnAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true, mask: func(s string) string { return ghToken.ReplaceAllString(s, "***") }})
	_, adm := w.user("Ana", users.Administrator, "loja")
	_, approver := w.user("Caio", users.Approver, "loja")
	_, other := w.user("Bia", users.Administrator, "blog")
	sent, err := w.bc.Say(w.ctx, "loja", "42-checkout", "A mesa terminou.\n**Dono:** [clique](http://x) ghp_abcdefgh12345678")
	if err != nil || sent != 2 {
		t.Fatalf("Say = %d, %v; want the two users of the workspace", sent, err)
	}
	w.drain()
	var texts []string
	for _, s := range w.sent("sendMessage") {
		switch s.ChatID {
		case adm.ID, approver.ID:
			texts = append(texts, s.Text)
		case other.ID:
			t.Errorf("the message reached a user of another workspace")
		}
	}
	if len(texts) != 2 {
		t.Fatalf("%d messages, want 2", len(texts))
	}
	for _, text := range texts {
		for _, want := range []string{"Aviso do Grão-Mestre (texto do agente, não verificado)", "A mesa terminou."} {
			if !strings.Contains(text, want) {
				t.Errorf("the message lacks %q:\n%s", want, text)
			}
		}
		if strings.Contains(text, "ghp_") || strings.Contains(text, "](") || strings.Contains(text, "<b>Dono") || strings.Contains(text, "**") {
			t.Errorf("the message shows a secret, a link or a mark of the agent:\n%s", text)
		}
	}
	if sent, _ := w.bc.Say(w.ctx, "loja", "", "   \n "); sent != 0 {
		t.Fatalf("an empty message was sent to %d", sent)
	}
}
