// Package blocking builds the message that asks something of the users because a
// table can not go on without it (docs/aprovacoes/pedido-que-bloqueia.md): why
// it stopped, the evidence the engine has, what the agent already checked, why it
// does not decide, what to do, who acts and the reason in a fixed vocabulary. It
// makes the text and says which buttons each user may be given; the Telegram
// sends it (telegram.BlockChannel) and the panel shows the same fields.
package blocking

import (
	"fmt"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/users"
)

// Motive is the reason of the vocabulary.
type Motive string

// The motives, with the icon of each.
const (
	Credential    Motive = "credencial"
	Quota         Motive = "cota"
	Permission    Motive = "permissao"
	Ambiguity     Motive = "ambiguidade"
	Destructive   Motive = "destrutivo"
	HumanDecision Motive = "decisao_humana"
	OutOfScope    Motive = "fora_de_escopo"
	Duplicate     Motive = "duplicado"
	Waiting       Motive = "espera"
	OnlyYou       Motive = "so_voce"
)

var motives = []struct {
	m    Motive
	icon string
}{
	{Credential, "🔐"}, {Quota, "💳"}, {Permission, "🔑"}, {Ambiguity, "❓"}, {Destructive, "🛡"},
	{HumanDecision, "🧑"}, {OutOfScope, "🤷"}, {Duplicate, "👯"}, {Waiting, "⏳"}, {OnlyYou, "🙋"},
}

// Motives are the ten values, in the order of the document.
func Motives() []Motive {
	out := make([]Motive, len(motives))
	for i, m := range motives {
		out[i] = m.m
	}
	return out
}

// Icon is the icon of the motive ("" for one outside the vocabulary).
func (m Motive) Icon() string {
	for _, x := range motives {
		if x.m == m {
			return x.icon
		}
	}
	return ""
}

// UsageError is a value outside the vocabulary: the CLI answers it with the
// exit code 2 (`usage`) and this text, which lists the accepted values.
type UsageError struct{ Got string }

func (e *UsageError) Error() string {
	names := make([]string, 0, len(motives))
	for _, m := range Motives() {
		names = append(names, string(m))
	}
	return fmt.Sprintf("motivo %q fora do vocabulário; os valores aceitos são %s", e.Got, strings.Join(names, ", "))
}

// ParseMotive reads a motive; one outside the vocabulary is a *UsageError.
func ParseMotive(s string) (Motive, error) {
	m := Motive(strings.TrimSpace(s))
	if m.Icon() == "" {
		return "", &UsageError{Got: s}
	}
	return m, nil
}

// MotiveOf is the motive the engine deduces from the type of the request when it
// can: the agent chooses among the values only when the engine does not know.
func MotiveOf(t classes.RequestType, siteCredential bool) Motive {
	switch {
	case siteCredential:
		return Credential
	}
	switch t {
	case classes.RequestClassPermission, classes.RequestRepoSettings:
		return Permission
	case classes.RequestExtraCost:
		return Quota
	case classes.RequestDestructive:
		return Destructive
	}
	return HumanDecision
}

// StepKind is what a step of "para destravar" is, for the buttons.
type StepKind string

// The steps the engine knows how to help with.
const (
	// StepText is a step with no button.
	StepText StepKind = ""
	// StepVault is "register the secret in the vault": it can only be done in
	// the panel, by the Owner, with the PIN (docs/plataforma/cofre.md#uso).
	StepVault StepKind = "vault"
)

// Step is a step of the checklist.
type Step struct {
	Text string
	Kind StepKind
}

// MaxEvidenceLines is how many lines of the evidence the message shows.
const MaxEvidenceLines = 3

// Notice is a message that asks something of the users.
type Notice struct {
	Workspace, Mesa string
	Motive          Motive
	// Stopped is what is missing, in one line; Evidence what proves it (a
	// command and its output, `file:line`, an error) and Steps what the engine
	// knows to do. All of them are of the engine.
	Stopped  string
	Evidence string
	Steps    []Step
	// Owner is who acts: the name of the user, or the minimum level ("um
	// Administrador"), never "alguém".
	Owner string
	// BlockedBy are the numbers of the epics that must move first; empty: only
	// a person unblocks it.
	BlockedBy []string
	// Message, Checked, WhyNot and AgentSteps are the text of the agent: what it
	// says is stopped (when the engine does not know it), what it checked, why
	// it does not decide and the steps it adds.
	Message, Checked, WhyNot string
	AgentSteps               []string
	// Request is the approval request the notice is about, 0 for a plain notice.
	Request int64
}

// OwnerOf is who acts on a request of approval: the users the policy requires,
// by name, or, without them, the minimum level of the type.
func OwnerOf(require []string, min classes.Level) string {
	if len(require) > 0 {
		return strings.Join(require, ", ")
	}
	switch min {
	case classes.LevelOwner:
		return "o Senhor da Guilda"
	case classes.LevelAdmin:
		return "um Administrador"
	}
	return "um Aprovador"
}

// NoticeOwner is who acts on a notice, which has no type: the Owner of the
// workspace for a credential (the vault only takes it from the Owner) and an
// Administrator for the rest.
func NoticeOwner(m Motive, owner string) string {
	if m == Credential {
		return owner
	}
	return "um Administrador"
}

// MachineLine is the line for machines: `blocked-by: 42,43` or `blocked-by: -`.
func (n Notice) MachineLine() string {
	if len(n.BlockedBy) == 0 {
		return "blocked-by: -"
	}
	return "blocked-by: " + strings.Join(n.BlockedBy, ",")
}

// Viewer is the user a message is built for.
type Viewer struct {
	Level users.Level
}

