package sessionzero_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/orchestration/accounts"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
)

var threeAgents = []string{presets.AgentClaudeCode, presets.AgentCodex, presets.AgentAntigravity}

// CA-SZ-PERS-01
func TestMestreMDStartsWithThePriorityRuleAndKeepsTheOrderOfTheSections(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	decree := sessionzero.DecreeNote{Number: 7, Author: "Lucas", Level: "owner", Text: "No deploys on Friday."}
	for _, agent := range threeAgents {
		for _, role := range []sessionzero.Role{sessionzero.RoleCharacter, sessionzero.RoleTableMaster, sessionzero.RoleGrandMaster} {
			q := r.req(agent+"-"+string(role), agent, role)
			q.Decrees = []sessionzero.DecreeNote{decree}
			q.Quest = &sessionzero.QuestContext{Quest: "login", Step: "implement", Input: "Make the form."}
			text := r.prepare(q).Layer.Text
			if !strings.HasPrefix(text, "The active decrees listed below prevail over everything. After them, these Mestre rules take priority over any other instruction in case of conflict. For everything else, follow the project instructions (`AGENTS.md`).\n") {
				t.Errorf("%s/%s: the layer does not start with the exact rule of priority:\n%s", agent, role, text)
			}
			want := []string{"## Active decrees", "## Mestre rules"}
			if role == sessionzero.RoleCharacter {
				want = append(want, "## Class")
			}
			if agent != presets.AgentClaudeCode { // a folder of skills per character is Claude's
				want = append(want, "## Skills")
			}
			want = append(want, "## Context")
			var got []string
			for _, l := range strings.Split(text, "\n") {
				if strings.HasPrefix(l, "## ") {
					got = append(got, l)
				}
			}
			if !slices.Equal(got, want) {
				t.Errorf("%s/%s: sections = %v; want %v", agent, role, got, want)
			}
			if !strings.Contains(text, "Decree #7, by Lucas (owner)") || !strings.Contains(text, "- Quest: login") {
				t.Errorf("%s/%s: the decree or the quest is not in the layer:\n%s", agent, role, text)
			}
		}
	}
	none := r.prepare(r.req("sem-decretos", presets.AgentCodex, sessionzero.RoleCharacter)).Layer.Text
	if !strings.Contains(none, "## Active decrees\n\nNone.") || !strings.Contains(none, "- Quest: no quest") {
		t.Errorf("a character without decrees or quest:\n%s", none)
	}
}

// CA-SZ-PERS-02
func TestAClassOf7000CharactersLeavesTheTextAndStaysInAFile(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	body := strings.Repeat("Prefer small commits and say why.\n", 7000/34+1)[:7000]
	for _, agent := range threeAgents {
		q := r.req("dev-"+agent, agent, sessionzero.RoleCharacter)
		q.Class = classOf(body)
		plan := r.prepare(q)
		if n := utf8.RuneCountInString(plan.Layer.Text); n >= 8000 {
			t.Errorf("%s: the layer has %d characters; want fewer than 8000", agent, n)
		}
		if !strings.Contains(plan.Layer.Text, ".mestre/agents/dev-"+agent+"/CLASS.md") {
			t.Errorf("%s: the layer does not point to the CLASS.md:\n%s", agent, plan.Layer.Text)
		}
		if got := read(t, filepath.Join(plan.AgentDir, "CLASS.md")); strings.TrimSpace(got) != strings.TrimSpace(body) {
			t.Errorf("%s: the CLASS.md of the character is not the class (%d characters)", agent, len(got))
		}
		if onDisk := read(t, plan.MestreMD); onDisk != plan.Layer.Text {
			t.Errorf("%s: the MESTRE.md on disk is not the layer", agent)
		}
	}
}

func TestTheSkillsLeaveTheTextBeforeTheClassAndTheClassBeforeTheDecrees(t *testing.T) {
	t.Parallel()
	in := sessionzero.LayerInput{
		AgentID: "dev", Name: "dev", Role: sessionzero.RoleCharacter, Provider: providers.Codex, Workspace: "w", Models: sessionzero.BuiltinModels{}.Current(), CLI: true,
		Decrees:   []sessionzero.DecreeNote{{Number: 1, Author: "A", Level: "owner", Text: strings.Repeat("Decree text. ", 30)}},
		ClassBody: strings.Repeat("Class text. ", 250),
		Skills:    []sessionzero.Skill{{Name: "dev-cuidados", Description: "Care.", Body: strings.Repeat("Skill text. ", 60)}},
		Limits:    sessionzero.LayerLimits{Hard: 1 << 20},
	}
	full, err := sessionzero.RenderLayer(in)
	if err != nil || len(full.Files) != 0 {
		t.Fatalf("with no limit: %v, files %v", err, full.Files)
	}
	var stages [][]string
	for hard := utf8.RuneCountInString(full.Text); hard > 0; hard -= 10 {
		in.Limits.Hard = hard
		l, err := sessionzero.RenderLayer(in)
		if err != nil {
			if !errors.Is(err, sessionzero.ErrLayerTooLarge) {
				t.Fatal(err)
			}
			if len(stages) != 4 {
				t.Fatalf("the layer failed with the stages %v; want skills, class and decrees first", stages)
			}
			break
		}
		if n := utf8.RuneCountInString(l.Text); n > hard {
			t.Fatalf("hard %d: the layer has %d characters", hard, n)
		}
		var out []string
		for _, f := range l.Files {
			out = append(out, f.Path)
		}
		if len(stages) == 0 || !slices.Equal(stages[len(stages)-1], out) {
			stages = append(stages, out)
		}
	}
	want := [][]string{{}, {"SKILLS.md"}, {"CLASS.md", "SKILLS.md"}, {"DECREES.md", "CLASS.md", "SKILLS.md"}}
	for i := range stages {
		if stages[i] == nil {
			stages[i] = []string{}
		}
		slices.Sort(stages[i])
		slices.Sort(want[i])
	}
	if len(stages) != 4 || !slices.EqualFunc(stages, want, slices.Equal) {
		t.Errorf("stages = %v; want %v", stages, want)
	}
}

