package panel

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/i18n"
)

// The confirmation of a decree, the reason of the PIN and the order the engine worded
// reach the panel in the language of the user, and what the user wrote stays as he wrote it.
func TestAProposalReachesThePanelInTheLanguageOfTheUser(t *testing.T) {
	t.Parallel()
	p := decrees.Pending{
		ID: "p1", PIN: true, Order: "libere o npm",
		Msg:      i18n.M("decree.why_loosening_rule"),
		WhyMsg:   i18n.M("decree.why_loosening_rule"),
		OrderMsg: i18n.M("decree.order_summary_suggestion", "id", 3, "verb", i18n.M("decree.order_summary_allow"), "pattern", "npm *", "class", "dev"),
	}
	p.Text, p.Why = i18n.Render(i18n.PtBR, p.Msg), i18n.Render(i18n.PtBR, p.WhyMsg)
	en, pt := proposalOut("en", p), proposalOut(i18n.PtBR, p)
	if en.Text != "a permanent rule that frees" || pt.Text != "regra permanente que libera" || en.Why != en.Text || pt.Why != pt.Text {
		t.Errorf("en = %+v\npt = %+v", en, pt)
	}
	if !strings.HasPrefix(en.Order, "Suggestion 3 of the summary: allow") || !strings.HasPrefix(pt.Order, "Sugestão do resumo 3: permitir") {
		t.Errorf("order: en = %q, pt = %q", en.Order, pt.Order)
	}
	p.OrderMsg = i18n.Msg{}
	if got := proposalOut("en", p).Order; got != "libere o npm" {
		t.Errorf("the order of the user = %q", got)
	}
}
