//go:build full

package sessionzero_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

// recordHooks answers every hook of the fake agents neutrally and keeps what
// the agents sent, by event.
type recordHooks struct {
	mu    sync.Mutex
	calls []fakeagent.HookCall
}

func (h *recordHooks) RunHook(_ context.Context, c fakeagent.HookCall) (fakeagent.HookResult, error) {
	h.mu.Lock()
	h.calls = append(h.calls, c)
	h.mu.Unlock()
	out := "{}"
	if c.Event == "PreToolUse" {
		out = `{"decision":"allow"}`
	}
	return fakeagent.HookResult{Stdout: []byte(out)}, nil
}

// login puts a fake credential in each account profile.
func (r *charRig) login() {
	r.t.Helper()
	write(r.t, filepath.Join(r.profiles[providers.ClaudeCode], ".credentials.json"), "{}")
	write(r.t, filepath.Join(r.profiles[providers.Codex], "auth.json"), "{}")
	write(r.t, filepath.Join(r.profiles[providers.Antigravity], ".gemini", "antigravity-cli", "antigravity-oauth-token"), "{}")
}

var fakeProgram = map[providers.ID]string{providers.ClaudeCode: "claude", providers.Codex: "codex", providers.Antigravity: "agy"}

// fakeChar is a fake agent started from the plan of a character.
type fakeChar struct {
	mp    *fakeagent.MemProcess
	hooks *recordHooks
	ad    providers.Adapter
}

// start runs the command of the plan on the fake agent, the way the engine
// will run it on a terminal (Launch.Verify first), with the home of the user
// the engine does not touch.
func (r *charRig) start(t *testing.T, plan sessionzero.CharacterPlan, extraEnv ...string) *fakeChar {
	t.Helper()
	if plan.Launch.Verify != nil {
		if err := plan.Launch.Verify(); err != nil {
			t.Fatalf("%s: the check before the start failed: %v", plan.ID, err)
		}
	}
	h := &recordHooks{}
	launcher := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
	env := append(slices.Clone(plan.Launch.Env), extraEnv...)
	proc, err := launcher.Start(t.Context(), terminal.Spec{Path: fakeProgram[plan.Provider], Args: plan.Launch.Args, Env: env, Dir: plan.Launch.Dir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proc.Kill() })
	ad, _ := r.opts.Providers.For(plan.Provider)
	return &fakeChar{mp: proc.(*fakeagent.MemProcess), hooks: h, ad: ad}
}

var typedRe = regexp.MustCompile(`(?s)\r\n> (.*?)\r\n\* Working…`)

// typed are the messages the agent started a turn with, in order, as the
// terminal shows them (not every provider tells its prompt to the hooks).
func (c *fakeChar) typed() []string {
	var out []string
	for _, m := range typedRe.FindAllStringSubmatch(c.mp.Output(), -1) {
		out = append(out, m[1])
	}
	return out
}

// type delivers a message the way the engine does, through the adapter.
func (c *fakeChar) type_(t *testing.T, text string) {
	t.Helper()
	b, err := c.ad.TypeMessage(text)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.mp.PTY().Write(b); err != nil {
		t.Fatal(err)
	}
	synctest.Wait()
}

// dump is the answer of /fake-dump.
type dump struct {
	Instructions string   `json:"instructions"`
	Skills       []string `json:"skills"`
	AddDirs      []string `json:"add_dirs"`
	Env          map[string]string
}

func (c *fakeChar) dump(t *testing.T) dump {
	t.Helper()
	before := len(c.mp.Output())
	c.type_(t, "/fake-dump")
	_, out, _ := strings.Cut(c.mp.Output()[before:], "{")
	line, _, _ := strings.Cut("{"+out, "\r\n")
	var d dump
	if err := json.Unmarshal([]byte(line), &d); err != nil {
		t.Fatalf("/fake-dump: %v\n%s", err, c.mp.Output()[before:])
	}
	return d
}

