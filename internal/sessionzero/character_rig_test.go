package sessionzero_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/accounts"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	machinedb "github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/pkg/clock"
)

func fakeClock() clock.Clock { return clock.NewFake(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)) }

func newMachineDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := machinedb.Open(t.Context(), filepath.Join(t.TempDir(), "maquina.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// --- fakes of the session zero of a character ---

// memBirths keeps the births in memory (BirthsDB is tested on its own).
type memBirths struct {
	mu sync.Mutex
	m  map[string]sessionzero.Birth
}

func (b *memBirths) Get(_ context.Context, id string) (sessionzero.Birth, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.m[id]
	return v, ok, nil
}

func (b *memBirths) Put(_ context.Context, v sessionzero.Birth) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.m[v.AgentID]; ok {
		return sessionzero.ErrBorn
	}
	b.m[v.AgentID] = v
	return nil
}

func (b *memBirths) SetVersion(_ context.Context, id, version string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	v := b.m[id]
	v.Version = version
	b.m[id] = v
	return nil
}

func (b *memBirths) Delete(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, id)
	return nil
}

// acctFake gives each provider a profile folder on the machine.
type acctFake struct {
	dirs map[providers.ID]string
	err  error
}

func (a *acctFake) Resolve(_ context.Context, p providers.ID, account string) (accounts.Profile, error) {
	if a.err != nil {
		return accounts.Profile{}, a.err
	}
	name := account
	if name == "" {
		name = string(p) + "-padrao"
	}
	return accounts.Profile{Name: name, Provider: p, Dir: a.dirs[p], Default: true, Step: accounts.StepDone}, nil
}

// channelsFake switches on the fallbacks of a provider.
type channelsFake map[providers.ID]providers.LayerFallbacks

func (c channelsFake) LayerFallbacks(_ context.Context, p providers.ID, _ string) (providers.LayerFallbacks, error) {
	return c[p], nil
}

// modelsFake keeps several versions of the models.
type modelsFake struct {
	mu   sync.Mutex
	cur  string
	sets map[string]sessionzero.ModelSet
}

func (m *modelsFake) Current() sessionzero.ModelSet {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sets[m.cur]
}

func (m *modelsFake) Version(v string) (sessionzero.ModelSet, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sets[v]
	return s, ok
}

func (m *modelsFake) use(v string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cur = v
}

// versionOf is the built-in models with another version and a rule that
// tells the version.
func versionOf(v string) sessionzero.ModelSet {
	s := sessionzero.BuiltinModels{}.Current()
	s.Version = v
	rules := map[sessionzero.Role][]sessionzero.Rule{}
	for role, rs := range s.Rules {
		rules[role] = append(slices.Clone(rs), sessionzero.Rule{Text: "Rule that exists in the models v" + v + "."})
	}
	s.Rules = rules
	return s
}

// charRig is the machine of a character: the worktree of a table with its
// shared files, the account profiles, the agentes folder and a Character.
type charRig struct {
	*tableEnv
	agents   string
	profiles map[providers.ID]string
	births   *memBirths
	accounts *acctFake
	channels channelsFake
	ch       *sessionzero.Character
	opts     sessionzero.CharacterOptions
	mestre   string
}

func newCharRig(t *testing.T, tweak func(*sessionzero.CharacterOptions)) *charRig {
	t.Helper()
	te := newTableEnv(t, nil, nil)
	te.req.Profiles = te.profiles()
	te.prepare()
	reg := providers.NewRegistry(providers.Options{})
	r := &charRig{tableEnv: te, agents: filepath.Join(t.TempDir(), "agentes"), profiles: map[providers.ID]string{},
		births: &memBirths{m: map[string]sessionzero.Birth{}}, channels: channelsFake{}, mestre: filepath.Join(t.TempDir(), "bin", "mestre")}
	for _, p := range te.profiles() {
		r.profiles[p.Provider] = p.Dir
	}
	r.accounts = &acctFake{dirs: r.profiles}
	r.opts = sessionzero.CharacterOptions{
		Providers: reg, Binary: r.mestre, Endpoint: "unix:///run/mestre/cli.sock", Agents: r.agents,
		GlobalGitConfig: filepath.Join(t.TempDir(), "home", ".gitconfig"),
		Accounts:        r.accounts, Births: r.births, Files: &classes.OSNativeFiles{},
		Native:   &classes.NativeSync{Adapters: reg, Files: &classes.OSNativeFiles{}},
		Channels: r.channels, Masker: logging.NewMasker(),
	}
	if tweak != nil {
		tweak(&r.opts)
	}
	r.rebuildChar()
	return r
}

func (r *charRig) rebuildChar() {
	r.t.Helper()
	ch, err := sessionzero.NewCharacter(r.opts)
	if err != nil {
		r.t.Fatal(err)
	}
	r.ch = ch
}

var agentCommand = map[string]string{presets.AgentClaudeCode: "claude", presets.AgentCodex: "codex", presets.AgentAntigravity: "agy", presets.AgentShell: "bash"}

