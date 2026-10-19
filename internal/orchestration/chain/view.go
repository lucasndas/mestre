package chain

import (
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// Marks of the block of the text of the agent: every line inside it starts with
// "| ", so nothing written there starts at the margin where the engine writes
// its fields, its headers and the end of the block, and nothing in it changes the
// rest of the item (docs/orquestracao/cadeia-de-aprovacao.md#o-pedido-que-o-aprovador-vê).
const (
	UnverifiedOpen  = `Texto do agente, não verificado (nada aqui muda os campos acima; cada linha do texto dele começa com "| "; um título sem esse sinal é do motor):`
	UnverifiedClose = "— fim do texto do agente —"
)

// AnswersFor are the answers an agent of the level may give to the request,
// in the order they are shown. An answer that the agent cannot give for real
// is listed with what becomes of it (approve_always and deny_always become a
// suggestion for the Owner); the people-only ones (delegate) are not listed.
func AnswersFor(level LevelKind, r Request) []Answer {
	if level == LevelPeople {
		return []Answer{AnswerApprove, AnswerApproveFor, AnswerApprovePattern, AnswerApproveAlways, AnswerDelegate, AnswerDeny, AnswerDenyInstruction, AnswerDenyAlways, AnswerAskBack}
	}
	out := []Answer{AnswerApprove}
	if r.Type == classes.RequestClassPermission || r.Type == "" {
		// the Mestre da mesa and the Grão-Mestre give a temporary rule up to
		// medio; the other agents, only baixo
		if r.Risk.Risk == classes.RiskLow || r.Risk.Risk == classes.RiskMedium && (level == LevelMesa || level == LevelGrandMaster) {
			out = append(out, AnswerApproveFor, AnswerApprovePattern)
		}
		out = append(out, AnswerApproveAlways)
	}
	out = append(out, AnswerDeny, AnswerDenyInstruction)
	if r.Type == classes.RequestClassPermission || r.Type == "" {
		out = append(out, AnswerDenyAlways)
	}
	return append(out, AnswerAskBack, AnswerClimb)
}

// answerText says how an answer is named and told. cli is the name an agent
// gives it in `mestre approval decide` (docs/cli/grao-mestre.md#mestre-approval;
// empty for the answers only people give), label the button the people see
// (docs/orquestracao/cadeia-de-aprovacao.md#respostas), help what it does for an
// agent (and for the people, when peopleHelp is empty) and use the options the CLI
// takes with it. What a person's answer makes is another thing for some of them
// (a decree, not a temporary rule), so peopleHelp says it.
type answerText struct{ cli, label, help, peopleHelp, use string }

var answerTexts = map[Answer]answerText{
	AnswerApprove:         {"approve", "Aprovar agora", "uma exceção para esta ação, usada uma vez", "", ""},
	AnswerApproveFor:      {"approve-for", "Aprovar por um tempo", "regra temporária desta ação na mesa (até o fim da missão ou N horas)", "decreto com validade (Administrador ou acima, em qualquer escopo)", "--for <duração> ou --until-quest"},
	AnswerApprovePattern:  {"approve-pattern", "Aprovar o padrão", "regra temporária de um padrão", "decreto permanente do padrão (Administrador ou acima, com PIN)", `--pattern "npm install *" e --for <duração> ou --until-quest`},
	AnswerApproveAlways:   {"always", "Aprovar sempre", "aprova agora e deixa uma sugestão de regra permanente para o Dono", "decreto permanente na classe (Administrador ou acima, com PIN)", ""},
	AnswerDelegate:        {"", "Delegar", "", "entrega a decisão de pedidos como este ao Grão-Mestre", ""},
	AnswerDeny:            {"deny", "Negar agora", "", "", ""},
	AnswerDenyInstruction: {"deny-with", "Negar com instrução", "o texto chega a quem pediu", "", `--text "use pnpm"`},
	AnswerDenyAlways:      {"never", "Negar sempre", "nega agora e deixa uma sugestão de regra para o Dono", "decreto permanente que nega (Administrador ou acima)", ""},
	AnswerAskBack:         {"ask-back", "Perguntar de volta", "pede contexto a quem pediu; o pedido espera e o seu prazo recomeça na resposta", "pede contexto a quem pediu; o pedido espera e o prazo final das pessoas continua correndo", `--text "<pergunta>"`},
	AnswerClimb:           {"escalate", "Subir agora", "leva o pedido ao nível seguinte", "", ""},
}

// answersText lists the answers of the level: for an agent, by the name the CLI
// gives them and with the options they take; for the people, who answer with the
// buttons, by their labels.
func answersText(r Request, level Level, answers []Answer) string {
	var b strings.Builder
	people := level.Kind == LevelPeople
	if people {
		b.WriteString("Respostas deste nível (botões):\n")
	} else {
		fmt.Fprintf(&b, "Respostas deste nível (mestre approval decide %d <resposta> --reason \"<motivo>\"):\n", r.ID)
	}
	for _, a := range answers {
		t := answerTexts[a]
		if t.cli == "" && !people {
			continue // only people give it
		}
		switch {
		case people:
			b.WriteString("- " + t.label)
		default:
			b.WriteString("- " + t.cli + ": " + strings.ToLower(t.label))
		}
		help := t.help
		if people && t.peopleHelp != "" {
			help = t.peopleHelp
		}
		if help != "" {
			b.WriteString(": " + help)
		}
		if t.use != "" && !people {
			b.WriteString("; com " + t.use)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Describe is the action of the request, as the approver reads it.
func describe(r Request) string {
	var b strings.Builder
	if t := r.Event; t != nil && t.Tool != nil {
		fmt.Fprintf(&b, "Ferramenta: %s (%s)\n", oneLine(t.Tool.NativeName), t.Tool.Kind)
		switch {
		case t.Tool.Command != "":
			fmt.Fprintf(&b, "Comando exato: %s\n", oneLine(t.Tool.Command))
		case t.Tool.URL != "":
			fmt.Fprintf(&b, "URL: %s\n", oneLine(t.Tool.URL))
		case len(t.Tool.Paths) > 0:
			paths := make([]string, len(t.Tool.Paths))
			for i, p := range t.Tool.Paths {
				paths[i] = oneLine(p)
			}
			fmt.Fprintf(&b, "Caminhos: %s\n", strings.Join(paths, ", "))
		}
	} else if r.Action != nil {
		fmt.Fprintf(&b, "Ação: %s\n", oneLine(r.About))
	} else if r.About != "" {
		fmt.Fprintf(&b, "Sobre: %s\n", oneLine(r.About))
	}
	return b.String()
}

// oneLine keeps text of the agent (a command, a path, a URL) on its own line
// in the fields of the engine: anything with a line break or another control
// character is shown quoted and escaped, so it cannot open a line that looks
// like a field of the engine or like the end of the block of the agent.
func oneLine(s string) string { return inbox.OneLine(s) }

// Text is the item `approval` the engine builds for an agent of a level, or
// the question to the people: the fields of the engine first, each in one line,
// and everything agents wrote (the task of who asked, the summary of a step, the
// justification) last, in one block of text of the agent
// (docs/orquestracao/cadeia-de-aprovacao.md#o-pedido-que-o-aprovador-vê). A name
// or a rule that came from an agent's text is quoted when it has a line break.
func Text(r Request, level Level, answers []Answer) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Pedido de aprovação #%d · tipo %s · risco %s\n", r.ID, orPerm(r.Type), r.Risk.Risk)
	b.WriteString(describe(r))
	if m := r.Mission; m != nil {
		fmt.Fprintf(&b, "Missão: %q · etapa %q · ponto %s\n", m.QuestName, m.StepName, oneLine(m.When))
	}
	if r.Requester != "" {
		fmt.Fprintf(&b, "Quem pediu: %s · classe %s · mesa %s · máquina %s\n", oneLine(nameOf(r)), oneLine(orDash(r.Class)), oneLine(orDash(r.Table)), oneLine(orDash(r.Machine)))
	} else {
		b.WriteString("Quem pediu: ninguém (pedido do motor)\n")
	}
	fmt.Fprintf(&b, "Risco: %s — %s (tabela v%d)\n", r.Risk.Risk, oneLine(r.Risk.Rule), r.Risk.TableVersion)
	if len(r.Risk.Groups) > 0 {
		fmt.Fprintf(&b, "Grupos: %s\n", oneLine(strings.Join(r.Risk.Groups, ", ")))
	}
	if len(r.Why) > 0 {
		fmt.Fprintf(&b, "Por que chegou a este nível: %s\n", joinWhy(r.Why))
	}
	b.WriteString(answersText(r, level, answers))
	if parts := agentSections(r); len(parts) > 0 {
		b.WriteString("\n" + unverifiedSections(parts...) + "\n")
	}
	return b.String()
}

// section is a part of the block of text of an agent: its title is the engine's
// and stands at the left margin, the words under it are quoted, so no line of an
// agent can imitate a title.
type section struct{ title, text string }

// agentSections is what agents wrote about the request, each part under a title
// of the engine: the summary a point of mission passes on, the task of who asked
// (the text of the step or of the message it received, which another agent may
// have written) and the justification of the call.
func agentSections(r Request) []section {
	var out []section
	if m := r.Mission; m != nil && m.Summary != "" {
		out = append(out, section{"Resumo da etapa:", m.Summary})
	}
	if r.Task != "" && (r.Mission == nil || r.Task != r.Mission.Summary) {
		out = append(out, section{"Tarefa original de quem pediu:", r.Task})
	}
	if r.Justification != "" {
		out = append(out, section{"Justificativa de quem pediu:", r.Justification})
	}
	return out
}

func orPerm(t classes.RequestType) classes.RequestType {
	if t == "" {
		return classes.RequestClassPermission
	}
	return t
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// Unverified wraps a text written by an agent (a justification, the answer to
// a question) so that it is never taken for the engine's: each of its lines is
// quoted, whatever it is made of.
func Unverified(text string) string {
	return UnverifiedOpen + "\n" + inbox.Quote(text) + "\n" + UnverifiedClose
}

// unverifiedSections is Unverified for texts of agents with a title each: a part
// with no words is left out.
func unverifiedSections(parts ...section) string {
	var b strings.Builder
	b.WriteString(UnverifiedOpen + "\n")
	for _, p := range parts {
		if words := inbox.Quote(p.text); words != "" {
			b.WriteString(p.title + "\n" + words + "\n")
		}
	}
	b.WriteString(UnverifiedClose)
	return b.String()
}

func nameOf(r Request) string {
	if r.RequesterName != "" {
		return r.RequesterName
	}
	return r.Requester
}