// CA-SZ-PERS-04
func TestWithTheMainChannelOfAntigravityFailedTheFirstMessageIsTheMestreMD(t *testing.T) { //nolint:paralleltest // the fake agent runs in a synctest bubble
	r := newCharRig(t, nil)
	r.login()
	r.channels[providers.Antigravity] = providers.LayerFallbacks{Channel: providers.LayerFirstMessage}
	plan := r.prepare(r.req("dev", presets.AgentAntigravity, sessionzero.RoleCharacter))
	if plan.FirstMessage == "" || plan.FirstMessage != read(t, plan.MestreMD) {
		t.Fatalf("the first message of the plan is not the MESTRE.md: %q", plan.FirstMessage)
	}
	synctest.Test(t, func(t *testing.T) {
		c := r.start(t, plan)
		synctest.Wait()
		c.type_(t, plan.FirstMessage)
		c.type_(t, "implement the form")
		got := c.typed()
		if len(got) != 2 || got[0] != plan.FirstMessage || got[1] != "implement the form" {
			t.Fatalf("the messages the agent received, in order: %q; want the MESTRE.md first and then the task", got)
		}
		// The agent file is the stub: the layer reached the agent by the message alone.
		if strings.Contains(read(t, providers.AntigravityAgentPath(r.dir, "dev")), "Write the code and the tests") {
			t.Error("the layer is also in the agent file")
		}
	})
}

// CA-SZ-PERS-07
func TestTheAgentsMDAndTheFilesOfTheUserAreNeverTouched(t *testing.T) { //nolint:paralleltest // the fake agents run in a synctest bubble
	r := newCharRig(t, nil)
	r.login()
	const agentsMD = "# Project\n\nRun `go test ./...`.\n"
	write(t, filepath.Join(r.dir, "AGENTS.md"), agentsMD)
	home := t.TempDir()
	for _, f := range []string{".claude/CLAUDE.md", ".codex/AGENTS.md", ".gemini/AGENTS.md", ".gitconfig"} {
		write(t, filepath.Join(home, filepath.FromSlash(f)), "mine\n")
	}
	realHome, _ := os.UserHomeDir()
	globals := []string{".claude/CLAUDE.md", ".codex/AGENTS.md", ".gemini/AGENTS.md"}
	existed := map[string]bool{}
	for _, f := range globals {
		_, err := os.Lstat(filepath.Join(realHome, filepath.FromSlash(f)))
		existed[f] = err == nil
	}
	before := tree(t, home)
	profilesBefore := map[providers.ID]map[string]string{}
	for p, dir := range r.profiles {
		profilesBefore[p] = tree(t, dir)
	}

	plans := map[string]sessionzero.CharacterPlan{}
	for _, agent := range threeAgents {
		plans[agent] = r.prepare(r.req("dev-"+agent, agent, sessionzero.RoleCharacter))
	}
	synctest.Test(t, func(t *testing.T) {
		for _, agent := range threeAgents {
			var extra []string
			if agent != presets.AgentAntigravity { // its HOME is the account profile
				extra = []string{"HOME=" + home, "USERPROFILE=" + home}
			}
			c := r.start(t, plans[agent], extra...)
			synctest.Wait()
			c.type_(t, "hello")
			if len(c.typed()) == 0 {
				t.Errorf("%s: the fake agent did not start a turn:\n%s", agent, c.mp.Output())
			}
		}
	})
	if got := read(t, filepath.Join(r.dir, "AGENTS.md")); got != agentsMD {
		t.Errorf("AGENTS.md changed:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(r.dir, "CLAUDE.md")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a CLAUDE.md was created: %v", err)
	}
	after := tree(t, home)
	for f, b := range before {
		if after[f] != b {
			t.Errorf("%s of the user changed", f)
		}
	}
	for f := range after {
		if _, ok := before[f]; !ok {
			t.Errorf("%s was created in the home of the user", f)
		}
	}
	for _, f := range globals {
		if _, err := os.Lstat(filepath.Join(realHome, filepath.FromSlash(f))); (err == nil) != existed[f] {
			t.Errorf("~/%s was created or removed", f)
		}
	}
	for p, was := range profilesBefore {
		for f := range tree(t, r.profiles[p]) {
			if _, ok := was[f]; !ok && (strings.HasSuffix(f, "AGENTS.md") || strings.HasSuffix(f, "CLAUDE.md")) {
				t.Errorf("%s: %s was created in the account profile", p, f)
			}
		}
	}
}

