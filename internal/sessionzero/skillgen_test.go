package sessionzero_test

import (
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

// allSkills are the four generated skills of a definition of commands.
func allSkills(reg *cli.Registry) []sessionzero.Skill {
	shared, levels := sessionzero.GenerateSkills(reg)
	out := []sessionzero.Skill{shared}
	for _, name := range []string{"mestre-coordenacao-mesa", "mestre-grao-mestre", "mestre-rituais"} {
		out = append(out, levels[name])
	}
	return out
}

var (
	codeSpan  = regexp.MustCompile("`([^`\n]+)`")
	flagToken = regexp.MustCompile(`--[a-z][a-z-]*`)
	// globalFlags belong to every command (docs/cli/convencoes.md).
	globalFlags = []string{"--json", "--help", "--debug"}
)

// citations lists what a skill cites that the definition does not have: a
// command after `mestre`, a flag of that command, or a flag no command has.
func citations(reg *cli.Registry, body string) []string {
	var bad []string
	anyFlag := map[string]bool{}
	for _, s := range reg.Specs() {
		for _, f := range s.Flags {
			anyFlag["--"+f.Name] = true
		}
		for _, f := range s.Internal {
			anyFlag["--"+f.Name] = true
		}
	}
	for _, m := range codeSpan.FindAllStringSubmatch(body, -1) {
		span := m[1]
		for _, alt := range strings.Split(span, "  |  ") {
			flags := flagToken.FindAllString(alt, -1)
			if !strings.HasPrefix(alt, "mestre ") {
				for _, f := range flags {
					if !anyFlag[f] && !slices.Contains(globalFlags, f) {
						bad = append(bad, "flag "+f+" in `"+span+"`")
					}
				}
				continue
			}
			spec, _, ok := reg.Resolve(strings.Fields(strings.TrimPrefix(alt, "mestre ")))
			if !ok {
				bad = append(bad, "command in `"+alt+"`")
				continue
			}
			for _, f := range flags {
				if _, has := specFlag(spec, f); !has && !slices.Contains(globalFlags, f) {
					bad = append(bad, "flag "+f+" of `"+spec.Name+"`")
				}
			}
		}
	}
	return bad
}

func specFlag(s cli.Spec, flag string) (cli.Flag, bool) {
	for _, f := range append(slices.Clone(s.Flags), s.Internal...) {
		if "--"+f.Name == flag {
			return f, true
		}
	}
	return cli.Flag{}, false
}

// CA-CLI-SKL-02: every command and flag the skills cite exists, a command
// taken out of the definition is caught, and the skills teach the commands of
// the Grand Master, of GitHub, of the decrees and of the approvals.
func TestEveryCommandAndFlagTheSkillsCiteIsInTheDefinition(t *testing.T) {
	t.Parallel()
	reg := cli.Default()
	for _, s := range allSkills(reg) {
		if bad := citations(reg, s.Body); len(bad) != 0 {
			t.Errorf("%s cites what the definition does not have: %v", s.Name, bad)
		}
		if n := len(s.Body); n == 0 || n > sessionzero.SkillMaxBytes {
			t.Errorf("%s: body of %d bytes", s.Name, n)
		}
	}
	_, levels := sessionzero.GenerateSkills(reg)
	for skill, want := range map[string][]string{
		"mestre-grao-mestre":      {"mestre gh review", "mestre gh approve", "mestre gh merge", "mestre gh settings", "mestre app list", "mestre decree propose", "mestre approval decide", "mestre approval ask"},
		"mestre-coordenacao-mesa": {"mestre issue create", "mestre pr open"},
	} {
		for _, w := range want {
			if !strings.Contains(levels[skill].Body, w) {
				t.Errorf("%s does not teach %q", skill, w)
			}
		}
	}
	// A command taken out of the definition leaves a citation the check finds
	// when the text of the skill is the one of the full definition.
	var rest []cli.Spec
	for _, s := range reg.Specs() {
		if s.Name != "ask" {
			rest = append(rest, s)
		}
	}
	smaller := cli.MustRegistry(rest...)
	shared, _ := sessionzero.GenerateSkills(reg)
	if bad := citations(smaller, shared.Body); !slices.ContainsFunc(bad, func(b string) bool { return strings.Contains(b, "ask") }) {
		t.Errorf("a removed command was not caught: %v", bad)
	}
}

// A skill is made from the definition: a command that leaves it leaves the
// skill, and a line that talks only about it too; the syntax is the one of
// --help.
func TestTheSkillsFollowTheDefinitionOfCommands(t *testing.T) {
	t.Parallel()
	reg := cli.Default()
	shared, levels := sessionzero.GenerateSkills(reg)
	spec, _ := reg.Get("recruit")
	if !strings.Contains(levels["mestre-coordenacao-mesa"].Body, spec.Usage) {
		t.Errorf("the usage of recruit is not the one of the definition:\n%s", levels["mestre-coordenacao-mesa"].Body)
	}
	var rest []cli.Spec
	for _, s := range reg.Specs() {
		if s.Name != "ritual run" && s.Name != "check" {
			rest = append(rest, s)
		}
	}
	_, after := sessionzero.GenerateSkills(cli.MustRegistry(rest...))
	if strings.Contains(after["mestre-rituais"].Body, "mestre ritual run") || strings.Contains(after["mestre-rituais"].Body, "`mestre ritual list`, `show`") {
		t.Errorf("the ritual skill still cites ritual run:\n%s", after["mestre-rituais"].Body)
	}
	shared2, _ := sessionzero.GenerateSkills(cli.MustRegistry(rest...))
	if shared.Body == shared2.Body || strings.Contains(shared2.Body, "`mestre check`") {
		t.Errorf("the communication skill did not follow the definition")
	}
}

// Each level gets its own orientation: the Grand Master is not taught to
// coordinate a table, and the table master is not taught the commands only
// the Grand Master uses.
func TestEachLevelSkillCarriesOnlyTheCommandsOfItsLevel(t *testing.T) {
	t.Parallel()
	reg := cli.Default()
	_, levels := sessionzero.GenerateSkills(reg)
	if strings.Contains(levels["mestre-coordenacao-mesa"].Body, "mestre table open") {
		t.Error("the table master skill teaches table open")
	}
	if strings.Contains(levels["mestre-grao-mestre"].Body, "mestre quest run") {
		t.Error("the Grand Master skill teaches quest run")
	}
	for name, s := range levels {
		if !strings.HasPrefix(name, "mestre-") || s.Name != name {
			t.Errorf("skill %q named %q", name, s.Name)
		}
	}
}

// When a command changes in the definition the generated skill of the worktree
// is rewritten at the next generation, and only it.
func TestAShippedSkillIsRegeneratedWhenTheCommandsChange(t *testing.T) {
	t.Parallel()
	e := newTableEnv(t, nil, nil)
	e.prepare()
	var rest []cli.Spec
	for _, s := range cli.Default().Specs() {
		if s.Name != "inbox" {
			rest = append(rest, s)
		}
	}
	shared, _ := sessionzero.GenerateSkills(cli.MustRegistry(rest...))
	e.opts.Worktree.Skill = sessionzero.SkillFile(shared, sessionzero.ModelVersion)
	e.rebuild()
	rep := e.prepare()
	if !slices.Equal(rep.Written, []string{".agents/skills/mestre-comunicacao/SKILL.md", ".claude/skills/mestre-comunicacao/SKILL.md"}) {
		t.Fatalf("written = %v", rep.Written)
	}
	if got := read(t, e.dir+"/.agents/skills/mestre-comunicacao/SKILL.md"); strings.Contains(got, "mestre inbox") {
		t.Errorf("the regenerated skill still cites the removed command:\n%s", got)
	}
	if again := e.prepare(); len(again.Written) != 0 {
		t.Errorf("a second generation wrote %v", again.Written)
	}
}
