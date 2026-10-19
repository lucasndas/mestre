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