// snapshot is a sheet and a base for an agent type.
func snapshot(agent string) presets.Snapshot {
	ready := presets.Ready{Mode: presets.ReadyHook}
	if agent == presets.AgentShell {
		ready = presets.Ready{Mode: presets.ReadyPattern, Pattern: `\$ $`}
	}
	return presets.Snapshot{
		Sheet: presets.Sheet{Name: "Dev " + agent, Base: agent, Agent: agent, Class: "dev"},
		Base: presets.Base{Name: agent, Command: agentCommand[agent], Ready: ready,
			Platforms: []presets.Platform{{OS: presets.Linux}, {OS: presets.Windows}}},
	}
}

// rulesOf are effective rules that reach the native controls.
func rulesOf(id string) *classes.Rules {
	return &classes.Rules{
		Version: 1, AgentID: id, Class: "dev",
		Files:    classes.FileRules{Read: []string{"**"}, Write: []string{"src/**"}, Deny: []string{".env"}},
		Commands: classes.PatternRules{Allow: []string{"go test *"}, Default: classes.Ask},
		Git:      classes.GitRules{Commit: classes.Allow, RequestPush: classes.Ask, Workflows: classes.WorkflowsNever},
		Network:  classes.NetworkRules{},
		Tools:    classes.PatternRules{Default: classes.Ask},
	}
}

var devIdentity = sessionzero.Identity{Name: "lucas-mestre-dev[bot]", Email: "123+lucas-mestre-dev[bot]@users.noreply.github.com"}

// classOf is an approved class with a CLASS.md of the given size and a
// skill of its own.
func classOf(body string) *classes.Class {
	skill := "---\nname: dev-cuidados\ndescription: Care to take when changing the code.\n---\n# Care\n\nRun the tests.\n"
	return &classes.Class{Name: "dev", Body: body, Meta: classes.Meta{Name: "dev", Skills: []string{"dev-cuidados"}},
		Files: classes.Files{{Path: "CLASS.md", Data: []byte(body)}, {Path: "skills/dev-cuidados/SKILL.md", Data: []byte(skill)}}}
}

// req is the request for a character of an agent type and level.
func (r *charRig) req(id, agent string, role sessionzero.Role) sessionzero.CharacterRequest {
	q := sessionzero.CharacterRequest{
		ID: id, Name: id, Role: role, Workspace: "meu-app", Machine: "cidadela", Dir: r.dir,
		Table:    sessionzero.TableContext{ID: "3-login", Branch: "mestre/3-login", Epic: 12},
		Snapshot: snapshot(agent), Rules: rulesOf(id), Identity: devIdentity, NewSessionID: "11111111-2222-4333-8444-555555555555",
	}
	switch role {
	case sessionzero.RoleCharacter:
		q.Class = classOf("# Dev\n\nWrite the code and the tests.\n")
	case sessionzero.RoleGrandMaster:
		q.Table = sessionzero.TableContext{}
		q.Name = "Grão-Mestre"
	case sessionzero.RoleTableMaster:
		q.Name = "Mestre"
	}
	return q
}

func (r *charRig) prepare(q sessionzero.CharacterRequest) sessionzero.CharacterPlan {
	r.t.Helper()
	plan, err := r.ch.Prepare(r.ctx, q)
	if err != nil {
		r.t.Fatalf("prepare %s: %v", q.ID, err)
	}
	return plan
}

// has reports whether a file of the character folder exists.
func (r *charRig) has(id string, rel ...string) bool {
	_, err := os.Lstat(filepath.Join(append([]string{r.dir, ".mestre", "agents", id}, rel...)...))
	return err == nil
}

// envOf is the value of a variable of a command's environment ("" and false
// when it is not set).
func envOf(env []string, key string) (string, bool) {
	v, ok := "", false
	for _, kv := range env {
		if k, val, found := strings.Cut(kv, "="); found && k == key {
			v, ok = val, true
		}
	}
	return v, ok
}

// readTOMLBasicString reads a TOML basic string (the whole of s) by the rules
// of the TOML spec.
func readTOMLBasicString(s string) (string, error) {
	if len(s) < 2 || s[0] != '"' || s[len(s)-1] != '"' {
		return "", errors.New("not between quotes")
	}
	in := []rune(s[1 : len(s)-1])
	var b strings.Builder
	for i := 0; i < len(in); i++ {
		r := in[i]
		switch {
		case r == '"':
			return "", errors.New("unescaped quote")
		case r < 0x20 && r != '\t' || r == 0x7f:
			return "", errors.New("raw control character")
		case r != '\\':
			b.WriteRune(r)
			continue
		}
		i++
		if i >= len(in) {
			return "", errors.New("trailing backslash")
		}
		switch in[i] {
		case 'b':
			b.WriteByte('\b')
		case 't':
			b.WriteByte('\t')
		case 'n':
			b.WriteByte('\n')
		case 'f':
			b.WriteByte('\f')
		case 'r':
			b.WriteByte('\r')
		case '"', '\\':
			b.WriteRune(in[i])
		case 'u', 'U':
			n := 4
			if in[i] == 'U' {
				n = 8
			}
			if i+n >= len(in) {
				return "", errors.New("short unicode escape")
			}
			var v rune
			for _, h := range in[i+1 : i+1+n] {
				d := strings.IndexRune("0123456789abcdef", rune(strings.ToLower(string(h))[0]))
				if d < 0 {
					return "", errors.New("bad hex digit")
				}
				v = v*16 + rune(d)
			}
			b.WriteRune(v)
			i += n
		default:
			return "", errors.New("unknown escape")
		}
	}
	return b.String(), nil
}
