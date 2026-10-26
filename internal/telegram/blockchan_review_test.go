package telegram_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/users"
)

func TestABadPanelLinkCostsTheButtonNeverTheNotice(t *testing.T) {
	t.Parallel()
	for _, bad := range []string{"javascript:x", "ftp://painel/x", "painel.local", "https://u:p@painel/x", ""} {
		w := newWorld(t, rigOpts{block: true})
		w.bc.PanelURL = bad
		owner := w.pairOwnerTelegram()
		if _, sent, err := w.bc.Notify(w.ctx, stripeNotice("loja")); err != nil || sent != 1 {
			t.Fatalf("%q: sent %d, %v", bad, sent, err)
		}
		w.drain()
		for _, s := range w.sent("sendMessage") {
			if s.ChatID != owner.ID {
				continue
			}
			if kb, ok := s.Params["reply_markup"].(map[string]any); ok && strings.Contains(strings.ToLower(strings.TrimSpace(toString(kb))), "url") {
				t.Errorf("%q: a link button went out: %v", bad, kb)
			}
		}
	}
}

func toString(m map[string]any) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k)
		if rows, ok := v.([]any); ok {
			for _, r := range rows {
				for _, x := range r.([]any) {
					for kk := range x.(map[string]any) {
						b.WriteString(" " + kk)
					}
				}
			}
		}
	}
	return b.String()
}

func TestANoticeWithAMotiveOutsideTheVocabularyIsRefusedAndEvidenceIsBounded(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true})
	w.pairOwnerTelegram()
	n := stripeNotice("loja")
	n.Motive = "urgente"
	if _, _, err := w.bc.Notify(w.ctx, n); err == nil {
		t.Fatal("a motive outside the vocabulary was sent")
	}
	n = stripeNotice("loja")
	n.BlockedBy = []string{"4x"}
	if _, _, err := w.bc.Notify(w.ctx, n); err == nil {
		t.Fatal("a blocked-by that is not a number was sent")
	}
	if w.count("blocking_notices") != 0 {
		t.Fatal("a refused notice was kept")
	}
	// one huge line and a progress bar made of carriage returns stay short
	n = stripeNotice("loja")
	n.Evidence = strings.Repeat("x", 10000) + "\r" + strings.Repeat("y", 10000)
	n.WhyNot = strings.Repeat("z", 10000)
	m := blocking.Build(n, blocking.Viewer{Level: users.Owner}, func(s string) string { return s })
	if len([]rune(m.Text)) > 1500 || strings.Count(m.Text, "```") != 2 {
		t.Fatalf("%d characters:\n%.200s", len([]rune(m.Text)), m.Text)
	}
	n.Evidence = " \t\n"
	if got := blocking.Build(n, blocking.Viewer{Level: users.Owner}, func(s string) string { return s }).Text; strings.Contains(got, "```") || !strings.Contains(got, "sem evidência verificada") {
		t.Fatalf("%s", got)
	}
	if strings.Contains(blocking.Full(n, func(s string) string { return s }), "Evidência") {
		t.Fatal("the full text shows an empty evidence")
	}
}