// tree is every regular file under dir with its content, by relative path.
func tree(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		rel, _ := filepath.Rel(dir, p)
		out[filepath.ToSlash(rel)] = string(b)
		return rerr
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// CA-SZ-PERS-08
func TestACharacterBornInVersion2StaysAfterTheModelsGoTo3(t *testing.T) {
	t.Parallel()
	models := &modelsFake{cur: "2", sets: map[string]sessionzero.ModelSet{"2": versionOf("2"), "3": versionOf("3")}}
	births := sessionzero.BirthsDB{DB: newMachineDB(t), Clock: fakeClock()}
	r := newCharRig(t, func(o *sessionzero.CharacterOptions) { o.Models, o.Births = models, births })
	old := r.req("antigo", presets.AgentCodex, sessionzero.RoleCharacter)
	if p := r.prepare(old); p.Version != "2" {
		t.Fatalf("born in %s; want 2", p.Version)
	}
	models.use("3")

	novo := r.prepare(r.req("novo", presets.AgentCodex, sessionzero.RoleCharacter))
	if novo.Version != "3" || !strings.Contains(read(t, novo.MestreMD), "models v3") {
		t.Errorf("the new character was born in %s", novo.Version)
	}
	for _, id := range []string{"antigo", "novo"} {
		want := map[string]string{"antigo": "2", "novo": "3"}[id]
		if b, ok, err := births.Get(t.Context(), id); err != nil || !ok || b.Version != want {
			t.Errorf("%s: birth = %+v, %v, %v; want version %s", id, b, ok, err, want)
		}
	}
	// The old one, restarted, is still in 2: its files are not regenerated.
	if p := r.prepare(old); p.Version != "2" || strings.Contains(read(t, p.MestreMD), "models v3") {
		t.Errorf("the old character moved to %s on a restart", p.Version)
	}
	if b, _, _ := births.Get(t.Context(), "antigo"); b.Version != "2" {
		t.Errorf("the old character is in %s in the database", b.Version)
	}
	// Until somebody regenerates.
	old.Regenerate = true
	if p := r.prepare(old); p.Version != "3" || !strings.Contains(read(t, p.MestreMD), "models v3") {
		t.Errorf("regenerated to %s", p.Version)
	}
}

// CA-CLI-SKL-03: the Mestre of the table and a Dev, in the same provider.
func TestTheSkillsOfTheTableMasterAndOfTheDevReachOnlyEachOfThem(t *testing.T) { //nolint:paralleltest // the fake agents run in a synctest bubble
	for _, agent := range threeAgents {
		t.Run(agent, func(t *testing.T) {
			t.Parallel()
			r := newCharRig(t, nil)
			r.login()
			master := r.prepare(r.req("mestre", agent, sessionzero.RoleTableMaster))
			devReq := r.req("dev", agent, sessionzero.RoleCharacter)
			devReq.Class.Meta.Skills = nil
			dev := r.prepare(devReq)
			synctest.Test(t, func(t *testing.T) {
				dm := r.start(t, master).dump(t)
				dd := r.start(t, dev).dump(t)
				if agent == presets.AgentClaudeCode {
					// A folder per character, loaded with --add-dir.
					if want := []string{"mestre-comunicacao", "mestre-coordenacao-mesa", "mestre-rituais"}; !slices.Equal(dm.Skills, want) {
						t.Errorf("skills of the table master = %v; want %v", dm.Skills, want)
					}
					if want := []string{"mestre-comunicacao"}; !slices.Equal(dd.Skills, want) {
						t.Errorf("skills of the Dev = %v; want %v", dd.Skills, want)
					}
					return
				}
				// A section of the layer, and only the shared skill in the folder.
				for _, s := range []string{"### mestre-coordenacao-mesa", "### mestre-rituais"} {
					if !strings.Contains(dm.Instructions, s) || strings.Contains(dd.Instructions, s) {
						t.Errorf("the section %q: table master %v, Dev %v", s, strings.Contains(dm.Instructions, s), strings.Contains(dd.Instructions, s))
					}
				}
				for who, d := range map[string]dump{"table master": dm, "Dev": dd} {
					if want := []string{"mestre-comunicacao"}; !slices.Equal(d.Skills, want) {
						t.Errorf("skills of the %s = %v; want only %v", who, d.Skills, want)
					}
				}
			})
			entries, err := os.ReadDir(filepath.Join(r.dir, ".agents", "skills"))
			if err != nil || len(entries) != 1 || entries[0].Name() != "mestre-comunicacao" {
				t.Errorf(".agents/skills/ = %v, %v; want only mestre-comunicacao", entries, err)
			}
		})
	}
}

// CA-CLI-SKL-04
func TestTheGrandMasterGetsItsOwnSkillsAndNoOtherOfTheMestre(t *testing.T) { //nolint:paralleltest // the fake agents run in a synctest bubble
	for _, agent := range threeAgents {
		t.Run(agent, func(t *testing.T) {
			t.Parallel()
			r := newCharRig(t, nil)
			r.login()
			plan := r.prepare(r.req("gm", agent, sessionzero.RoleGrandMaster))
			synctest.Test(t, func(t *testing.T) {
				d := r.start(t, plan).dump(t)
				if agent == presets.AgentClaudeCode {
					if want := []string{"mestre-comunicacao", "mestre-grao-mestre", "mestre-rituais"}; !slices.Equal(d.Skills, want) {
						t.Errorf("skills of the Grand Master = %v; want %v", d.Skills, want)
					}
					return
				}
				for _, s := range []string{"### mestre-grao-mestre", "### mestre-rituais"} {
					if !strings.Contains(d.Instructions, s) {
						t.Errorf("the layer lacks %q", s)
					}
				}
				if strings.Contains(d.Instructions, "mestre-coordenacao-mesa") {
					t.Error("the Grand Master got the skill of the table master")
				}
			})
		})
	}
}

// CA-SZ-PERS-10 (with the environment and the file in character_test.go): the next commit of the living character has the
// author of the new App, with the real git.
func TestTheNextCommitHasTheAuthorOfTheNewApp(t *testing.T) {
	t.Parallel()
	r := newCharRig(t, nil)
	plan := r.prepare(r.req("dev", presets.AgentClaudeCode, sessionzero.RoleCharacter))
	conf, _ := envOf(plan.Launch.Env, "GIT_CONFIG_GLOBAL")
	repo := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = repo
		for _, kv := range os.Environ() {
			if !strings.HasPrefix(kv, "GIT_") {
				cmd.Env = append(cmd.Env, kv)
			}
		}
		cmd.Env = append(cmd.Env, "GIT_CONFIG_GLOBAL="+conf, "GIT_CONFIG_NOSYSTEM=1")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	run("commit", "-q", "--allow-empty", "-m", "first", "--no-gpg-sign")
	if got, want := run("log", "-1", "--format=%an <%ae>"), devIdentity.Name+" <"+devIdentity.Email+">"; got != want {
		t.Fatalf("author = %q; want %q", got, want)
	}
	next := sessionzero.Identity{Name: "lucas-mestre-qa[bot]", Email: "456+lucas-mestre-qa[bot]@users.noreply.github.com"}
	if _, err := r.ch.UpdateIdentity(t.Context(), "dev", next); err != nil {
		t.Fatal(err)
	}
	run("commit", "-q", "--allow-empty", "-m", "second", "--no-gpg-sign")
	if got, want := run("log", "-1", "--format=%an <%ae>|%cn"), next.Name+" <"+next.Email+">|"+next.Name; got != want {
		t.Errorf("author and committer of the next commit = %q; want %q", got, want)
	}
}
