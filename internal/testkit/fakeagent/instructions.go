package fakeagent

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// What the fake agent "reads" as instructions and skills, for the answers
// of the conformance test (docs/provedores/conformidade.md): a `say` step
// may name it, and the fake answers with what the real CLI would have in
// its context.
//
//   - {{codes}}: the random codes (M-…, P-…) of the instructions the provider
//     gets: the Mestre layer through the channel of the provider and the
//     project's AGENTS.md;
//   - {{skill:<name>}}: the body of the skill, from the folders the provider
//     reads, or "skill <name> indisponível".
//
// FAKEAGENT_CLAUDE_AGENTS_SKILLS=1 makes the fake Claude Code also read
// .agents/skills/ (a version that reads it natively).

var (
	placeholderRe = regexp.MustCompile(`\{\{(codes|skill:[A-Za-z0-9_-]+)\}\}`)
	codeRe        = regexp.MustCompile(`\b[MP]-[A-Z0-9]{6}\b`)
)

// expand fills the placeholders of a `say` step.
func (a *agent) expand(text string) string {
	return placeholderRe.ReplaceAllStringFunc(text, func(m string) string {
		name := strings.TrimSuffix(strings.TrimPrefix(m, "{{"), "}}")
		if skill, ok := strings.CutPrefix(name, "skill:"); ok {
			return a.skillBody(skill)
		}
		var codes []string
		for _, c := range codeRe.FindAllString(a.instructions(), -1) {
			if !slices.Contains(codes, c) {
				codes = append(codes, c)
			}
		}
		return strings.Join(codes, " ")
	})
}

// instructions are the texts the provider gives the model: the Mestre layer
// by the channel of the provider and the AGENTS.md of the project.
func (a *agent) instructions() string {
	var b strings.Builder
	switch a.p {
	case Claude:
		b.WriteString(a.layerText())
	case Codex:
		for _, c := range a.configArgs {
			if v, ok := strings.CutPrefix(c, "developer_instructions="); ok {
				b.WriteString(v) // a TOML string: the codes read the same inside it
			}
		}
	case Antigravity:
		if a.agentName != "" {
			if raw, err := os.ReadFile(filepath.Join(a.dir, ".agents", "agents", a.agentName+".md")); err == nil {
				b.Write(raw)
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(a.dir, "AGENTS.md")); err == nil {
		b.WriteString("\n")
		b.Write(raw)
	}
	return b.String()
}

// skillDirs are the folders of skills the provider reads.
func (a *agent) skillDirs() []string {
	switch a.p {
	case Claude:
		dirs := []string{filepath.Join(a.dir, ".claude", "skills")}
		if a.env["FAKEAGENT_CLAUDE_AGENTS_SKILLS"] == "1" {
			dirs = append(dirs, filepath.Join(a.dir, ".agents", "skills"))
		}
		return dirs
	}
	return []string{filepath.Join(a.dir, ".agents", "skills")}
}

// skillBody is the body of a skill, without the frontmatter.
func (a *agent) skillBody(name string) string {
	for _, d := range a.skillDirs() {
		raw, err := os.ReadFile(filepath.Join(d, name, "SKILL.md"))
		if err != nil {
			continue
		}
		text := strings.ReplaceAll(string(raw), "\r\n", "\n")
		if rest, ok := strings.CutPrefix(text, "---\n"); ok {
			if _, body, found := strings.Cut(rest, "\n---\n"); found {
				text = body
			}
		}
		return strings.TrimSpace(text)
	}
	return "skill " + name + " indisponível"
}

// skipped reports whether FAKEAGENT_SKIP_HOOKS (a comma list of native
// events) says the fake never fires the event: a provider whose hook of that
// event does not run.
func (a *agent) skipped(event string) bool {
	return slices.Contains(strings.Split(a.env["FAKEAGENT_SKIP_HOOKS"], ","), event)
}