// CA-SZ-PERS-03
func TestTheCodexCommandCarriesTheMestreMDAsATOMLString(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	q := r.req("dev-codex", presets.AgentCodex, sessionzero.RoleCharacter)
	q.Decrees = []sessionzero.DecreeNote{{Number: 3, Author: "Maria \"M\"", Level: "admin", Text: "Use C:\\temp\tand \"quotes\"."}}
	plan := r.prepare(q)
	args := plan.Launch.Args
	var value string
	for i, a := range args {
		if v, ok := strings.CutPrefix(a, "developer_instructions="); ok && i > 0 && args[i-1] == "-c" {
			value = v
		}
	}
	if value == "" {
		t.Fatalf("no -c developer_instructions= in %q", args)
	}
	got, err := readTOMLBasicString(value)
	if err != nil {
		t.Fatalf("the value is not a TOML string: %v", err)
	}
	if got != read(t, plan.MestreMD) {
		t.Fatalf("read back as TOML:\n%q\nwant the MESTRE.md:\n%q", got, read(t, plan.MestreMD))
	}
	if plan.FirstMessage != "" || plan.HookContext != "" {
		t.Errorf("the main channel needs no reserve: first message %q, hook %q", plan.FirstMessage, plan.HookContext)
	}
}

// CA-SZ-PERS-06 and CA-GH-ID-02
func TestTheEnvironmentHasNoSecretAndNoGitAuthor(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	secret := "ghp_" + strings.Repeat("a1B2c3D4e5", 4)
	for _, agent := range threeAgents {
		q := r.req("dev-"+agent, agent, sessionzero.RoleCharacter)
		q.Snapshot.Base.Env = map[string]string{
			"GIT_AUTHOR_NAME": "Someone", "GIT_AUTHOR_EMAIL": "x@example.invalid", "git_committer_name": "Other", "GIT_COMMITTER_DATE": "now",
			"LANG": "pt_BR.UTF-8", "FOO": "bar",
		}
		plan := r.prepare(q)
		env := plan.Launch.Env
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			if strings.HasPrefix(strings.ToUpper(k), "GIT_AUTHOR_") || strings.HasPrefix(strings.ToUpper(k), "GIT_COMMITTER_") {
				t.Errorf("%s: %s is in the environment", agent, kv)
			}
			if r.opts.Masker.Mask(v) != v {
				t.Errorf("%s: %s holds a value the detector of secrets recognizes", agent, k)
			}
		}
		for k, want := range map[string]string{
			"MESTRE_AGENT_ID": "dev-" + agent, "MESTRE_WORKSPACE": "meu-app", "MESTRE_MESA": "3-login", "MESTRE_CLI": r.mestre,
			"MESTRE_ENDPOINT": "unix:///run/mestre/cli.sock", "MESTRE_TMP": plan.Tmp, "TMPDIR": plan.Tmp, "TEMP": plan.Tmp, "TMP": plan.Tmp,
			"GIT_CONFIG_GLOBAL": filepath.Join(r.dir, ".mestre", "agents", "dev-"+agent, "gitconfig"), "LANG": "pt_BR.UTF-8", "FOO": "bar",
		} {
			if got, ok := envOf(env, k); !ok || got != want {
				t.Errorf("%s: %s = %q (set %v); want %q", agent, k, got, ok, want)
			}
		}
		if want := filepath.Join(r.agents, "dev-"+agent, "tmp"); plan.Tmp != want {
			t.Errorf("%s: MESTRE_TMP = %s; want %s", agent, plan.Tmp, want)
		}
		profile := map[string]string{presets.AgentClaudeCode: "CLAUDE_CONFIG_DIR", presets.AgentCodex: "CODEX_HOME", presets.AgentAntigravity: "HOME"}[agent]
		if got, _ := envOf(env, profile); got != plan.Profile.Dir || got == "" {
			t.Errorf("%s: %s = %q; want the profile %q", agent, profile, got, plan.Profile.Dir)
		}
	}

	// A secret in the base is refused at the start, whatever was checked when
	// the base was saved; the message names the variable, not the value.
	q := r.req("com-segredo", presets.AgentCodex, sessionzero.RoleCharacter)
	q.Snapshot.Base.Env = map[string]string{"MY_TOKEN": secret}
	if _, err := r.ch.Prepare(r.ctx, q); err == nil || !strings.Contains(err.Error(), "MY_TOKEN") || strings.Contains(err.Error(), secret) {
		t.Errorf("a base with a secret = %v; want an error that names MY_TOKEN and not its value", err)
	}

	// The API key of the provider is the one secret allowed.
	k := r.req("com-chave", presets.AgentCodex, sessionzero.RoleCharacter)
	k.ProviderKey = &sessionzero.EnvVar{Name: "OPENAI_API_KEY", Value: "sk-" + strings.Repeat("Ab3dE6gH", 6)}
	plan := r.prepare(k)
	if got, _ := envOf(plan.Launch.Env, "OPENAI_API_KEY"); got != k.ProviderKey.Value {
		t.Errorf("OPENAI_API_KEY = %q; want the key", got)
	}
	k.ID, k.Rules = "chave-errada", rulesOf("chave-errada")
	k.ProviderKey.Name = "ANTHROPIC_API_KEY"
	if _, err := r.ch.Prepare(r.ctx, k); err == nil {
		t.Error("the key of another provider was accepted")
	}
}

