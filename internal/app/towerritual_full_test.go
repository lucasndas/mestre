//go:build full

package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/orchestration/rituals"
	"github.com/lucasndas/mestre/internal/orchestration/scrolls"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/safefile"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

// towerTableRig is a Citadel and a Tower with the worktree of a table on both:
// the table, its mirror and its worktree on the Citadel, the branch sent to the
// Tower, and a living character of the table there.
type towerTableRig struct {
	r                 *rig
	a, b              *inproc
	root, home        string
	ws, table, branch string
	citadelTree       string
	towerTree         string
	orc               *Orchestration
	now               time.Time
}

const (
	rigWorkspace = "meu-app"
	rigTable     = "3-login"
	rigBranch    = "mestre/3-login"
)

// newTowerTableRig makes the Citadel (which hosts the orchestration, with the
// integration given) and the Tower; withAgents gives the Tower its terminals
// and the fake agent.
func newTowerTableRig(t *testing.T, integ Integration, withAgents bool) *towerTableRig {
	t.Helper()
	if withAgents {
		buildRuntimeBinaries(t)
	}
	r := newRig(t)
	root, err := os.MkdirTemp("", "mrt")
	if err != nil {
		t.Fatal(err)
	}
	if rr, err := filepath.EvalSymlinks(root); err == nil {
		root = rr
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	r.root = root
	x := &towerTableRig{r: r, root: root, home: filepath.Join(root, "home"), ws: rigWorkspace, table: rigTable, branch: rigBranch}
	x.a, x.b = r.node("a"), nil
	x.b = r.node("b", x.a)
	x.a.paths.CLISocket = filepath.Join(root, "a", "cli.sock")
	x.b.paths.CLISocket = filepath.Join(root, "b", "cli.sock")
	for rel, content := range map[string]string{
		"cfg/.credentials.json": "{}",
		"quick.yaml":            "prompts:\n  - match: \".*\"\n    steps:\n      - say: \"feito\"\n",
	} {
		p := filepath.Join(x.home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	x.a.setup = func(e *Engine) {
		e.deps.HostOrchestration = true
		if integ != nil {
			e.deps.Integration = integ
		}
	}
	if withAgents {
		x.b.cfg.Terminais = &Terminals{Dir: runtimeBuilds.ptyFolde, Version: runtimePTYVersion}
		x.b.setup = func(e *Engine) {
			e.deps.Spawner = &terminal.DirectSpawner{Suite: "full", System: terminal.NewSystem()}
			e.deps.CharacterPrep = runtimePrep{cfg: filepath.Join(x.home, "cfg"), home: x.home, sock: x.b.paths.CLISocket}
		}
	}
	r.found(x.a, clock.Real{})
	r.start(x.a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return x.a.acting() && x.a.e.orc.Load() != nil })
	r.join(x.b, x.a, clock.Real{})
	r.start(x.b, clock.Real{})
	r.waitFor("b to connect", func() bool { return x.b.connected(1) })
	x.orc = x.a.e.orc.Load()
	ctx := t.Context()

	src := filepath.Join(root, "src")
	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, src, "init", "-q")
	if err := os.WriteFile(filepath.Join(src, "README.md"), []byte("# Meu App\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, src, "add", ".")
	git(t, src, "commit", "-q", "-m", "chore: initial commit")
	git(t, src, "branch", x.branch)
	mirror := filepath.Join(x.a.paths.Workspaces, x.ws, "espelho.git")
	if err := os.MkdirAll(filepath.Dir(mirror), 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "clone", "-q", "--bare", src, mirror)
	x.citadelTree = filepath.Join(x.a.paths.Workspaces, x.ws, "mesas", x.table)
	git(t, mirror, "worktree", "add", "-q", x.citadelTree, x.branch)
	if _, err := orcDB(t, x.a).Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES (?, 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, x.ws); err != nil {
		t.Fatal(err)
	}
	x.now = clock.Real{}.Now()
	if err := x.orc.Tables.Store().Insert(ctx, tables.Table{Workspace: x.ws, ID: x.table, Epic: 3, EpicTitle: "Login", Branch: x.branch, BaseBranch: "main",
		BaseCommit: git(t, src, "rev-parse", "HEAD"), State: tables.StateWorking, OpenedBy: "engine", CreatedAt: x.now, UpdatedAt: x.now}); err != nil {
		t.Fatal(err)
	}
	g, err := gitx.New(gitx.Options{Clock: clock.Real{}, Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record(ctx, x.citadelTree)
	if err != nil {
		t.Fatal(err)
	}
	if err := x.orc.Tables.Store().PutWorktree(ctx, tables.Worktree{Workspace: x.ws, Table: x.table, Machine: x.a.id, Dir: rec.Dir, GitDir: rec.GitDir, Common: rec.Common,
		LastCommit: git(t, src, "rev-parse", x.branch), CreatedAt: x.now, UpdatedAt: x.now}); err != nil {
		t.Fatal(err)
	}
	return x
}

// character inserts a character of the table in the registry.
func (x *towerTableRig) character(t *testing.T, id, name, machine string, st characters.State) characters.Character {
	t.Helper()
	c := characters.Character{ID: id, Name: name, Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: x.ws, Table: x.table, Machine: machine,
		Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", Dir: x.towerTree, State: st, CreatedBy: characters.Creator{Kind: characters.CreatedByEngine},
		Lifecycle: characters.Ephemeral, CreatedAt: x.now}
	if err := x.orc.Registry.Insert(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	return c
}

// sync sends the table to the Tower, which makes the worktree.
func (x *towerTableRig) sync(t *testing.T) {
	t.Helper()
	if err := x.orc.Tables.SyncTower(t.Context(), x.ws, x.table, x.b.id); err != nil {
		t.Fatalf("the sync of the table to the Tower: %v", err)
	}
	x.towerTree = filepath.Join(x.b.paths.Workspaces, x.ws, "mesas", x.table)
}

// CA-PERG-10: the copies of the scrolls of a table reach the worktree of the
// Tower where its characters are, read only, and a change of the scroll is
// copied again; the Tower writes only that path, in its own worktrees, and
// only for the Citadel it follows.
func TestTheCopiesOfTheScrollsReachTheWorktreeOfATower(t *testing.T) {
	t.Parallel()
	x := newTowerTableRig(t, nil, false)
	ctx := t.Context()
	x.sync(t)
	x.character(t, "c9", "Dev-9", x.b.id, characters.Idle)

	n, err := x.orc.Scrolls.Create(ctx, scrolls.CreateRequest{Workspace: x.ws, Table: x.table, Actor: scrolls.Engine, Name: "plano", Content: "# Plano\nprimeira versão\n"})
	if err != nil {
		t.Fatalf("the scroll: %v", err)
	}
	copyPath := filepath.Join(x.towerTree, filepath.FromSlash(scrolls.Rel(x.table, n.ID)))
	read := func() string { b, _ := os.ReadFile(copyPath); return string(b) }
	x.r.waitFor("the copy on the Tower", func() bool { return strings.Contains(read(), "primeira versão") })
	// read only, the way that holds on both systems: the file does not open for writing
	if f, err := os.OpenFile(copyPath, os.O_WRONLY|os.O_APPEND, 0); err == nil {
		_ = f.Close()
		t.Error("the copy on the Tower opens for writing")
	}
	if _, err := x.orc.Scrolls.Write(ctx, scrolls.WriteRequest{Workspace: x.ws, Table: x.table, Actor: scrolls.Engine, Name: "plano", Content: "# Plano\nsegunda versão\n"}); err != nil {
		t.Fatal(err)
	}
	x.r.waitFor("the changed copy on the Tower", func() bool { return strings.Contains(read(), "segunda versão") })
	// a copy that went missing is made again by the sync (a Tower back, a new character)
	if err := safefile.Remove(copyPath, nil); err != nil {
		t.Fatalf("remove the copy: %v", err)
	}
	if err := x.orc.Scrolls.Sync(ctx, x.ws, x.table); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(), "segunda versão") {
		t.Error("the sync did not bring the copy back")
	}
	// the copy of a scroll that was deleted goes (the deletion itself needs a user's explicit request: its own tests)
	if err := x.a.e.mach.Load().Remove(ctx, scrolls.Target{Machine: x.b.id, Dir: x.towerTree}, scrolls.Rel(x.table, n.ID)); err != nil {
		t.Fatalf("remove the copy on the Tower: %v", err)
	}
	if _, err := os.Stat(copyPath); !os.IsNotExist(err) {
		t.Errorf("the copy is still on the Tower: %v", err)
	}

	// the Tower writes nothing else, nowhere else, for nobody else
	router := protocol.NewRouter()
	x.b.e.registerTowerScrolls(router)
	citadel := protocol.Peer{Role: protocol.RoleMachine, ID: x.a.id}
	rel := scrolls.Rel(x.table, "p0001")
	for name, c := range map[string]struct {
		typ     string
		payload any
		from    protocol.Peer
	}{
		"another sender":              {protocol.TypeScrollPut, &protocol.ScrollPut{Dir: x.towerTree, Rel: rel, Content: "x"}, protocol.Peer{Role: protocol.RoleMachine, ID: "outra"}},
		"a path that leaves":          {protocol.TypeScrollPut, &protocol.ScrollPut{Dir: x.towerTree, Rel: ".mestre/mesas/" + x.table + "/pergaminhos/../../../x.md", Content: "x"}, citadel},
		"another file":                {protocol.TypeScrollPut, &protocol.ScrollPut{Dir: x.towerTree, Rel: "README.md", Content: "x"}, citadel},
		"the folder of another table": {protocol.TypeScrollPut, &protocol.ScrollPut{Dir: filepath.Join(x.b.paths.Workspaces, x.ws, "mesas", "9-outra"), Rel: rel, Content: "x"}, citadel},
		"a folder outside":            {protocol.TypeScrollPut, &protocol.ScrollPut{Dir: x.root, Rel: rel, Content: "x"}, citadel},
		"the removal of a file":       {protocol.TypeScrollRemove, &protocol.ScrollRemove{Dir: x.towerTree, Rel: "README.md"}, citadel},
	} {
		h, ok := router.Lookup(c.typ)
		if !ok {
			t.Fatalf("no handler of %s", c.typ)
		}
		if _, err := h.Handle(ctx, &protocol.Message{Env: protocol.Envelope{Type: c.typ}, Payload: c.payload, Peer: c.from}); err == nil {
			t.Errorf("%s: the Tower wrote it", name)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(x.towerTree, "README.md")); string(b) != "# Meu App\n" {
		t.Errorf("README.md changed on the Tower: %q", b)
	}
}

// stubRitualPermissions lets the pre-runs through: the decision of the
// permissions has its own tests; here the transport is what is looked at.
type stubRitualPermissions struct{}

func (stubRitualPermissions) Open(context.Context, IntegrationEnv) (IntegrationRun, error) {
	return stubRitualRun{}, nil
}

type stubRitualRun struct{}

func (stubRitualRun) Extend(o *OrchestrationOptions) { o.RitualPermissions = allowRituals{} }
func (stubRitualRun) Attach(*Orchestration, *localchan.LocalForwarder) []ExternalFunc {
	return nil
}
func (stubRitualRun) BindFence(*guild.Fence)        {}
func (stubRitualRun) FenceChanged(guild.FenceState) {}
func (stubRitualRun) Close()                        {}

type allowRituals struct{}

func (allowRituals) Allow(context.Context, characters.Character, string) (rituals.Decision, error) {
	return rituals.Decision{Allow: true}, nil
}

// CA-RIT-11: a ritual with a pre-run whose target is on a Tower fires: the
// command runs on the machine of the target, as the target, in its folder, and
// its output goes into what is delivered; with the Tower away the firing fails
// with the reason and nothing is delivered.
func TestThePreRunOfARitualRunsOnTheTowerOfItsTarget(t *testing.T) {
	t.Parallel()
	x := newTowerTableRig(t, stubRitualPermissions{}, true)
	ctx := t.Context()
	x.sync(t)
	cat := &presets.Catalog{DB: orcDB(t, x.a), Clock: clock.Real{}}
	doc, err := presets.ParseDocument([]byte("kind: mestre/presets\nversion: 1\nbases:\n  - {name: claude-code, command: claude, platforms: [{os: linux}, {os: windows, command: claude.exe}], ready: hook}\n" +
		"sheets:\n  - {name: Dev Claude, base: claude-code, agent: claude-code, class: Dev, max_copies: 200}\n"))
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
	dev := x.character(t, "c1", "Dev-1", x.b.id, characters.Starting)
	// the effective rules of the character on the Tower decide the pre-run there
	rules := &classes.Rules{Version: 2, AgentID: dev.ID, Class: "dev", GeneratedAt: x.now,
		Files:    classes.FileRules{Read: []string{"**"}, Write: []string{"src/**"}},
		Commands: classes.PatternRules{Allow: []string{"echo *", "pwd", "exit *"}, Default: classes.Deny},
		Git:      classes.GitRules{Commit: classes.Allow, RequestPush: classes.Ask, Workflows: classes.WorkflowsNever},
		Tools:    classes.PatternRules{Default: classes.Ask}}
	if err := (&classes.Store{DB: x.b.e.mdb, Clock: clock.Real{}, Agents: x.b.paths.Agentes}).Save(ctx, rules); err != nil {
		t.Fatal(err)
	}
	m := x.a.e.mach.Load()
	req := sessionzero.CharacterRequest{
		ID: dev.ID, Name: dev.Name, Role: dev.Role, Workspace: x.ws, Table: sessionzero.TableContext{ID: x.table}, Machine: x.b.id, Dir: x.towerTree,
		Snapshot: presets.Snapshot{Sheet: presets.Sheet{Name: dev.Sheet, Agent: "claude-code", Class: "Dev"}, Base: presets.Base{Name: "claude-code", Ready: presets.Ready{Mode: presets.ReadyHook}}},
	}
	if _, err := m.Start(ctx, x.b.id, characters.StartCommand{Character: dev.ID, Terminal: "t-c1", Kind: characters.KindCharacter, Request: req}); err != nil {
		t.Fatalf("the character did not start on the Tower: %v", err)
	}
	x.r.waitFor("the character idle", func() bool {
		c, err := x.orc.Registry.Get(ctx, dev.ID)
		return err == nil && c.State == characters.Idle
	})
	by := rituals.Actor{Kind: rituals.ByUser, ID: "lucas", Level: characters.LevelOwner}
	sch, err := x.orc.Rituals.ParseSchedule(ctx, x.ws, rituals.Spec{Every: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(name, pre string) rituals.Ritual {
		t.Helper()
		rt, err := x.orc.Rituals.Create(ctx, rituals.CreateRequest{Workspace: x.ws, Table: x.table, Name: name, Target: "Dev-1", Prompt: "Veja:\n" + rituals.Placeholder,
			Schedule: sch, PreRun: pre, By: by})
		if err != nil {
			t.Fatalf("the ritual: %v", err)
		}
		return rt
	}
	// a line both shells read the same: sh on Linux, PowerShell on Windows (where pwd is Get-Location)
	ok := create("quadro", "echo ola-da-torre; pwd")
	f, err := x.orc.Rituals.Run(ctx, ok.ID, by)
	if err != nil || f.State != rituals.Delivered {
		t.Fatalf("the firing: %+v, %v", f, err)
	}
	bodies := func() []string {
		t.Helper()
		rows, err := orcDB(t, x.a).Reader().QueryContext(ctx, `SELECT body FROM inbox_messages WHERE target = ? AND kind = ? ORDER BY seq`, dev.ID, string(inbox.KindRitual))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		var out []string
		for rows.Next() {
			var b string
			if err := rows.Scan(&b); err != nil {
				t.Fatal(err)
			}
			out = append(out, b)
		}
		return out
	}
	if got := bodies(); len(got) != 1 || !strings.Contains(got[0], "ola-da-torre") || !strings.Contains(got[0], x.towerTree) || strings.Contains(got[0], x.citadelTree) {
		t.Fatalf("what the ritual delivered: %q", got)
	}
	// the prompt of the first firing makes the target work; a firing waits in the
	// queue until it is free, and the loop of the rituals takes it from there
	settled := func(f rituals.Firing) rituals.Firing {
		t.Helper()
		x.r.waitFor("the firing to leave the queue", func() bool {
			g, err := x.orc.Rituals.Store().Firing(ctx, f.ID)
			f = g
			return err == nil && g.State != rituals.Queued && g.State != rituals.PreRunning
		})
		return f
	}
	failing := create("falha", "echo erro-da-torre; exit 3")
	f, err = x.orc.Rituals.Run(ctx, failing.ID, by)
	if f = settled(f); err != nil || f.State != rituals.Failed || !strings.Contains(f.Reason, "código 3") || !strings.Contains(f.Output, "erro-da-torre") {
		t.Errorf("a pre-run that fails on the Tower: %+v, %v", f, err)
	}

	// what the permissions of the character on the Tower do not allow is not run
	denied := create("negado", "rm -rf /")
	f, err = x.orc.Rituals.Run(ctx, denied.ID, by)
	if f = settled(f); err != nil || f.State != rituals.Failed || !strings.Contains(f.Reason, "permiss") {
		t.Errorf("a pre-run the permissions of the character refuse: %+v, %v", f, err)
	}
	if _, err := os.Stat(x.towerTree); err != nil {
		t.Errorf("the worktree of the Tower is gone: %v", err)
	}
	// and the Tower runs it only for the Citadel it follows, and only for a character that is there
	router := protocol.NewRouter()
	x.b.e.registerTowerCharacters(router)
	h, _ := router.Lookup(protocol.TypeRitualPreRun)
	for name, c := range map[string]struct {
		from protocol.Peer
		who  string
	}{
		"another sender":       {protocol.Peer{Role: protocol.RoleMachine, ID: "outra"}, dev.ID},
		"a character not here": {protocol.Peer{Role: protocol.RoleMachine, ID: x.a.id}, "fantasma"},
	} {
		if _, err := h.Handle(ctx, &protocol.Message{Env: protocol.Envelope{Type: protocol.TypeRitualPreRun}, Peer: c.from,
			Payload: &protocol.RitualPreRun{Character: c.who, Command: "echo oi", TimeoutSeconds: 5}}); err == nil {
			t.Errorf("%s: the Tower ran the pre-run", name)
		}
	}

	// with the Tower away nothing runs and nothing is delivered
	x.b.stop()
	x.r.waitFor("the Citadel to see the Tower gone", func() bool { return !(towerTables{x.a.e.towerClient()}).Connected(x.b.id) })
	away := create("ausente", "echo nao-roda")
	f, err = x.orc.Rituals.Run(ctx, away.ID, by)
	f = settled(f)
	if err != nil || f.State != rituals.Failed || !strings.Contains(f.Reason, "não rodou") {
		t.Errorf("a pre-run with the Tower away: %+v, %v", f, err)
	}
	if got := bodies(); len(got) != 1 {
		t.Errorf("the inbox has %d items of rituals; the failed firings delivered nothing", len(got))
	}
	var unreachable *characters.MachineUnreachableError
	if _, err := m.Run(ctx, rituals.PreRunRequest{Machine: x.b.id, Character: dev.ID, Command: "true", Timeout: time.Second}); !errors.As(err, &unreachable) {
		t.Errorf("the pre-run with the Tower away: %v", err)
	}
}
