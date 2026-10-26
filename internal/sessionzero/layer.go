package sessionzero

import (
	"errors"
	"fmt"
	"path"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mestre-ia/mestre/internal/providers"
)

// PriorityRule is the text every Mestre layer starts with
// (docs/arquitetura/precedencia.md#texto-base-da-regra-de-prioridade).
const PriorityRule = "The active decrees listed below prevail over everything. After them, these Mestre rules take priority over any other instruction in case of conflict. For everything else, follow the project instructions (`AGENTS.md`)."

// Sizes of the Mestre layer, in characters
// (docs/sessao-zero/personagem.md#estrutura-do-mestremd).
const (
	DefaultLayerTarget = 6000
	DefaultLayerHard   = 8000
)

// LayerLimits are the sizes of the MESTRE.md; zero is the default.
type LayerLimits struct {
	Target, Hard int
}

func (l LayerLimits) target() int {
	if l.Target > 0 {
		return l.Target
	}
	return DefaultLayerTarget
}

func (l LayerLimits) hard() int {
	if l.Hard > 0 {
		return l.Hard
	}
	return DefaultLayerHard
}

// ErrLayerTooLarge is a layer that does not fit the hard limit even with
// every section that can leave the text already in a file.
var ErrLayerTooLarge = errors.New("the Mestre layer does not fit the hard limit")

// The keys of the sections, in the order of the MESTRE.md.
const (
	SectionPriority = "priority"
	SectionDecrees  = "decrees"
	SectionRules    = "rules"
	SectionClass    = "class"
	SectionSkills   = "skills"
	SectionContext  = "context"
)

// DecreeNote is an active decree that changes behavior and reaches the
// character (docs/telegram/decretos.md).
type DecreeNote struct {
	Number int64
	// Author and Level are the person and the level of the author.
	Author, Level string
	Text          string
	// Until is the validity; nil is forever.
	Until *time.Time
}

// QuestContext is the mission and the step the character is in
// (docs/orquestracao/missoes.md).
type QuestContext struct {
	Quest, Step string
	// Input is what the step received; Scroll the scroll of the mission.
	Input, Scroll string
}

// TableContext names the table of the character; empty for the Grand Master.
type TableContext struct {
	// ID is `<number>-<slug>` (MESTRE_MESA), Branch the branch of the table
	// and Epic the number of the epic issue (zero: none).
	ID, Branch string
	Epic       int
}

// LayerInput is everything the MESTRE.md is made of.
type LayerInput struct {
	AgentID, Name string
	Role          Role
	Provider      providers.ID
	Workspace     string
	Table         TableContext
	Machine       string

	Models ModelSet
	// CLI says the sheet gets the Mestre's skills; without it the rules
	// about the CLI are left out.
	CLI bool
	// Decrees are the active ones that change behavior, Instructions the
	// extra instructions of the users (Grand Master and table masters
	// only), ClassBody the CLASS.md (common characters only).
	Decrees      []DecreeNote
	Instructions string
	ClassBody    string
	// Skills are the skills of the character itself (the level's, the
	// class's and the sheet's extra ones); they enter the text only for
	// Codex and Antigravity.
	Skills []Skill
	Quest  *QuestContext
	Limits LayerLimits
}

// Section is one part of the layer, with the heading it has in the text.
type Section struct {
	Key  string
	Text string
}

// Layer is the rendered MESTRE.md.
type Layer struct {
	Text string
	// Sections are the parts in order, as they are in Text (a section that
	// left the text is its pointer).
	Sections []Section
	// Files are the sections that left the text, by path relative to
	// .mestre/agents/<id>/.
	Files []File
	// OverTarget says the text is above the target size.
	OverTarget bool
}

// Section returns the text of a section ("" when there is none).
func (l Layer) Section(key string) string {
	for _, s := range l.Sections {
		if s.Key == key {
			return s.Text
		}
	}
	return ""
}