// Button is a button the viewer is given.
type Button struct {
	Text string
	// Panel says it opens the panel (a link, not a callback).
	Panel bool
	// Details says it shows the whole evidence.
	Details bool
}

// Message is the text and the buttons for a user.
type Message struct {
	Text    string
	Buttons []Button
}

// Build makes the message for a viewer. mask takes the secrets out of the
// evidence before it is cut; a nil mask would let a secret through, so it is
// required. The text of the engine comes first; the text of the agent, after
// them, inside the block marked as not verified.
func Build(n Notice, v Viewer, mask func(string) string) Message {
	var b strings.Builder
	motive := n.Motive
	if motive.Icon() == "" {
		motive = HumanDecision
	}
	if n.Stopped != "" {
		fmt.Fprintf(&b, "%s %s\n", motive.Icon(), clip(maskedLine(n.Stopped, mask), MaxField))
	} else {
		fmt.Fprintf(&b, "%s %s\n", motive.Icon(), motive)
	}
	if ev := cut(mask(n.Evidence)); ev != "" {
		b.WriteString("Evidência:\n```\n" + ev + "\n```\n")
	}
	var buttons []Button
	if len(n.Steps) > 0 {
		b.WriteString("Para destravar:\n")
		for _, s := range n.Steps {
			text := maskedLine(s.Text, mask)
			if CanDo(s.Kind, v.Level) {
				text += " (painel)"
				buttons = append(buttons, Button{Text: "Abrir no painel", Panel: true})
			}
			fmt.Fprintf(&b, "[ ] %s\n", text)
		}
	}
	if n.Owner != "" {
		fmt.Fprintf(&b, "Dono: %s\n", engineLine(n.Owner))
	}
	if agent := agentBlock(n, mask); agent != "" {
		b.WriteString("— texto do agente, não verificado —\n" + agent)
	}
	buttons = append(buttons, Button{Text: "Ver detalhes", Details: true})
	text := strings.TrimRight(b.String(), "\n")
	if len(lines(mask(n.Evidence))) == 0 {
		// nothing the engine could put together: only the text of the agent
		text = strings.Replace(text, "\n", "\nsem evidência verificada\n", 1)
	}
	return Message{Text: text, Buttons: buttons}
}

// Full is the whole evidence, masked, for Ver detalhes and for the panel.
func Full(n Notice, mask func(string) string) string {
	var b strings.Builder
	if n.Stopped != "" {
		b.WriteString(maskedLine(n.Stopped, mask) + "\n")
	}
	if ls := lines(mask(n.Evidence)); len(ls) > 0 {
		b.WriteString("Evidência:\n```\n" + safeBlock(strings.Join(ls, "\n")) + "\n```\n")
	}
	if agent := agentBlock(n, mask); agent != "" {
		b.WriteString("— texto do agente, não verificado —\n" + agent)
	}
	return strings.TrimRight(b.String(), "\n")
}

// MaxLine is the longest line of the evidence the message shows, and MaxField the
// longest field of the agent, in characters.
const (
	MaxLine  = 300
	MaxField = 500
)

// clip cuts a text to n characters.
func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// lines splits the evidence at every break a reader may take for the end of a line.
func lines(ev string) []string {
	ev = strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(strings.TrimSpace(ev))
	if ev == "" {
		return nil
	}
	return strings.Split(ev, "\n")
}

// cut keeps the first lines of the evidence, each cut to a length, and says how
// many were left out.
func cut(ev string) string {
	ls := lines(ev)
	if len(ls) == 0 {
		return ""
	}
	rest := len(ls) - MaxEvidenceLines
	ls = ls[:min(len(ls), MaxEvidenceLines)]
	for i, l := range ls {
		ls[i] = clip(l, MaxLine)
	}
	out := safeBlock(strings.Join(ls, "\n"))
	if rest > 0 {
		out += fmt.Sprintf("\n… +%d linhas em Ver detalhes", rest)
	}
	return out
}

// safeBlock keeps a text from closing the fence it is shown in.
func safeBlock(s string) string { return strings.ReplaceAll(s, "```", "'''") }

// engineLine is a line of the engine that may carry a name or a text taken from
// somewhere else: one line, with no mark that the bot would turn into bold, code
// or a link.
func engineLine(s string) string { return plain(inbox.OneLine(s)) }

// maskedLine is a line of the engine with the secrets taken out. The marks of
// the bot are taken away before the mask looks (a secret split by an asterisk
// would pass it) and the mask looks once more after, for what the cleaning put
// together.
func maskedLine(s string, mask func(string) string) string {
	return engineLine(mask(engineLine(s)))
}

// Line is a text of the engine or of an agent as one safe line, masked.
func Line(s string, mask func(string) string) string { return clip(maskedLine(s, mask), MaxField) }

// plain takes away the marks of the Markdown of the bot (bold, code, links).
func plain(s string) string {
	return strings.NewReplacer("*", "", "`", "'", "[", "(", "]", ")").Replace(s)
}

// agentBlock is what the agent wrote, each part under a title of the engine and
// in one line: nothing it writes can open a title, a link or a field.
func agentBlock(n Notice, mask func(string) string) string {
	var b strings.Builder
	add := func(title, text string) {
		if strings.TrimSpace(text) != "" {
			fmt.Fprintf(&b, "%s: %s\n", title, clip(maskedLine(text, mask), MaxField))
		}
	}
	add("Parou porque", n.Message)
	add("Já conferi", n.Checked)
	add("Por que não decido", n.WhyNot)
	for _, s := range n.AgentSteps {
		if strings.TrimSpace(s) != "" {
			fmt.Fprintf(&b, "[ ] %s\n", clip(maskedLine(s, mask), MaxField))
		}
	}
	return b.String()
}
