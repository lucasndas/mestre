//go:build full

package characters_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/machinesheet"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	machinedb "github.com/lucasndas/mestre/internal/store/machine"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit/sheetfake"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The full suite of the characters starts real processes: the mestre-pty, the
// `mestre` binary (whose `mestre hook` the fake agent calls) and the fake
// agent, built here once. The fake agent is built as `claude` and found in the
// PATH, as the base claude-code finds the real one.
var fullBuilds struct {
	dir, bin, pty, mestre, claude string
}

var fullOnce sync.Once
var fullErr error

const fullPTYVersion = "1.0.0"

func buildFull(t *testing.T) {
	t.Helper()
	fullOnce.Do(func() {
		dir, err := os.MkdirTemp("", "mcbin")
		if err != nil {
			fullErr = err
			return
		}
		exitHooks = append(exitHooks, func() { _ = os.RemoveAll(dir) })
		exe := ""
		if runtime.GOOS == "windows" {
			exe = ".exe"
		}
		fullBuilds.dir, fullBuilds.bin = dir, filepath.Join(dir, "bin")
		fullBuilds.pty = filepath.Join(dir, "pty", fullPTYVersion, "mestre-pty"+exe)
		fullBuilds.mestre = filepath.Join(dir, "mestre"+exe)
		fullBuilds.claude = filepath.Join(fullBuilds.bin, "claude"+exe)
		for _, b := range [][]string{
			{"-o", fullBuilds.pty, "-ldflags", "-X main.version=" + fullPTYVersion, "github.com/lucasndas/mestre/cmd/mestre-pty"},
			{"-o", fullBuilds.mestre, "github.com/lucasndas/mestre/cmd/mestre"},
			{"-o", fullBuilds.claude, "github.com/lucasndas/mestre/cmd/fakeagent"},
		} {
			if out, err := exec.Command("go", append([]string{"build"}, b...)...).CombinedOutput(); err != nil {
				fullErr = fmt.Errorf("go build %v: %v\n%s", b, err, out)
				return
			}
		}
	})
	if fullErr != nil {
		t.Fatal(fullErr)
	}
}

// stateWatch is the Reporter of the machine in the full tests: it hands the
// reports to the Citadel and wakes whoever waits for a state.
type stateWatch struct {
	svc *characters.Service
	mu  sync.Mutex
	ch  chan struct{}
}

func (w *stateWatch) Report(ctx context.Context, r characters.Report) error {
	err := w.svc.Report(ctx, r)
	w.mu.Lock()
	close(w.ch)
	w.ch = make(chan struct{})
	w.mu.Unlock()
	return err
}

// until waits, without polling, until cond holds after a report.
func (w *stateWatch) until(t *testing.T, what string, cond func() bool) {
	t.Helper()
	limit := clock.Real{}.NewTimer(90 * time.Second)
	defer limit.Stop()
	for {
		w.mu.Lock()
		ch := w.ch
		w.mu.Unlock()
		if cond() {
			return
		}
		select {
		case <-ch:
		case <-t.Context().Done():
			t.Fatalf("never: %s", what)
		case <-limit.C():
			t.Fatalf("not in 90 s: %s", what)
		}
	}
}

// fullPrep is the session zero of the full tests: the hook files of the
// provider, the trust of the folder and the command of the fake agent.
type fullPrep struct {
	f *fullRig

	mu       sync.Mutex
	launched []providers.Launch
}

