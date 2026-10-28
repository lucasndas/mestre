package main

import (
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The campaign `loja` of the Guild: one table in each situation of the life of a table
// (docs/painel/vida-da-mesa.md), so the screens can be seen in every state. The table of
// the smoke test stays in the campaign `e2e` (view.go) and does not change.
const lojaWorkspace = "loja"

// lojaTable is a table of `loja` and the state the canvas of its screen shows.
type lojaTable struct {
	block panel.TableBlock
	state string
}

func lojaTables(now time.Time) []lojaTable {
	delivered := now.Add(-26 * time.Hour)
	recent := now.Add(-40 * time.Minute)
	party := []panel.ViewMachine{{Machine: "e2e", MachineName: "e2e", Characters: 3}}
	quest := &panel.ViewQuest{ID: "q1", Name: "entrega", Issue: 8, State: "running", Step: "Dev"}
	criteria := &panel.ViewCriteria{Total: 4, Proven: 2, Verified: 1}
	base := func(id string, epic int, title, state string) panel.TableBlock {
		return panel.TableBlock{
			ID: id, Epic: epic, EpicTitle: title, Branch: "epico/" + title, State: state,
			Party: party, PartySize: 3, Waits: []panel.ViewWait{}, LastDelivery: &recent,
		}
	}
	working := base("loja-trabalhando", 21, "login-com-google", "working")
	working.Quest, working.Criteria = quest, criteria
	quota := base("loja-esperando", 22, "carrinho", "working")
	quota.Quest = quest
	quota.Waits = []panel.ViewWait{{Kind: "quota", Reason: "o limite de uso da conta volta às 18:00"}}
	decision := base("loja-decisao", 23, "pagamento-pix", "working")
	decision.Quest = quest
	decision.Waits = []panel.ViewWait{{Kind: "approval", Reason: "usar mais US$ 20 de IA neste mês"}}
	stuck := base("loja-travada", 24, "frete", "working")
	stuck.Quest, stuck.LastDelivery = quest, &delivered
	stuck.Waits = []panel.ViewWait{{Kind: "same_failure", Reason: "o teste de frete falhou 3 vezes igual", Fingerprint: "TestFrete: esperado 12, veio 0", Where: "frete/calc.go:42"}}
	stalled := base("loja-parada", 25, "busca", "working")
	stalled.Quest, stalled.LastDelivery, stalled.Stalled, stalled.QuietSeconds = quest, &delivered, true, 5*3600
	review := base("loja-revisao", 26, "avaliacoes", "in_review")
	review.PR = 21
	closed := base("loja-entregue", 27, "pagina-de-contato", "closed")
	closed.Party, closed.PartySize, closed.LastDelivery, closed.PR = []panel.ViewMachine{}, 0, &delivered, 14
	paused := base("loja-pausada", 28, "cupons", "working")
	paused.Quest, paused.Paused = quest, true
	paused.Waits = []panel.ViewWait{{Kind: "paused", Reason: "Lucas pausou: esperar a decisão do dono sobre o desconto"}}
	cancelled := base("loja-cancelada", 29, "newsletter", "cancelled")
	cancelled.Party, cancelled.PartySize = []panel.ViewMachine{}, 0
	titles := map[string]string{
		"loja-trabalhando": "Login com Apple", "loja-esperando": "Carrinho de compras", "loja-decisao": "Pagamento por Pix",
		"loja-travada": "Cálculo de frete", "loja-parada": "Busca de produtos", "loja-revisao": "Avaliações de clientes",
		"loja-entregue": "Página Fale conosco", "loja-pausada": "Cupons de desconto", "loja-cancelada": "Newsletter",
	}
	out := []lojaTable{{working, "working"}, {quota, "working"}, {decision, "working"}, {stuck, "working"}, {stalled, "working"},
		{review, "in_review"}, {closed, "closed"}, {paused, "working"}, {cancelled, "cancelled"}}
	for i := range out {
		out[i].block.EpicTitle = titles[out[i].block.ID]
	}
	return out
}

// lojaState is the state of a table of `loja` for the canvas, "" when there is no such table.
func lojaState(id string) string {
	for _, t := range lojaTables(clock.Real{}.Now()) {
		if t.block.ID == id {
			return t.state
		}
	}
	return ""
}
