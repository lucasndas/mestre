package quests

import (
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// stamp is the time as the scroll writes it.
func (s *Service) stamp() string { return s.clk.Now().UTC().Format("2006-01-02 15:04") }

// roundSection opens a round in the scroll of the quest.
func (s *Service) roundSection(q Quest) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Rodada %d · iniciada — %s\n", q.Round, s.stamp())
	if q.Input != "" {
		b.WriteString(inbox.Say("**Entrada:**", q.Input, ">") + "\n")
	}
	return b.String()
}

// stepSection is the section the engine adds to the scroll of the quest when a
// step ends (docs/orquestracao/pergaminhos-e-tomos.md#pergaminho-da-missão):
// the summary of the done and the files, as the version control says them.
func (s *Service) stepSection(q Quest, st Step, sd StepDef, r Run, o outcome) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Rodada %d · %d. %s — %s — %s\n", q.Round, st.Pos+1, sd.ID, r.CharacterName, s.stamp())
	b.WriteString(inbox.Say("**Resumo:**", orNone(r.Summary), ">") + "\n")
	fmt.Fprintf(&b, "**Arquivos:** %s\n", inbox.OneLine(orNone(capList(r.Files, maxListed))))
	fmt.Fprintf(&b, "**Não commitados:** %s\n", inbox.OneLine(orNone(capList(r.Uncommitted, maxListed))))
	switch o.kind {
	case doneReturned:
		b.WriteString(inbox.Say("**Devolvida para:** "+o.returnedTo+" —", o.reason, ">") + "\n")
	case doneLimit:
		b.WriteString(inbox.Say("**Limite de voltas:** a devolução não aconteceu; a missão ficou pausada. Motivo da devolução:", o.reason, ">") + "\n")
	case doneApproval:
		b.WriteString("**Aprovação:** espera a aprovação antes de passar adiante.\n")
	}
	return b.String()
}

// maxListed is how many files a list in the scroll or in an item names; the
// rest is counted. A summary of an agent has its own limit; the changed files
// come from the version control and can be thousands.
const maxListed = 100

// capList joins the first n items and says how many were left out.
func capList(l []string, n int) string {
	if len(l) <= n {
		return strings.Join(l, ", ")
	}
	return strings.Join(l[:n], ", ") + fmt.Sprintf(" … e mais %d", len(l)-n)
}

func orNone(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(nenhum)"
	}
	return s
}