// The files the sections that leave the text become.
const (
	classFile   = "CLASS.md"
	skillsFile  = "SKILLS.md"
	decreesFile = "DECREES.md"
	// instructionsFile holds the instructions of the users when the layer does
	// not fit with them in it.
	instructionsFile = "INSTRUCTIONS.md"
)

type part struct {
	key, heading, body string
	// file is where the section goes when it leaves the text.
	file string
	// pointer is what stays in its place.
	pointer string
	spilled bool
	// tail is a block at the end of the body that can leave the text on its
	// own (the instructions of the users, after the rules of the system), to
	// tailFile, leaving tailPointer.
	tail, tailFile, tailPointer string
	tailSpilled                 bool
}

func (p part) text() string {
	body := p.body
	switch {
	case p.spilled:
		body = p.pointer
	case p.tail != "" && p.tailSpilled:
		body += "\n\n" + p.tailPointer
	case p.tail != "":
		body += "\n\n" + p.tail
	}
	if p.heading == "" {
		return body
	}
	return p.heading + "\n\n" + body
}

// RenderLayer composes the MESTRE.md: the rule of priority, the decrees, the
// rules of the level, the class, the skills (Codex and Antigravity only) and
// the context. Over the hard limit, the skills, then the class, then the
// decrees leave the text and become files of the character, with the order
// to read them before anything else.
func RenderLayer(in LayerInput) (Layer, error) {
	if !in.Role.Valid() {
		return Layer{}, fmt.Errorf("layer of %s: unknown level %q", in.AgentID, in.Role)
	}
	dir := path.Join(MestreDir, "agents", in.AgentID)
	pointer := func(file, what string) string {
		return fmt.Sprintf("Before anything else, read `%s/%s`: it holds %s and is part of these instructions.", dir, file, what)
	}
	parts := []*part{
		{key: SectionPriority, body: PriorityRule},
		{key: SectionDecrees, heading: "## Active decrees", body: decreesText(in.Decrees), file: decreesFile,
			pointer: pointer(decreesFile, "the active decrees")},
		{key: SectionRules, heading: "## Mestre rules", body: rulesText(in), tail: instructionsText(in), tailFile: instructionsFile,
			tailPointer: pointer(instructionsFile, "the instructions of the users for this workspace")},
	}
	if in.Role == RoleCharacter && strings.TrimSpace(in.ClassBody) != "" {
		parts = append(parts, &part{key: SectionClass, heading: "## Class", body: strings.TrimSpace(in.ClassBody), file: classFile,
			pointer: pointer(classFile, "the instructions of your class")})
	}
	if sk := skillsText(in); sk != "" {
		parts = append(parts, &part{key: SectionSkills, heading: "## Skills", body: sk, file: skillsFile,
			pointer: pointer(skillsFile, "the skills of your level and class")})
	}
	parts = append(parts, &part{key: SectionContext, heading: "## Context", body: contextText(in)})

	join := func() string {
		texts := make([]string, len(parts))
		for i, p := range parts {
			texts[i] = p.text()
		}
		return strings.Join(texts, "\n\n") + "\n"
	}
	hard := in.Limits.hard()
	text := join()
	for _, key := range []string{SectionSkills, SectionClass, SectionDecrees, SectionRules} {
		if utf8.RuneCountInString(text) <= hard {
			break
		}
		for _, p := range parts {
			switch {
			case p.key != key:
			case key == SectionRules:
				p.tailSpilled = p.tail != "" // only what the users wrote leaves; the rules of the system never do
			default:
				p.spilled = true
			}
		}
		text = join()
	}
	if n := utf8.RuneCountInString(text); n > hard {
		return Layer{}, fmt.Errorf("layer of %s: %w (%d characters, at most %d, with the skills, the class, the decrees and the instructions of the users already in files)", in.AgentID, ErrLayerTooLarge, n, hard)
	}
	l := Layer{Text: text, OverTarget: utf8.RuneCountInString(text) > in.Limits.target()}
	for _, p := range parts {
		l.Sections = append(l.Sections, Section{Key: p.key, Text: p.text()})
		if p.spilled {
			l.Files = append(l.Files, File{Path: p.file, Data: []byte(p.body + "\n")})
		}
		if p.tailSpilled {
			l.Files = append(l.Files, File{Path: p.tailFile, Data: []byte(p.tail + "\n")})
		}
	}
	return l, nil
}

