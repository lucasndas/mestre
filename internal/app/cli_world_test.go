package app_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/app"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/cli"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/orchestration/rituals"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/localrig"
	"github.com/lucasndas/mestre/internal/testkit/orcfakes"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The world of the CLI of communication and coordination: the real
// orchestration of the Citadel over the doubles of the other milestones, with
// the real CLI running in the terminal of a character, through the local
// channel and the Gate of the conventions (docs/cli/comunicacao.md,
// docs/cli/coordenacao.md).

// commPresets has the sheets and bases of the team; "QA Codex" asks for a GPU
// that no machine has.
type commPresets struct{}

var commSheets = map[string]presets.Sheet{
	"Dev Claude":     {Name: "Dev Claude", Base: "claude-code", Agent: presets.AgentClaudeCode, Class: "Dev", Lifecycle: presets.Persistent},
	"Dev Codex":      {Name: "Dev Codex", Base: "codex", Agent: presets.AgentCodex, Class: "Dev", Lifecycle: presets.Persistent},
	"Revisor Codex":  {Name: "Revisor Codex", Base: "codex", Agent: presets.AgentCodex, Class: "Revisor", Lifecycle: presets.Persistent},
	"QA Codex":       {Name: "QA Codex", Base: "codex", Agent: presets.AgentCodex, Class: "QA", Requires: map[string]any{"gpu": true}},
	"Mestre da mesa": {Name: "Mestre da mesa", Base: "claude-code", Agent: presets.AgentClaudeCode, Class: "Mestre", Lifecycle: presets.Persistent},
	"Grão-Mestre":    {Name: "Grão-Mestre", Base: "claude-code", Agent: presets.AgentClaudeCode, Class: "Grão-Mestre", Lifecycle: presets.Persistent},
	"Operador":       {Name: "Operador", Base: presets.OperatorBase, Agent: presets.AgentClaudeCode, Class: "Operador"},
}

var commBases = map[string]presets.Base{
	"claude-code":        {Name: "claude-code", Command: "claude", Env: map[string]string{"TOKEN": "s3cr3t-value"}},
	"codex":              {Name: "codex", Command: "codex"},
	"bash":               {Name: "bash", Command: "bash", Platforms: []presets.Platform{{OS: "linux"}}, Ready: presets.Ready{Mode: presets.ReadyDelay, Delay: 1}},
	presets.OperatorBase: {Name: presets.OperatorBase, Command: "operator"},
}

func (commPresets) Snapshot(_ context.Context, sheet string) (presets.Snapshot, error) {
	s, ok := commSheets[sheet]
	if !ok {
		return presets.Snapshot{}, fmt.Errorf("no sheet %s", sheet)
	}
	return presets.Snapshot{Sheet: s, Base: commBases[s.Base]}, nil
}

func (commPresets) Base(_ context.Context, name string) (presets.Base, bool, error) {
	b, ok := commBases[name]
	return b, ok, nil
}

func (commPresets) Choose(_ context.Context, sheet, _ string) (machinesheet.Choice, error) {
	if sheet == "QA Codex" {
		return machinesheet.Choice{Verdicts: []machinesheet.Verdict{
			{MachineID: "cidadela", Name: "Cidadela", Unmet: "gpu", Reason: "sem GPU"},
			{MachineID: "torre", Name: "windows-pc", Unmet: "gpu", Reason: "sem GPU"},
		}}, nil
	}
	return machinesheet.Choice{Verdicts: []machinesheet.Verdict{{MachineID: "cidadela", Name: "Cidadela", OK: true}}}, nil
}

func (commPresets) Machines(context.Context) ([]machinesheet.Sheet, error) {
	return []machinesheet.Sheet{{MachineID: "cidadela", Name: "Cidadela", OS: "linux", Online: true}}, nil
}

func (commPresets) Sheets(context.Context) ([]presets.Sheet, error) {
	var out []presets.Sheet
	for _, s := range commSheets {
		out = append(out, s)
	}
	return out, nil
}

func (commPresets) Bases(context.Context) ([]presets.Base, error) {
	var out []presets.Base
	for _, b := range commBases {
		out = append(out, b)
	}
	return out, nil
}

// commEpics knows the epics 42 and 43.
type commEpics struct{ orcfakes.Epics }

