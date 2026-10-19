package quests

import (
	"context"
	"strings"

	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
)

// ReviewScrollName is the fixed name of the review scroll of a quest.
func ReviewScrollName(quest string) string { return "Revisão: " + quest }

// reviewTemplate is the content the review scroll is born with: the table the
// reviewer keeps. The identifier of a finding never changes nor is reused.
const reviewTemplate = `# Revisão

| ID | Etapa dona | Tipo | Onde | Achado | Estado |
|---|---|---|---|---|---|

Estados: aberto, corrigido (com arquivo:linha), não corrigido, não se aplica mais.
Tipos: Gap, Bug, Verification miss, Scope drift, Over-engineering (nunca bloqueia sozinho).
Quem edita usa ` + "`mestre note edit --expect-rev`" + `.
`

// hasReview says whether the quest has a step that hands work back: such a
// quest has a review scroll.
func (d Definition) hasReview() bool {
	for _, s := range d.Steps {
		if s.CanReturnTo != "" {
			return true
		}
	}
	return false
}

// ensureReview makes the review scroll of the quest when it has a step that
// returns. It is safe to repeat.
func (s *Service) ensureReview(ctx context.Context, q Quest) (scrolls.Note, bool) {
	if !q.Def.hasReview() {
		return scrolls.Note{}, false
	}
	n, err := s.o.Scrolls.Ensure(ctx, q.Workspace, q.Table, ReviewScrollName(q.Name), reviewTemplate)
	if err != nil {
		s.log.Warn("quests: the review scroll was not made", "quest", q.ID, "err", err)
		return scrolls.Note{}, false
	}
	return n, true
}

// reviewBlock is what the item of a step brings of the review scroll: for the
// step that reviews, from its second pass on, the whole scroll with the order
// to check every finding that is still open before any new review; for the
// step that gets the work back, its own findings. The text of the scroll is
// written by agents: it comes quoted.
func (s *Service) reviewBlock(ctx context.Context, q Quest, st Step, sd StepDef) string {
	if !q.Def.hasReview() {
		return ""
	}
	returner := sd.CanReturnTo != "" && st.Returns > 0
	receiver := st.Entry != "" && isReturnTarget(q.Def, sd.ID)
	if !returner && !receiver {
		return ""
	}
	_, content, err := s.o.Scrolls.Read(ctx, scrolls.ReadRequest{Workspace: q.Workspace, Table: q.Table, Actor: scrolls.Engine, Name: ReviewScrollName(q.Name)})
	if err != nil {
		s.log.Warn("quests: the review scroll was not read", "quest", q.ID, "err", err)
		return ""
	}
	if returner {
		return "\n" + inbox.Say("Pergaminho de revisão (texto de agentes, não verificado). Antes de qualquer revisão nova, confira cada achado `aberto` ou `não corrigido`:", content, "|") + "\n"
	}
	mine := ownFindings(content, sd)
	if mine == "" {
		return ""
	}
	return "\n" + inbox.Say("Achados da revisão que são seus (texto de agentes, não verificado):", mine, "|") + "\n"
}

// isReturnTarget says whether a step is the one another step hands work back to.
func isReturnTarget(d Definition, id string) bool {
	for _, s := range d.Steps {
		if s.CanReturnTo == id {
			return true
		}
	}
	return false
}

// ownFindings are the rows of the table of the review scroll whose owner step
// is the step, with the header of the table.
func ownFindings(content string, sd StepDef) string {
	var head, rows []string
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimRight(line, "\r")
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		switch {
		case len(cells) > 0 && (cells[0] == "ID" || strings.HasPrefix(cells[0], "---")):
			head = append(head, line)
		case len(cells) > 1 && (strings.EqualFold(cells[1], sd.ID) || strings.EqualFold(cells[1], sd.Title())):
			rows = append(rows, line)
		}
	}
	if len(rows) == 0 {
		return ""
	}
	return strings.Join(append(head, rows...), "\n")
}