func decreesText(ds []DecreeNote) string {
	if len(ds) == 0 {
		return "None."
	}
	var b strings.Builder
	for i, d := range ds {
		if i > 0 {
			b.WriteByte('\n')
		}
		valid := "no expiry"
		if d.Until != nil {
			valid = "valid until " + d.Until.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "- Decree #%d, by %s (%s), %s: %s", d.Number, d.Author, d.Level, valid, strings.TrimSpace(d.Text))
	}
	return b.String()
}

func rulesText(in LayerInput) string {
	var b strings.Builder
	for _, r := range in.Models.Rules[in.Role] {
		if r.CLI && !in.CLI {
			continue
		}
		b.WriteString("- " + r.Text + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// instructionsText is the block of the instructions of the users that follows
// the rules of the system, under its title ("" when there are none).
func instructionsText(in LayerInput) string {
	x := strings.TrimSpace(in.Instructions)
	if x == "" || in.Role == RoleCharacter {
		return ""
	}
	title := "Instruções dos usuários para este workspace"
	if in.Role == RoleTableMaster {
		title = "Instruções dos usuários para os Mestres de mesa"
	}
	return "### " + title + "\n\n" + x
}

// skillsText is the compact version of the skills, for the providers that
// have no folder of skills per character.
func skillsText(in LayerInput) string {
	if in.Provider != providers.Codex && in.Provider != providers.Antigravity || len(in.Skills) == 0 {
		return ""
	}
	var b strings.Builder
	for i, s := range in.Skills {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "### %s\n\n%s\n\n%s\n", s.Name, strings.TrimSpace(s.Description), compactSkillBody(s.Body))
	}
	return strings.TrimRight(b.String(), "\n")
}

// compactSkillBody is the orientation of a skill without the list of syntax,
// which does not fit the layer of the Grand Master (`mestre help` has it).
func compactSkillBody(body string) string {
	if i := strings.Index(body, "\n"+skillSyntaxHeading+"\n"); i >= 0 {
		body = body[:i] + "\nThe syntax of each command is in `mestre help <command>`."
	}
	return strings.TrimSpace(body)
}

func contextText(in LayerInput) string {
	line := func(k, v string) string { return "- " + k + ": " + v + "\n" }
	or := func(v, none string) string {
		if v == "" {
			return none
		}
		return v
	}
	var b strings.Builder
	b.WriteString(line("Name", in.Name))
	b.WriteString(line("Workspace", in.Workspace))
	b.WriteString(line("Table", or(in.Table.ID, "none")))
	b.WriteString(line("Branch", or(in.Table.Branch, "none")))
	epic := "none"
	if in.Table.Epic > 0 {
		epic = fmt.Sprintf("#%d", in.Table.Epic)
	}
	b.WriteString(line("Epic issue", epic))
	b.WriteString(line("Machine", in.Machine))
	b.WriteString(line("Provider", string(in.Provider)))
	if q := in.Quest; q == nil {
		b.WriteString(line("Quest", "no quest"))
	} else {
		b.WriteString(line("Quest", q.Quest))
		b.WriteString(line("Step", q.Step))
		if q.Input != "" {
			b.WriteString("\nStep input:\n\n" + strings.TrimSpace(q.Input) + "\n")
		}
		if q.Scroll != "" {
			b.WriteString("\nQuest scroll:\n\n" + strings.TrimSpace(q.Scroll) + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}