func (commEpics) Epic(_ context.Context, _ string, n int) (tables.Epic, error) {
	switch n {
	case 42:
		return tables.Epic{Number: 42, Title: "Login de usuários", Labels: []string{"epico"}, Open: true}, nil
	case 43:
		return tables.Epic{Number: 43, Title: "Relatórios", Labels: []string{"epico"}, Open: true}, nil
	}
	return tables.Epic{}, fmt.Errorf("%w: #%d", tables.ErrEpicNotFound, n)
}

// commMachines are the orcfakes machines that also keep the raw input typed
// and show a screen.
type commMachines struct {
	*orcfakes.Machines
	mu     sync.Mutex
	raw    map[string][]byte
	screen map[string]string
}

func (m *commMachines) Input(_ context.Context, _, id string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.raw[id] = append(m.raw[id], data...)
	return nil
}

func (m *commMachines) Screen(_ context.Context, _, id string) (characters.Screen, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return characters.Screen{Text: m.screen[id]}, nil
}

func (m *commMachines) typedRaw(id string) []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]byte(nil), m.raw[id]...)
}

// commAttachments copies the attachments of a message into the table of the
// target.
type commAttachments struct{ table string }

func (a *commAttachments) Forward(_ context.Context, _, table string, paths []string) ([]string, error) {
	a.table = table
	out := make([]string, len(paths))
	for i, p := range paths {
		out[i] = ".mestre/anexos/" + filepath.Base(p)
	}
	return out, nil
}

type commOperator struct{ got []characters.OperatorRequest }

func (o *commOperator) Request(_ context.Context, r characters.OperatorRequest) (string, error) {
	o.got = append(o.got, r)
	return "7", nil
}

var (
	templateOnce sync.Once
	templateDB   []byte
	templateErr  error
)

// commTemplate is the Guild database migrated once; every world starts from a
// copy of it (docs/qualidade/testes.md).
func commTemplate(t *testing.T) []byte {
	t.Helper()
	templateOnce.Do(func() {
		dir, err := os.MkdirTemp("", "app-comm-template")
		if err != nil {
			templateErr = err
			return
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "mestre.db")
		db, err := guilddb.Open(context.Background(), path, store.Options{Synchronous: "OFF"})
		if err != nil {
			templateErr = err
			return
		}
		if templateErr = db.Close(); templateErr == nil {
			templateDB, templateErr = os.ReadFile(path)
		}
	})
	if templateErr != nil {
		t.Fatal(templateErr)
	}
	return templateDB
}

// commWorld is a Citadel with a workspace, a Grand Master and one table whose
// master is idle.
type commWorld struct {
	t      *testing.T
	ctx    context.Context
	clk    *clock.Fake
	orc    *app.Orchestration
	mach   *commMachines
	atts   *commAttachments
	oper   *commOperator
	rig    *localrig.Rig
	slug   string
	gm     characters.Character
	master characters.Character
	table  tables.Table

	// files is the file system the CLI reads; stdin its standard input.
	files map[string][]byte
	stdin string
	// sizes overrides the size of a file for --attach.
	sizes map[string]int64

	// The classes and the rituals (stage 11): the library of the workspace,
	// the MESTRE_TMP of the callers (a real folder), the folders the CLI reads
	// with --from, the pre-run of the rituals and the doubles around them.
	lib     *classes.Library
	tmp     string
	trees   map[string]map[string]string
	pre     *commPreRun
	deleted []classes.DeleteRequest
	allowed bool

	// The Citadel side of the effective rules (withCitadelClasses): the
	// builder, the publisher, the store of this machine and the profiles of
	// the gate, when the world has them.
	cc       *app.CitadelClasses
	pub      *app.RulesPublisher
	local    *classes.Store
	agents   string
	profiles cli.Profiles
}

// commPreRun is the machine running the pre-run of a ritual; its output is what
// the test says.
type commPreRun struct {
	mu  sync.Mutex
	out string
}

func (p *commPreRun) Run(_ context.Context, r rituals.PreRunRequest) (rituals.PreRunResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return rituals.PreRunResult{Output: p.out}, nil
}

type commPermissions struct{}

func (commPermissions) Allow(context.Context, characters.Character, string) (rituals.Decision, error) {
	return rituals.Decision{Allow: true}, nil
}