func TestATerminalGetsOnlyTheWorkspaceTheTableAndTheVariablesOfTheBase(t *testing.T) {
	t.Parallel()
	env := sessionzero.TerminalEnv("meu-app", "3-login", []string{"A=1", "GIT_AUTHOR_NAME=x", "git_committer_email=y"})
	if want := []string{"MESTRE_WORKSPACE=meu-app", "MESTRE_MESA=3-login", "A=1"}; !slices.Equal(env, want) {
		t.Errorf("env = %v; want %v", env, want)
	}
	r := newCharRig(t, nil)
	q := r.req("shell-1", presets.AgentShell, sessionzero.RoleCharacter)
	q.Class, q.Rules, q.Identity = nil, nil, sessionzero.Identity{}
	plan := r.prepare(q)
	if plan.Launch.Path != "bash" || plan.Layer.Text != "" || r.has("shell-1") {
		t.Errorf("a shell got a layer or files: %+v", plan)
	}
	for _, kv := range plan.Launch.Env {
		if k, _, _ := strings.Cut(kv, "="); strings.HasPrefix(k, "MESTRE_") && k != "MESTRE_WORKSPACE" && k != "MESTRE_MESA" && k != "MESTRE_AGENT_ID" {
			t.Errorf("a terminal got %s", kv)
		}
	}
}