func (p *fullPrep) Prepare(_ context.Context, req sessionzero.CharacterRequest) (sessionzero.CharacterPlan, error) {
	f := p.f
	ad := providers.NewClaudeCode(providers.Options{})
	files, err := ad.HookFiles(providers.HookFilesRequest{OS: runtime.GOOS, Root: req.Dir, Binary: fullBuilds.mestre})
	if err != nil {
		return sessionzero.CharacterPlan{}, err
	}
	for _, mf := range files {
		data, err := mf.Merge(nil)
		if err != nil {
			return sessionzero.CharacterPlan{}, err
		}
		if err := os.MkdirAll(filepath.Dir(mf.Path), 0o755); err != nil {
			return sessionzero.CharacterPlan{}, err
		}
		if err := os.WriteFile(mf.Path, data, 0o644); err != nil {
			return sessionzero.CharacterPlan{}, err
		}
	}
	if _, err := ad.PreTrust(providers.PreTrustRequest{OS: runtime.GOOS, Profile: f.cfg, Worktree: req.Dir, RepoRoot: req.Dir}); err != nil {
		return sessionzero.CharacterPlan{}, err
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		return sessionzero.CharacterPlan{}, err
	}
	args := []string{}
	session := req.NewSessionID
	switch {
	case req.ResumeSessionID != "":
		args, session = append(args, "--resume", req.ResumeSessionID), req.ResumeSessionID
	case req.NewSessionID != "":
		args = append(args, "--session-id", req.NewSessionID)
	}
	script := f.scripts[req.Name]
	if script == "" {
		script = f.scripts["quick"]
	}
	env := append(os.Environ(),
		localchan.EnvAgentID+"="+req.ID, localchan.EnvEndpoint+"="+localchan.EndpointURL(f.sock), localchan.EnvCLI+"="+fullBuilds.mestre,
		"MESTRE_MESA="+req.Table.ID, "FAKEAGENT_START_SCREEN="+f.startScreens[req.Name], "HOME="+f.root, "CLAUDE_CONFIG_DIR="+f.cfg, "FAKEAGENT_SCRIPT="+script, "GORACE=atexit_sleep_ms=0")
	plan := sessionzero.CharacterPlan{ID: req.ID, Provider: providers.ClaudeCode, Tmp: filepath.Join(f.root, "tmp", req.ID)}
	plan.Launch = providers.Launch{Path: claude, Args: args, Env: env, Dir: req.Dir, SessionID: session, Resumed: req.ResumeSessionID != ""}
	p.mu.Lock()
	p.launched = append(p.launched, plan.Launch)
	p.mu.Unlock()
	return plan, nil
}

func (p *fullPrep) End(context.Context, sessionzero.EndRequest) error { return nil }
func (p *fullPrep) Contexts() hook.Contexts                           { return fakeContexts{} }
func (p *fullPrep) TakeUpdate(string) string                          { return "" }

// fullEngine is the engine of the machine: the terminals of internal/terminal,
// the runtime, the hook engine and the local channel on a real socket with
// the real identification of the caller.
type fullEngine struct {
	host *characters.TerminalHost
	mgr  *terminal.Manager
	rt   *characters.Runtime
	stop func()
	// recovered is what the engine found when it started.
	recovered characters.RecoverReport
}

type fullRig struct {
	t               *testing.T
	ctx             context.Context
	root, work, cfg string
	sock            string
	gdb, mdb        *store.DB
	reg             characters.Registry
	svc             *characters.Service
	rules           *classes.Store
	sys             terminal.System
	spawner         *terminal.DirectSpawner
	notes           *noticeLog
	waits           *fakeWaits
	inbox           *fakeInbox
	watch           *stateWatch
	prep            *fullPrep
	scripts         map[string]string
	// startScreens are the start screens no adapter knows, by character.
	startScreens map[string]string
	machines     characters.MachineSet
	eng          *fullEngine
}