type commCeiling struct{ c *classes.Ceiling }

func (c commCeiling) Current(context.Context) (*classes.Ceiling, error) { return c.c, nil }

func newCommWorld(t *testing.T, setup ...func(*commWorld, *app.OrchestrationOptions)) *commWorld {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC))
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "mestre.db"), commTemplate(t), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := guilddb.Open(ctx, filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	git := fakegit.NewGit(clk)
	remote, err := git.Init(ctx, filepath.Join(root, "remote", "meu-app"), gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	git.Handle(remote.Dir()).Data().Commit("main", "chore: initial commit", map[string]string{"README.md": "# Meu App\n"})

	w := &commWorld{t: t, ctx: ctx, clk: clk, files: map[string][]byte{}, atts: &commAttachments{}, oper: &commOperator{}, tmp: t.TempDir(),
		trees: map[string]map[string]string{}, pre: &commPreRun{}}
	w.lib = &classes.Library{Roots: classes.DefaultRoots(filepath.Join(root, "classes-globais"), filepath.Join(root, "clone")), Records: classes.ClassDB{DB: db, Clock: clk},
		Ceiling: commCeiling{classes.DefaultCeiling()}, Clock: clk, OS: "linux", Skill: func(string) bool { return false }}
	w.mach = &commMachines{Machines: orcfakes.NewMachines(clk), raw: map[string][]byte{}, screen: map[string]string{}}
	w.mach.Chars = func() *characters.Service { return w.orc.Characters }
	oo := app.OrchestrationOptions{Guild: db, Machine: "cidadela", Clock: clk, WorkspacesDir: filepath.Join(root, "workspaces"),
		TempDir: filepath.Join(root, "tmp"), Git: git, Machines: w.mach, Presets: characters.Presets(commPresets{}), Builder: orcfakes.Builder{},
		GitHub: orcfakes.Hub{URL: remote.Dir()}, Epics: commEpics{}, Alerts: orcfakes.Alerts{}, Files: orcfakes.Files{}, Messages: orcfakes.Messages{},
		Peers: &orcfakes.Peers{Root: filepath.Join(root, "towers")}, Users: &orcfakes.Users{}, Destructive: orcfakes.Destructive{},
		MessageAttachments: w.atts, Operator: w.oper,
		ClassLibraries: func(string) *classes.Library { return w.lib }, ClassTmp: func(context.Context, string) (string, error) { return w.tmp, nil },
		ClassDestructive: func(_ context.Context, d classes.DeleteRequest) (bool, error) {
			w.deleted = append(w.deleted, d)
			return w.allowed, nil
		},
		RitualPreRun: w.pre, RitualPermissions: commPermissions{}}
	for _, f := range setup {
		f(w, &oo)
	}
	w.orc, err = app.NewOrchestration(oo)
	if err != nil {
		t.Fatal(err)
	}
	w.mach.Confirm = w.orc.Inbox.Confirmed
	ws, err := w.orc.Workspaces.Create(ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := w.orc.Workspaces.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := w.orc.Workspaces.Prepare(ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	w.slug = ws.Slug
	w.lib.Workspace = ws.Slug
	res, err := w.orc.Characters.Create(ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster,
		Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		t.Fatal(err)
	}
	w.gm = res.Character
	w.table, w.master = w.openTable(42)

	fwd := localchan.NewLocalForwarder()
	w.orc.Register(fwd, nil)
	profiles := w.profiles
	if profiles == nil {
		profiles = cli.ProfilesFunc(func(ctx context.Context, id string) cli.Profile {
			c, err := w.orc.Registry.Get(ctx, id)
			if err != nil {
				return cli.Profile{}
			}
			return cli.Profile{Role: map[characters.Role]cli.Role{
				characters.RoleCharacter: cli.RoleCharacter, characters.RoleTableMaster: cli.RoleTableMaster, characters.RoleGrandMaster: cli.RoleGrandMaster,
			}[c.Role]}
		})
	}
	gate := cli.NewGate(cli.GateConfig{Next: fwd, Profiles: profiles})
	w.rig = localrig.New(clock.Real{}, localrig.Options{Config: localchan.Config{
		Forwarder: gate, Local: map[string]localchan.Handler{localchan.CmdHelp: gate.Help()},
	}})
	w.rig.Start(t)
	return w
}

// openTable opens the table of an epic; its master finishes its first turn.
func (w *commWorld) openTable(epic int) (tables.Table, characters.Character) {
	w.t.Helper()
	opened, err := w.orc.Tables.Open(w.ctx, tables.OpenRequest{By: w.gm, Epic: epic})
	if err != nil {
		w.t.Fatal(err)
	}
	w.mach.Report(w.ctx, opened.Master.ID, characters.Idle)
	return opened.Table, opened.Master
}

// result is what a run of the CLI left behind.
type result struct {
	code           int
	stdout, stderr string
}

// run runs `mestre` as a process of the terminal of the character.
func (w *commWorld) run(c characters.Character, args ...string) result {
	w.t.Helper()
	term, host := "t-"+c.ID, "proc-"+c.ID
	w.rig.Character(term, c.ID)
	w.rig.Process(host, term)
	var stdout, stderr bytes.Buffer
	code := cli.Run(w.ctx, cli.Env{
		Getenv: func(k string) string {
			switch k {
			case localchan.EnvAgentID:
				return c.ID
			case "MESTRE_TMP":
				return w.tmp
			}
			return ""
		},
		RealPath: func(p string) (string, error) { return filepath.Clean(p), nil },
		ReadTree: func(dir string, _ int64) (map[string]string, error) {
			t, ok := w.trees[filepath.ToSlash(dir)]
			if !ok {
				return nil, fmt.Errorf("open %s: %w", dir, os.ErrNotExist)
			}
			return t, nil
		},
		Stdout: &stdout, Stderr: &stderr, Clock: w.rig.Clock, Cwd: "/work", Version: "1.0.0", Stdin: strings.NewReader(w.stdin),
		Dial:       func(string) localchan.Dialer { return w.rig.Dialer(host) },
		FileExists: func(string) bool { return true },
		ReadFile: func(path string, max int64) ([]byte, error) {
			b, ok := w.files[filepath.ToSlash(path)]
			if !ok {
				return nil, fmt.Errorf("open %s: %w", path, os.ErrNotExist)
			}
			return b[:min(int64(len(b)), max)], nil
		},
		FileSize: func(path string) (int64, error) {
			path = filepath.ToSlash(path)
			if n, ok := w.sizes[path]; ok {
				return n, nil
			}
			b, ok := w.files[path]
			if !ok {
				return 0, fmt.Errorf("stat %s: %w", path, os.ErrNotExist)
			}
			return int64(len(b)), nil
		},
	}, args)
	return result{code, stdout.String(), stderr.String()}
}

type jsonOut struct {
	OK    bool           `json:"ok"`
	Data  map[string]any `json:"data"`
	Error struct {
		Code, Message, Hint string
		Details             map[string]any
	}
}

func decodeJSON(t *testing.T, r result) jsonOut {
	t.Helper()
	var v jsonOut
	dec := json.NewDecoder(strings.NewReader(r.stdout))
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("stdout is not JSON: %q (%v) stderr %q", r.stdout, err, r.stderr)
	}
	if _, err := dec.Token(); err != io.EOF {
		t.Fatalf("stdout is more than one JSON object: %q", r.stdout)
	}
	return v
}

// recruit makes a character of the table as the master would, through the CLI.
func (w *commWorld) recruit(name, sheet string) characters.Character {
	w.t.Helper()
	if r := w.run(w.master, "recruit", name, "--sheet", sheet); r.code != 0 {
		w.t.Fatalf("recruit %s: %+v", name, r)
	}
	c, err := w.orc.Registry.ByName(w.ctx, w.slug, w.table.ID, name)
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

func (w *commWorld) get(c characters.Character) characters.Character {
	w.t.Helper()
	got, err := w.orc.Registry.Get(w.ctx, c.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

// turnEnds is the end of a turn: the Runtime tells the Citadel first, then
// the state changes.
func (w *commWorld) turnEnds(c characters.Character, last string) {
	w.t.Helper()
	if err := w.orc.Observers.TurnEnded(w.ctx, c, last); err != nil {
		w.t.Fatal(err)
	}
	w.mach.Report(w.ctx, c.ID, characters.Idle)
}

func decodeInto(s string, v any) error { return json.Unmarshal([]byte(s), v) }