// CA-SZ-PERS-09
func TestTheSkillsOfTheLevelAreOnlyInTheLayerOfTheCharacterThatHasThem(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	master := r.prepare(r.req("mestre", presets.AgentCodex, sessionzero.RoleTableMaster))
	dev := r.prepare(r.req("dev", presets.AgentCodex, sessionzero.RoleCharacter))
	entries, err := os.ReadDir(filepath.Join(r.dir, ".agents", "skills"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if !slices.Equal(names, []string{"mestre-comunicacao"}) {
		t.Errorf(".agents/skills/ = %v; want only mestre-comunicacao", names)
	}
	skills := func(p sessionzero.CharacterPlan) string {
		_, after, _ := strings.Cut(p.Layer.Text, "## Skills")
		after, _, _ = strings.Cut(after, "## Context")
		return after
	}
	if s := skills(master); !strings.Contains(s, "### mestre-coordenacao-mesa") || !strings.Contains(s, "### mestre-rituais") {
		t.Errorf("the skills section of the table master:\n%s", s)
	}
	if s := skills(dev); strings.Contains(s, "mestre-coordenacao-mesa") || strings.Contains(s, "mestre-rituais") || !strings.Contains(s, "### dev-cuidados") {
		t.Errorf("the skills section of the Dev:\n%s", s)
	}
}

// Part of CA-CLI-SKL-03 (the fake agents show the skills in the full suite).
func TestClaudeGetsTheSkillsOfItsLevelInAFolderOfItsOwn(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	master := r.prepare(r.req("mestre", presets.AgentClaudeCode, sessionzero.RoleTableMaster))
	dev := r.prepare(r.req("dev", presets.AgentClaudeCode, sessionzero.RoleCharacter))
	for _, s := range []string{"mestre-coordenacao-mesa", "mestre-rituais"} {
		if got := read(t, filepath.Join(master.AgentDir, ".claude", "skills", s, "SKILL.md")); !strings.Contains(got, "name: "+s+"\n") {
			t.Errorf("SKILL.md of %s:\n%s", s, got)
		}
		if r.has("dev", ".claude", "skills", s) {
			t.Errorf("the Dev got %s", s)
		}
	}
	if !r.has("dev", ".claude", "skills", "dev-cuidados", "SKILL.md") || r.has("mestre", ".claude", "skills", "dev-cuidados") {
		t.Error("the skill of the class is not only the Dev's")
	}
	for _, p := range []sessionzero.CharacterPlan{master, dev} {
		if i := slices.Index(p.Launch.Args, "--add-dir"); i < 0 || p.Launch.Args[i+1] != p.AgentDir {
			t.Errorf("%s: --add-dir missing or not the folder of the character: %q", p.ID, p.Launch.Args)
		}
		if i := slices.Index(p.Launch.Args, "--append-system-prompt-file"); i < 0 || p.Launch.Args[i+1] != p.MestreMD {
			t.Errorf("%s: --append-system-prompt-file missing or not the MESTRE.md: %q", p.ID, p.Launch.Args)
		}
		if !slices.Contains(p.Launch.Args, "--settings") || !strings.Contains(read(t, filepath.Join(p.AgentDir, "claude-settings.json")), p.ID) {
			t.Errorf("%s: the native rules were not written: %q", p.ID, p.Launch.Args)
		}
	}
	// A class that loses the skill takes the folder away.
	q := r.req("dev", presets.AgentClaudeCode, sessionzero.RoleCharacter)
	q.Class.Meta.Skills = nil
	r.prepare(q)
	if r.has("dev", ".claude", "skills", "dev-cuidados") {
		t.Error("the skill of the class stayed after the class lost it")
	}
}

// Part of CA-SZ-PERS-10 (the environment and the file; the commit with the
// new author, with the real git, is in character_full_test.go)
func TestTheGitIdentityIsOnlyTheFileOfTheCharacter(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	plan := r.prepare(r.req("dev", presets.AgentClaudeCode, sessionzero.RoleCharacter))
	conf := filepath.Join(r.dir, ".mestre", "agents", "dev", "gitconfig")
	if got, _ := envOf(plan.Launch.Env, "GIT_CONFIG_GLOBAL"); got != conf {
		t.Fatalf("GIT_CONFIG_GLOBAL = %q; want %q", got, conf)
	}
	before := read(t, conf)
	for _, want := range []string{`name = "lucas-mestre-dev[bot]"`, `email = "123+lucas-mestre-dev[bot]@users.noreply.github.com"`, "[include]"} {
		if !strings.Contains(before, want) {
			t.Errorf("gitconfig lacks %s:\n%s", want, before)
		}
	}
	files := func() map[string]string {
		out := map[string]string{}
		_ = filepath.WalkDir(plan.AgentDir, func(p string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				b, _ := os.ReadFile(p)
				out[p] = string(b)
			}
			return nil
		})
		return out
	}
	other := files()
	delete(other, conf)

	changed, err := r.ch.UpdateIdentity(r.ctx, "dev", sessionzero.Identity{Name: "lucas-mestre-qa[bot]", Email: "456+lucas-mestre-qa[bot]@users.noreply.github.com"})
	if err != nil || !changed {
		t.Fatalf("UpdateIdentity = %v, %v", changed, err)
	}
	after := files()
	if !strings.Contains(after[conf], `name = "lucas-mestre-qa[bot]"`) || strings.Contains(after[conf], "dev[bot]") {
		t.Errorf("the gitconfig after the change:\n%s", after[conf])
	}
	delete(after, conf)
	if len(after) != len(other) {
		t.Fatalf("files after = %d, before = %d", len(after), len(other))
	}
	for p, b := range other {
		if after[p] != b {
			t.Errorf("%s changed with the identity", p)
		}
	}
	if again, err := r.ch.UpdateIdentity(r.ctx, "dev", sessionzero.Identity{Name: "lucas-mestre-qa[bot]", Email: "456+lucas-mestre-qa[bot]@users.noreply.github.com"}); err != nil || again {
		t.Errorf("the same identity = %v, %v; want no change", again, err)
	}
	if _, err := r.ch.UpdateIdentity(r.ctx, "dev", sessionzero.Identity{Name: "x\n[core]", Email: "y"}); err == nil {
		t.Error("an identity with a line break was accepted")
	}
	if _, err := r.ch.UpdateIdentity(r.ctx, "ninguem", devIdentity); err == nil {
		t.Error("the identity of a character that is not here was accepted")
	}
}

// CA-SZ-PERS-11
func TestAnEndedAntigravityCharacterLeavesNoFileAndGitNeverSawThem(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	plan := r.prepare(r.req("dev", presets.AgentAntigravity, sessionzero.RoleCharacter))
	agentFile := providers.AntigravityAgentPath(r.dir, "dev")
	if _, err := os.Stat(agentFile); err != nil || !r.has("dev", "MESTRE.md") {
		t.Fatalf("the files of the character are not there: %v", err)
	}
	untracked := func(when string) {
		t.Helper()
		st, err := r.repo.Status(r.ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range st {
			if strings.HasPrefix(e.Path, ".mestre/") || strings.Contains(e.Path, "mestre-dev") {
				t.Errorf("%s: git status lists %s", when, e.Path)
			}
		}
	}
	untracked("after the start")

	// A restart with resume keeps the files.
	if err := r.ch.End(r.ctx, sessionzero.EndRequest{ID: "dev", Dir: r.dir, Provider: providers.Antigravity}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agentFile); err != nil || !r.has("dev", "MESTRE.md") || !r.has("dev", "gitconfig") {
		t.Fatalf("a restart with resume took files away: %v", err)
	}
	if _, ok, _ := r.births.Get(r.ctx, "dev"); !ok {
		t.Error("a restart with resume forgot the birth")
	}

	write(t, filepath.Join(plan.AgentDir, "x", "from-the-agent.txt"), "x")
	if err := r.ch.End(r.ctx, sessionzero.EndRequest{ID: "dev", Dir: r.dir, Provider: providers.Antigravity, Final: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(agentFile); !os.IsNotExist(err) {
		t.Errorf("the agent file is still there: %v", err)
	}
	if _, err := os.Stat(plan.AgentDir); !os.IsNotExist(err) {
		t.Errorf(".mestre/agents/dev is still there: %v", err)
	}
	untracked("after the end")
	if _, ok, _ := r.births.Get(r.ctx, "dev"); ok {
		t.Error("the birth of a character that ended for good was kept")
	}
	if err := r.ch.End(r.ctx, sessionzero.EndRequest{ID: "dev", Dir: r.dir, Provider: providers.Antigravity, Final: true}); err != nil {
		t.Errorf("ending twice = %v", err)
	}
}

// CA-SZ-PERS-12
func TestMestreTmpIsDeletedForGoodAndKeptOnAResume(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	plan := r.prepare(r.req("dev", presets.AgentCodex, sessionzero.RoleCharacter))
	write(t, filepath.Join(plan.Tmp, "pacote", "CLASS.md"), "a class")
	write(t, filepath.Join(plan.Tmp, "estado.json"), "{}")

	if err := r.ch.End(r.ctx, sessionzero.EndRequest{ID: "dev", Dir: r.dir, Provider: providers.Codex}); err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(plan.Tmp, "pacote", "CLASS.md")) != "a class" || read(t, filepath.Join(plan.Tmp, "estado.json")) != "{}" {
		t.Fatal("a restart with resume changed MESTRE_TMP")
	}
	// Preparing it again for the resume keeps MESTRE_TMP too.
	r.prepare(r.req("dev", presets.AgentCodex, sessionzero.RoleCharacter))
	if read(t, filepath.Join(plan.Tmp, "estado.json")) != "{}" {
		t.Fatal("preparing a character again changed MESTRE_TMP")
	}

	if err := r.ch.End(r.ctx, sessionzero.EndRequest{ID: "dev", Dir: r.dir, Provider: providers.Codex, Final: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.Tmp); !os.IsNotExist(err) {
		t.Errorf("MESTRE_TMP is still there: %v", err)
	}
}

// CA-SZ-PERS-13
func TestTheRulesTellToTreatQueuedAsPending(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	for _, role := range []sessionzero.Role{sessionzero.RoleCharacter, sessionzero.RoleTableMaster, sessionzero.RoleGrandMaster} {
		for _, agent := range threeAgents {
			rules := r.prepare(r.req(string(role)+"-"+agent, agent, role)).Layer.Section(sessionzero.SectionRules)
			for _, want := range []string{"`queued: true`", "`PENDING:`", "pending", "inbox"} {
				if !strings.Contains(rules, want) {
					t.Errorf("%s/%s: the rules lack %q:\n%s", role, agent, want, rules)
				}
			}
		}
	}
	// A sheet that does not get the Mestre's skills keeps the rest of the
	// layer and loses the part about the CLI.
	q := r.req("sem-cli", presets.AgentCodex, sessionzero.RoleCharacter)
	no := false
	q.Snapshot.Sheet.MestreSkills = &no
	plan := r.prepare(q)
	if strings.Contains(plan.Layer.Text, "mestre done") || strings.Contains(plan.Layer.Text, "## Skills") || !strings.Contains(plan.Layer.Text, "Commits are local only") {
		t.Errorf("a sheet without the Mestre skills:\n%s", plan.Layer.Text)
	}
}

// CA-GM-03
func TestTheInstructionsOfTheUsersAddToTheRulesOfTheGrandMasterAndNeverToACommonCharacter(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	gm := r.req("gm", presets.AgentClaudeCode, sessionzero.RoleGrandMaster)
	gm.Instructions = "Prefira Codex para revisão."
	rules := r.prepare(gm).Layer.Section(sessionzero.SectionRules)
	i := strings.Index(rules, "### Instruções dos usuários para este workspace")
	if i < 0 || !strings.Contains(rules[i:], "Prefira Codex para revisão.") || !strings.Contains(rules[:i], "Grand Master") {
		t.Errorf("the rules of the Grand Master:\n%s", rules)
	}
	// The rules of the system stay complete, before the text of the users.
	for _, rule := range (sessionzero.BuiltinModels{}).Current().Rules[sessionzero.RoleGrandMaster] {
		if !strings.Contains(rules[:i], "- "+rule.Text) {
			t.Errorf("the rule %.40q... is not complete before the instructions of the users", rule.Text)
		}
	}
	tm := r.req("tm", presets.AgentClaudeCode, sessionzero.RoleTableMaster)
	tm.Instructions = "Sempre abra o PR em rascunho."
	if rules := r.prepare(tm).Layer.Section(sessionzero.SectionRules); !strings.Contains(rules, "### Instruções dos usuários para os Mestres de mesa") {
		t.Errorf("the rules of the table master:\n%s", rules)
	}
	dev := r.req("dev", presets.AgentClaudeCode, sessionzero.RoleCharacter)
	dev.Instructions = "Isto não é para o Dev."
	if text := r.prepare(dev).Layer.Text; strings.Contains(text, "Isto não é para o Dev.") {
		t.Errorf("the instructions of the users reached a common character:\n%s", text)
	}
}

func TestAnAccountProfileThatTheMachineLacksStopsTheCreationWithTheReason(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	r.accounts.err = accounts.ErrNoProfile
	_, err := r.ch.Prepare(r.ctx, r.req("dev", presets.AgentCodex, sessionzero.RoleCharacter))
	if !errors.Is(err, accounts.ErrNoProfile) {
		t.Fatalf("Prepare = %v; want ErrNoProfile", err)
	}
	if r.has("dev") {
		t.Error("files were made for a character that could not be created")
	}
	if _, ok, _ := r.births.Get(r.ctx, "dev"); ok {
		t.Error("a character that could not be created was born")
	}
}

func TestACharacterNeedsItsRulesItsIdentityAndAValidRequest(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	for name, tweak := range map[string]func(*sessionzero.CharacterRequest){
		"no rules":       func(q *sessionzero.CharacterRequest) { q.Rules = nil },
		"no identity":    func(q *sessionzero.CharacterRequest) { q.Identity = sessionzero.Identity{} },
		"bad id":         func(q *sessionzero.CharacterRequest) { q.ID = "../x" },
		"bad level":      func(q *sessionzero.CharacterRequest) { q.Role = "boss" },
		"relative dir":   func(q *sessionzero.CharacterRequest) { q.Dir = "wt" },
		"gm with class":  func(q *sessionzero.CharacterRequest) { q.Role = sessionzero.RoleGrandMaster },
		"unknown agent":  func(q *sessionzero.CharacterRequest) { q.Snapshot.Sheet.Agent = "gemini" },
		"base off linux": func(q *sessionzero.CharacterRequest) { q.Snapshot.Base.Platforms = nil },
	} {
		q := r.req("dev", presets.AgentCodex, sessionzero.RoleCharacter)
		tweak(&q)
		if _, err := r.ch.Prepare(r.ctx, q); err == nil {
			t.Errorf("%s: Prepare accepted it", name)
		}
	}
	if _, ok, _ := r.births.Get(r.ctx, "dev"); ok || r.has("dev") {
		t.Error("a request that failed left a character behind")
	}
}

func TestPreparingAgainWritesNothingAndAClassChangeWritesOnlyWhatChanged(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	for _, agent := range threeAgents {
		q := r.req("dev-"+agent, agent, sessionzero.RoleCharacter)
		first := r.prepare(q)
		if len(first.Written) == 0 {
			t.Fatalf("%s: nothing written the first time", agent)
		}
		if again := r.prepare(q); len(again.Written) != 0 || len(again.Removed) != 0 {
			t.Errorf("%s: the second Prepare wrote %v and removed %v", agent, again.Written, again.Removed)
		}
		q.Class = classOf("# Dev\n\nA new class text.\n")
		changed := r.prepare(q)
		if !slices.Contains(changed.Written, ".mestre/agents/dev-"+agent+"/MESTRE.md") || slices.Contains(changed.Written, ".mestre/agents/dev-"+agent+"/gitconfig") {
			t.Errorf("%s: after a class change wrote %v", agent, changed.Written)
		}
	}
}

func TestACharacterBornBeforeKeepsItsSheetAndItsBase(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	q := r.req("dev", presets.AgentCodex, sessionzero.RoleCharacter)
	q.Snapshot.Base.Args = []string{"--model", "gpt-old"}
	r.prepare(q)
	q.Snapshot.Base.Args = []string{"--model", "gpt-new"} // the base was edited after
	q.Snapshot.Sheet.Account = "outro"
	plan := r.prepare(q)
	if !slices.Contains(plan.Launch.Args, "gpt-old") || slices.Contains(plan.Launch.Args, "gpt-new") {
		t.Errorf("the command of a living character follows the edit of the base: %q", plan.Launch.Args)
	}
	if plan.Profile.Name != "codex-padrao" {
		t.Errorf("the account profile follows the edit of the sheet: %q", plan.Profile.Name)
	}
	b, _, _ := r.births.Get(r.ctx, "dev")
	if b.Snapshot.Base.Args[1] != "gpt-old" || b.Provider != providers.Codex {
		t.Errorf("birth = %+v", b)
	}
}

func TestTheReserveChannelsOfTheLayer(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	r.channels[providers.Codex] = providers.LayerFallbacks{Channel: providers.LayerHook}
	r.channels[providers.Antigravity] = providers.LayerFallbacks{Channel: providers.LayerFirstMessage}
	r.channels[providers.ClaudeCode] = providers.LayerFallbacks{Channel: providers.LayerFirstMessage}

	cx := r.prepare(r.req("cx", presets.AgentCodex, sessionzero.RoleCharacter))
	if strings.Contains(strings.Join(cx.Launch.Args, " "), "developer_instructions") || cx.HookContext != cx.Layer.Text || cx.FirstMessage != "" {
		t.Errorf("Codex with the hook channel: args %q, hook %q, first message %q", cx.Launch.Args, cx.HookContext, cx.FirstMessage)
	}
	ag := r.prepare(r.req("ag", presets.AgentAntigravity, sessionzero.RoleCharacter))
	if ag.FirstMessage != ag.Layer.Text || strings.Contains(read(t, providers.AntigravityAgentPath(r.dir, "ag")), "Write the code and the tests") {
		t.Errorf("Antigravity with the first message: first message %q", ag.FirstMessage)
	}
	cl := r.prepare(r.req("cl", presets.AgentClaudeCode, sessionzero.RoleCharacter))
	if cl.FirstMessage != cl.Layer.Text || !slices.Contains(cl.Launch.Args, "--append-system-prompt-file") {
		t.Errorf("Claude Code with the first message: %q, args %q", cl.FirstMessage, cl.Launch.Args)
	}
	delete(r.channels, providers.Codex)
	if cx2 := r.prepare(r.req("cx2", presets.AgentCodex, sessionzero.RoleCharacter)); cx2.HookContext != "" || cx2.FirstMessage != "" {
		t.Errorf("the main channel has a reserve: %+v", cx2)
	}

	// The hook channel gives the layer once, at the start of the session.
	ctxs := r.ch.Contexts()
	start := providers.Event{Event: providers.EventSessionStart, AgentID: "cx"}
	if got, err := ctxs.Context(r.ctx, start); err != nil || got != cx.Layer.Text {
		t.Errorf("session start = %q, %v; want the layer", got, err)
	}
	if got, _ := ctxs.Context(r.ctx, start); got != "" {
		t.Errorf("the layer came twice: %q", got)
	}
}

// The change of what goes in the layer, in the session zero (CA-SZ-PERS-05 is
// proved end to end, with a decree made by the flow of the people, in internal/app).
func TestAChangeOfTheDecreesRewritesTheFileAndTheNextTurnGetsOnlyThatSection(t *testing.T) {
	t.Parallel()
	for _, agent := range threeAgents {
		r := newCharRig(t, nil)
		q := r.req("dev", agent, sessionzero.RoleCharacter)
		plan := r.prepare(q)
		ctxs := r.ch.Contexts()
		turn := providers.Event{Event: providers.EventTurnStart, AgentID: "dev"}
		if got, _ := ctxs.Context(r.ctx, turn); got != "" {
			t.Fatalf("%s: an update with nothing changed: %q", agent, got)
		}

		until := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
		d := sessionzero.Dynamic{Decrees: []sessionzero.DecreeNote{{Number: 9, Author: "Lucas", Level: "owner", Text: "Do not touch src/legacy.", Until: &until}}}
		rep, err := r.ch.Refresh(r.ctx, "dev", d)
		if err != nil || !slices.Equal(rep.Changed, []string{sessionzero.SectionDecrees}) {
			t.Fatalf("%s: Refresh = %+v, %v; want only the decrees changed", agent, rep, err)
		}
		if onDisk := read(t, plan.MestreMD); !strings.Contains(onDisk, "Decree #9, by Lucas (owner), valid until 2026-10-20T12:00:00Z: Do not touch src/legacy.") {
			t.Errorf("%s: the MESTRE.md on disk lacks the decree:\n%s", agent, onDisk)
		}
		// Never in the middle of a turn: the start of a session gives nothing.
		if got, _ := ctxs.Context(r.ctx, providers.Event{Event: providers.EventPreTool, AgentID: "dev"}); got != "" {
			t.Errorf("%s: an update in the middle of a turn: %q", agent, got)
		}
		got, err := ctxs.Context(r.ctx, turn)
		if err != nil || !strings.Contains(got, "Update of the Mestre layer") || !strings.Contains(got, "Decree #9") || strings.Contains(got, "## Context") || strings.Contains(got, "## Mestre rules") {
			t.Errorf("%s: the update of the turn = %q, %v", agent, got, err)
		}
		if again, _ := ctxs.Context(r.ctx, turn); again != "" {
			t.Errorf("%s: the update came twice: %q", agent, again)
		}
		if rep, err := r.ch.Refresh(r.ctx, "dev", d); err != nil || len(rep.Changed) != 0 || len(rep.Written) != 0 {
			t.Errorf("%s: the same Refresh = %+v, %v", agent, rep, err)
		}
		// Without the hook of the start of the turn the update goes in the next message.
		if _, err := r.ch.Refresh(r.ctx, "dev", sessionzero.Dynamic{}); err != nil {
			t.Fatal(err)
		}
		if up := r.ch.TakeUpdate("dev"); !strings.Contains(up, "None.") || r.ch.TakeUpdate("dev") != "" {
			t.Errorf("%s: TakeUpdate = %q", agent, up)
		}
	}
}

func TestTheBirthOfACharacterIsKeptInTheMachineDatabase(t *testing.T) {
	t.Parallel()
	db := newMachineDB(t)
	b := sessionzero.BirthsDB{DB: db, Clock: fakeClock()}
	snap := snapshot(presets.AgentCodex)
	if _, ok, err := b.Get(t.Context(), "dev"); ok || err != nil {
		t.Fatalf("Get of nobody = %v, %v", ok, err)
	}
	if err := b.Put(t.Context(), sessionzero.Birth{AgentID: "dev", Provider: providers.Codex, Version: "2", Snapshot: snap}); err != nil {
		t.Fatal(err)
	}
	if err := b.Put(t.Context(), sessionzero.Birth{AgentID: "dev", Provider: providers.Codex, Version: "3", Snapshot: snap}); !errors.Is(err, sessionzero.ErrBorn) {
		t.Fatalf("a second birth = %v; want ErrBorn", err)
	}
	got, ok, err := b.Get(t.Context(), "dev")
	if err != nil || !ok || got.Version != "2" || got.Snapshot.Sheet.Name != snap.Sheet.Name || got.Snapshot.Base.Command != "codex" || got.Snapshot.Base.Ready.Mode != presets.ReadyHook {
		t.Fatalf("Get = %+v, %v, %v", got, ok, err)
	}
	if err := b.SetVersion(t.Context(), "dev", "3"); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := b.Get(t.Context(), "dev"); got.Version != "3" {
		t.Errorf("version = %s after SetVersion", got.Version)
	}
	if err := b.Delete(t.Context(), "dev"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := b.Get(t.Context(), "dev"); ok {
		t.Error("the birth is still there")
	}
}

// CA-CLI-SKL-01
func TestEveryGeneratedSkillHasOnlyNameAndDescriptionAndTheMarker(t *testing.T) {
	t.Parallel()
	set := sessionzero.BuiltinModels{}.Current()
	shared, _ := sessionzero.GenerateSkills(cli.Default())
	skills := []sessionzero.Skill{shared}
	for _, s := range set.Skills {
		skills = append(skills, s)
	}
	if len(skills) != 4 {
		t.Fatalf("%d skills; want the four models", len(skills))
	}
	for _, s := range skills {
		text := string(sessionzero.SkillFile(s, set.Version))
		lines := strings.Split(text, "\n")
		if lines[0] != "---" || !strings.HasPrefix(lines[1], "name: mestre-") || lines[2] != "description: >-" {
			t.Errorf("%s: frontmatter:\n%s", s.Name, text)
			continue
		}
		end := slices.Index(lines[1:], "---") + 1
		for _, l := range lines[1:end] {
			if k, _, ok := strings.Cut(l, ":"); ok && !strings.HasPrefix(l, " ") && k != "name" && k != "description" {
				t.Errorf("%s: field %q in the frontmatter", s.Name, k)
			}
		}
		desc := strings.TrimSpace(strings.Join(lines[3:end], " "))
		if desc == "" || utf8.RuneCountInString(desc) > 500 {
			t.Errorf("%s: description of %d characters", s.Name, utf8.RuneCountInString(desc))
		}
		if want := "<!-- generated by Mestre, template v" + set.Version + "; do not edit -->"; lines[end+1] != want {
			t.Errorf("%s: the line after the frontmatter is %q; want %q", s.Name, lines[end+1], want)
		}
		if n := len(text); n > 6*1024 {
			t.Errorf("%s: %d bytes", s.Name, n)
		}
	}
	for name := range set.Skills {
		if !strings.HasPrefix(name, "mestre-") {
			t.Errorf("skill %q lacks the prefix", name)
		}
	}
}

func TestTheModelsOfAnOlderVersionStayUntilTheCharacterIsRegenerated(t *testing.T) {
	t.Parallel()
	models := &modelsFake{cur: "2", sets: map[string]sessionzero.ModelSet{"2": versionOf("2"), "3": versionOf("3")}}
	r := newCharRig(t, func(o *sessionzero.CharacterOptions) { o.Models = models })
	old := r.req("antigo", presets.AgentCodex, sessionzero.RoleCharacter)
	if p := r.prepare(old); p.Version != "2" || !strings.Contains(p.Layer.Text, "models v2") {
		t.Fatalf("born in 2: %+v", p.Version)
	}
	models.use("3")
	if p := r.prepare(r.req("novo", presets.AgentCodex, sessionzero.RoleCharacter)); p.Version != "3" || !strings.Contains(p.Layer.Text, "models v3") {
		t.Errorf("a new character born in %s", p.Version)
	}
	again := r.prepare(old) // a resume restart
	if again.Version != "2" || strings.Contains(again.Layer.Text, "models v3") || again.Upgraded {
		t.Errorf("the old character moved to %s without a regeneration", again.Version)
	}
	if b, _, _ := r.births.Get(r.ctx, "antigo"); b.Version != "2" {
		t.Errorf("birth version = %s", b.Version)
	}
	old.Regenerate = true
	reg := r.prepare(old)
	if reg.Version != "3" || !strings.Contains(read(t, reg.MestreMD), "models v3") {
		t.Errorf("regenerated: %s", reg.Version)
	}
	if b, _, _ := r.births.Get(r.ctx, "antigo"); b.Version != "3" {
		t.Errorf("birth version after the regeneration = %s", b.Version)
	}

	// The Citadel no longer has the models of the version it was born with.
	delete(models.sets, "2")
	r.births.m["velho"] = sessionzero.Birth{AgentID: "velho", Provider: providers.Codex, Version: "2", Snapshot: snapshot(presets.AgentCodex)}
	if p := r.prepare(r.req("velho", presets.AgentCodex, sessionzero.RoleCharacter)); p.Version != "3" || !p.Upgraded {
		t.Errorf("without the old models: version %s, upgraded %v", p.Version, p.Upgraded)
	}
}