func newFullRig(t *testing.T) *fullRig {
	t.Helper()
	buildFull(t)
	t.Setenv("PATH", fullBuilds.bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	// Unix socket paths are short (about 100 bytes): a folder of its own.
	root, err := os.MkdirTemp("", "mc")
	if err != nil {
		t.Fatal(err)
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	ctx, cancel := clock.WithTimeout(t.Context(), clock.Real{}, 4*time.Minute)
	t.Cleanup(cancel)
	f := &fullRig{t: t, ctx: ctx, root: root, work: filepath.Join(root, "work"), cfg: filepath.Join(root, "cfg"), sock: filepath.Join(root, "cli.sock"),
		sys: terminal.NewSystem(), notes: &noticeLog{}, waits: &fakeWaits{by: map[string]string{}}, inbox: &fakeInbox{pending: map[string]int{}},
		scripts: map[string]string{}, startScreens: map[string]string{}, machines: characters.MachineSet{}}
	f.spawner = &terminal.DirectSpawner{Suite: "full", System: f.sys}
	for rel, content := range map[string]string{
		"work/.git/HEAD":        "ref: refs/heads/main\n",
		"cfg/.credentials.json": "{}",
		"quick.yaml":            "prompts:\n  - match: \".*\"\n    steps:\n      - say: \"feito\"\n",
		"hang.yaml":             "prompts:\n  - match: \".*\"\n    steps:\n      - hang: true\n",
	} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f.scripts["quick"], f.scripts["Preso"] = filepath.Join(root, "quick.yaml"), filepath.Join(root, "hang.yaml")
	f.gdb = openCopy(t, "mestre.db", guildTemplate, guilddb.Open)
	f.mdb = openCopy(t, "maquina.db", machineTemplate, machinedb.Open)
	f.reg = characters.Registry{DB: f.gdb}
	if _, err := f.gdb.Writer().Exec(`INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, workspace); err != nil {
		t.Fatal(err)
	}
	machines := &fakeMachineSheets{sheets: map[string]machinesheet.Sheet{}}
	cat := &presets.Catalog{DB: f.gdb, Clock: clock.Real{}, Machines: machines, Users: f.reg}
	doc, err := presets.ParseDocument([]byte(presetsDoc))
	if err != nil {
		t.Fatal(err)
	}
	plan, err := cat.PlanImport(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.ApplyImport(ctx, plan, nil); err != nil {
		t.Fatal(err)
	}
	m := sheetfake.Linux("m1")
	if runtime.GOOS == "windows" {
		m = sheetfake.Windows("m1")
		m.SetProviders(machinesheet.Provider{Provider: machinesheet.ProviderClaudeCode, Profiles: []machinesheet.Profile{{Name: "claude-trabalho", Version: "2.1.0", LoggedIn: true, Conformant: true, Default: true}}})
	}
	machines.set(sheetOf(t, m, "m1", "m1", true))
	tables := &fakeTables{tables: map[string]characters.TableInfo{"3-login": {ID: "3-login", Workspace: workspace, State: characters.TableWorking}}}
	svc, err := characters.NewService(characters.ServiceOptions{
		Registry: f.reg, Clock: clock.Real{}, Presets: characters.CatalogPresets{Catalog: cat}, Tables: fullTables{tables, f.work}, Machines: f.machines,
		Builder: &fakeBuilder{last: map[string]characters.BuildInput{}}, Notifier: f.notes, Inbox: f.inbox,
	})
	if err != nil {
		t.Fatal(err)
	}
	f.svc, f.watch = svc, &stateWatch{svc: svc, ch: make(chan struct{})}
	f.prep = &fullPrep{f: f}
	f.rules = &classes.Store{DB: f.mdb, Clock: clock.Real{}, Agents: filepath.Join(root, "agentes")}
	f.startEngine()
	return f
}

// fullTables is the tables with one worktree for every machine.
type fullTables struct {
	*fakeTables
	dir string
}

func (f fullTables) Worktree(context.Context, string, string, string) (string, error) {
	return f.dir, nil
}

// startEngine starts the engine of the machine: what runs when the engine of
// a machine starts, or comes back.
func (f *fullRig) startEngine() *fullEngine {
	f.t.Helper()
	dir := localchan.NewDirectory(f.sys)
	var host *characters.TerminalHost
	host, err := characters.NewTerminalHost(terminal.ManagerConfig{
		DB: f.mdb, System: f.sys, MachineID: "m1", SocketDir: filepath.Join(f.root, "s"), LogDir: filepath.Join(f.root, "l"),
		Spawner: f.spawner, PTYBinary: func(string) (string, error) { return fullBuilds.pty, nil }, PTYVersion: fullPTYVersion,
	}, dir)
	if err != nil {
		f.t.Fatal(err)
	}
	rt, err := characters.NewRuntime(characters.RuntimeOptions{
		Machine: "m1", Clock: clock.Real{}, DB: f.mdb, Host: host, Prep: f.prep, Providers: providers.NewRegistry(providers.Options{}),
		Reporter: f.watch, Notifier: f.notes, Waits: f.waits, OS: runtime.GOOS, ScreenTimeout: 3 * time.Second,
	})
	if err != nil {
		f.t.Fatal(err)
	}
	f.machines["m1"] = rt
	// The terminals that survived are found before the local channel serves
	// anyone, as the engine does when it starts.
	recovered, err := rt.Recover(f.ctx)
	if err != nil {
		f.t.Fatal(err)
	}
	hk := &hook.Engine{Clock: clock.Real{}, Rules: f.rules, Ledger: f.rules, Agents: f.rules.Agents, Characters: rt, Contexts: rt, States: rt, OnEvent: rt.OnHook}
	srv := localchan.NewServer(localchan.Config{
		Clock: clock.Real{},
		Identifier: localchan.NewSystemIdentifier(localchan.SystemConfig{
			Terminals: dir, Jobs: dir, System: f.sys, Mode: localchan.ModeTree,
		}),
		Local:    map[string]localchan.Handler{localchan.CmdHook: hk},
		Instance: "test", EngineVersion: "test",
	})
	ln, err := localchan.Listen(f.sock, localchan.NoAccess{})
	if err != nil {
		f.t.Fatal(err)
	}
	served := make(chan struct{})
	sctx, scancel := context.WithCancel(f.ctx)
	go func() { defer close(served); _ = srv.Serve(sctx, ln) }()
	e := &fullEngine{host: host, mgr: host.Manager(), rt: rt, recovered: recovered}
	e.stop = func() {
		// What the death of the engine does to its terminals: the
		// connections close, and nothing else is touched.
		rt.Close()
		host.Close()
		scancel()
		<-served
	}
	f.eng = e
	f.t.Cleanup(func() {
		f.eng.stop()
		recs, _ := f.eng.mgr.Records(context.Background())
		for _, r := range recs {
			_ = f.spawner.KillCgroup(context.Background(), r.ID)
			if r.PTY.PID != 0 {
				_ = f.sys.KillTree(context.Background(), r.PTY, nil)
			}
		}
	})
	return e
}

// create makes a character of a sheet through the Citadel, and waits for its
// hook to make it idle.
func (f *fullRig) create(sheet, name string) characters.Character {
	f.t.Helper()
	res, err := f.svc.Create(f.ctx, characters.CreateRequest{Name: name, Sheet: sheet, Table: "3-login", Workspace: workspace, Creator: engine})
	if err != nil || res.Pending != "" {
		f.t.Fatalf("create %s: %v %+v", name, err, res)
	}
	c := res.Character
	f.waitState(c, characters.Idle)
	return c
}

func (f *fullRig) state(c characters.Character) characters.State {
	f.t.Helper()
	got, err := f.svc.Get(f.ctx, c.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	return got.State
}

func (f *fullRig) waitState(c characters.Character, want characters.State) {
	f.t.Helper()
	f.watch.until(f.t, fmt.Sprintf("%s is %s", c.Name, want), func() bool { return f.state(c) == want })
}

// changed returns a channel that closes at the next notice.
func (n *noticeLog) changed() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.ch == nil {
		n.ch = make(chan struct{})
	}
	return n.ch
}

// waitNotice waits, without polling, until a notice of the kind was sent: it
// comes after the report of the state, not with it.
func (f *fullRig) waitNotice(kind characters.NoticeKind) []characters.Notice {
	f.t.Helper()
	limit := clock.Real{}.NewTimer(90 * time.Second)
	defer limit.Stop()
	for {
		ch := f.notes.changed()
		if ns := f.notes.of(kind); len(ns) > 0 {
			return ns
		}
		select {
		case <-ch:
		case <-f.t.Context().Done():
			f.t.Fatalf("never: a notice %s", kind)
		case <-limit.C():
			f.t.Fatalf("not in 90 s: a notice %s", kind)
		}
	}
}

func (f *fullRig) log(c characters.Character) []characters.State {
	f.t.Helper()
	log, err := f.reg.Log(f.ctx, c.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []characters.State
	for _, l := range log {
		out = append(out, l.To)
	}
	return out
}

func contains(states []characters.State, s characters.State) bool {
	for _, x := range states {
		if x == s {
			return true
		}
	}
	return false
}

var _ = strings.Contains
