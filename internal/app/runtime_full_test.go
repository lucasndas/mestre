//go:build full

package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/pkg/clock"
)

const runtimePTYVersion = "1.0.0"

// runtimeBuilds are the binaries of the test, built once per test binary.
var runtimeBuilds struct {
	once                sync.Once
	err                 error
	dir, pty, mestre    string
	fakeagent, ptyFolde string
}

// buildRuntimeBinaries builds the mestre-pty (in the folder layout the
// installation brings), the mestre binary the hook files call and the fake
// agent, once per test binary.
func buildRuntimeBinaries(t *testing.T) {
	t.Helper()
	b := &runtimeBuilds
	b.once.Do(func() {
		dir, err := os.MkdirTemp("", "mrtbin")
		if err != nil {
			b.err = err
			return
		}
		exe := ""
		if runtime.GOOS == "windows" {
			exe = ".exe"
		}
		b.dir, b.ptyFolde = dir, filepath.Join(dir, "pty")
		b.pty = filepath.Join(b.ptyFolde, runtimePTYVersion, "mestre-pty"+exe)
		b.mestre, b.fakeagent = filepath.Join(dir, "mestre"+exe), filepath.Join(dir, "claude"+exe)
		for _, a := range [][]string{
			{"-o", b.pty, "-ldflags", "-X main.version=" + runtimePTYVersion, "github.com/lucasndas/mestre/cmd/mestre-pty"},
			{"-o", b.mestre, "github.com/lucasndas/mestre/cmd/mestre"},
			{"-o", b.fakeagent, "github.com/lucasndas/mestre/cmd/fakeagent"},
		} {
			if out, err := exec.Command("go", append([]string{"build"}, a...)...).CombinedOutput(); err != nil {
				b.err = fmt.Errorf("go build %v: %v\n%s", a, err, out)
				return
			}
		}
	})
	if b.err != nil {
		t.Fatal(b.err)
	}
}

// runtimePrep is the session zero of the character in this test: the hook
// files and the trust of the worktree, and the fake agent as the command.
type runtimePrep struct {
	cfg, home, sock string
}

func (p runtimePrep) Prepare(_ context.Context, req sessionzero.CharacterRequest) (sessionzero.CharacterPlan, error) {
	ad := providers.NewClaudeCode(providers.Options{})
	files, err := ad.HookFiles(providers.HookFilesRequest{OS: runtime.GOOS, Root: req.Dir, Binary: runtimeBuilds.mestre})
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
	if _, err := ad.PreTrust(providers.PreTrustRequest{OS: runtime.GOOS, Profile: p.cfg, Worktree: req.Dir, RepoRoot: req.Dir}); err != nil {
		return sessionzero.CharacterPlan{}, err
	}
	script := filepath.Join(p.home, "quick.yaml")
	env := append(os.Environ(),
		localchan.EnvAgentID+"="+req.ID, localchan.EnvEndpoint+"="+localchan.EndpointURL(p.sock), localchan.EnvCLI+"="+runtimeBuilds.mestre,
		"HOME="+p.home, "CLAUDE_CONFIG_DIR="+p.cfg, "FAKEAGENT_SCRIPT="+script, "GORACE=atexit_sleep_ms=0",
		"PATH="+filepath.Dir(runtimeBuilds.mestre)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_AUTHOR_NAME=Dev", "GIT_AUTHOR_EMAIL=dev@example.test", "GIT_COMMITTER_NAME=Dev", "GIT_COMMITTER_EMAIL=dev@example.test")
	plan := sessionzero.CharacterPlan{ID: req.ID, Provider: providers.ClaudeCode, Tmp: filepath.Join(p.home, "tmp", req.ID)}
	plan.Launch = providers.Launch{Path: runtimeBuilds.fakeagent, Env: env, Dir: req.Dir, SessionID: req.NewSessionID}
	return plan, nil
}

func (runtimePrep) End(context.Context, sessionzero.EndRequest) error { return nil }
func (runtimePrep) Contexts() hook.Contexts                           { return nilContexts{} }
func (runtimePrep) TakeUpdate(string) string                          { return "" }

type nilContexts struct{}

func (nilContexts) Context(context.Context, providers.Event) (string, error) { return "", nil }

// Part of CA-CIDADELA-16, the half of this machine: `mestre run` with
// `terminais` in the configuration builds the Runtime of the machine, which
// starts a character in a terminal of the mestre-pty (the fake agent), reports
// each transition to the registry of the Citadel of the period and gives the
// notices to its inbox. The rest of the criterion (the Tower, over the
// connection) is the next part of the ligação de produção.
func TestMestreRunStartsACharacterInATerminalAndReportsToTheCitadel(t *testing.T) {
	t.Parallel()
	buildRuntimeBinaries(t)
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
	a := r.node("a")
	a.paths.CLISocket = filepath.Join(root, "a", "cli.sock")
	a.cfg.Terminais = &Terminals{Dir: runtimeBuilds.ptyFolde, Version: runtimePTYVersion}
	home := filepath.Join(root, "home")
	cfgDir := filepath.Join(home, "cfg")
	for rel, content := range map[string]string{
		"cfg/.credentials.json": "{}",
		"quick.yaml":            "prompts:\n  - match: \".*\"\n    steps:\n      - say: \"feito\"\n",
		"work/.git/HEAD":        "ref: refs/heads/main\n",
	} {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a.setup = func(e *Engine) {
		e.deps.HostOrchestration = true
		e.deps.Spawner = &terminal.DirectSpawner{Suite: "full", System: terminal.NewSystem()}
		e.deps.CharacterPrep = runtimePrep{cfg: cfgDir, home: home, sock: a.paths.CLISocket}
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return a.acting() && a.e.orc.Load() != nil })

	orc := a.e.orc.Load()
	ctx := t.Context()
	if err := a.e.withGuildDB(func(db *store.DB) error {
		_, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES ('meu-app', 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	c := characters.Character{
		ID: "c1", Name: "Dev-1", Kind: characters.KindCharacter, Role: characters.RoleCharacter, Workspace: "meu-app", Machine: a.id,
		Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", Dir: filepath.Join(home, "work"), State: characters.Starting, Reason: "start",
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Ephemeral,
	}
	if err := orc.Registry.Insert(ctx, c); err != nil {
		t.Fatal(err)
	}
	res, err := a.e.rt.rt.Start(ctx, characters.StartCommand{
		Character: c.ID, Terminal: "t-c1", Kind: characters.KindCharacter,
		Request: sessionzero.CharacterRequest{
			ID: c.ID, Name: c.Name, Role: c.Role, Workspace: c.Workspace, Machine: a.id, Dir: c.Dir,
			Snapshot: presets.Snapshot{Sheet: presets.Sheet{Name: c.Sheet, Agent: "claude-code", Class: "Dev"}, Base: presets.Base{Name: "claude-code", Ready: presets.Ready{Mode: presets.ReadyHook}}},
		},
	})
	if err != nil {
		t.Fatalf("the Runtime of the machine did not start the character: %v", err)
	}
	if res.Terminal != "t-c1" {
		t.Errorf("terminal = %q", res.Terminal)
	}
	// the registry of the Citadel hears the machine: the transitions of the
	// character reached it through the sinks of the period, up to its first idle
	r.waitFor("the registry of the Citadel to hear the character idle", func() bool {
		got, err := orc.Registry.Get(ctx, c.ID)
		return err == nil && got.State == characters.Idle
	})
	got, err := orc.Registry.Get(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.TerminalID != "t-c1" || got.PTY.PID == 0 {
		t.Errorf("the registry has the terminal %q and the mestre-pty %+v", got.TerminalID, got.PTY)
	}

	// a character the registry has alive and this machine does not know (the
	// engine restarted and found its terminal gone, while no Citadel was there
	// to hear the report) is interrupted when the Citadel reconciles
	ghost := c
	ghost.ID, ghost.Name, ghost.State, ghost.Reason, ghost.TerminalID = "c2", "Dev-2", characters.Busy, "turn", "t-c2"
	if err := orc.Registry.Insert(ctx, ghost); err != nil {
		t.Fatal(err)
	}
	a.e.reconcileLocal(ctx, orc)
	if g, err := orc.Registry.Get(ctx, ghost.ID); err != nil || g.State != characters.Interrupted {
		t.Errorf("the character the machine does not have is %v (err %v), want interrupted", g.State, err)
	}
	if g, err := orc.Registry.Get(ctx, c.ID); err != nil || g.State != characters.Idle {
		t.Errorf("the character the machine has is %v (err %v), want idle", g.State, err)
	}

	// a notice of the machine reaches the inbox of the Citadel of the period
	sk := sinks{a.e}
	if err := sk.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToCreator}, Workspace: "meu-app",
		Character: c.ID, Name: c.Name, Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: c.ID}, Text: "aviso de teste"}); err != nil {
		t.Fatalf("the notice of the machine: %v", err)
	}
	if n, err := orc.Inbox.Pending(ctx, c.ID); err != nil || n == 0 {
		t.Errorf("the inbox of %s has %d items (err %v), want the notice", c.ID, n, err)
	}
}

// CA-CIDADELA-16: the Citadel starts a character on a Tower. The request of the
// session zero crosses the connection; the Runtime of the Tower prepares it,
// starts the fake agent in a terminal of its mestre-pty and reports every
// transition to the registry of the Citadel; the Citadel types into it, reads
// its screen and hears the notices of the Tower for the master of the table,
// and refuses what a Tower says about a character that runs elsewhere.
func TestTheCitadelStartsACharacterOnATowerAndHearsItBack(t *testing.T) {
	t.Parallel()
	buildRuntimeBinaries(t)
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
	a := r.node("a")
	b := r.node("b", a)
	a.paths.CLISocket = filepath.Join(root, "a", "cli.sock")
	b.paths.CLISocket = filepath.Join(root, "b", "cli.sock")
	b.cfg.Terminais = &Terminals{Dir: runtimeBuilds.ptyFolde, Version: runtimePTYVersion}
	home := filepath.Join(root, "home")
	for rel, content := range map[string]string{
		"cfg/.credentials.json": "{}",
		"quick.yaml":            "prompts:\n  - match: \".*\"\n    steps:\n      - say: \"feito\"\n",
	} {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	work := filepath.Join(b.paths.Workspaces, "meu-app", "work")
	if err := os.MkdirAll(filepath.Join(work, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.setup = func(e *Engine) { e.deps.HostOrchestration = true }
	b.setup = func(e *Engine) {
		e.deps.Spawner = &terminal.DirectSpawner{Suite: "full", System: terminal.NewSystem()}
		e.deps.CharacterPrep = runtimePrep{cfg: filepath.Join(home, "cfg"), home: home, sock: b.paths.CLISocket}
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act and host the orchestration", func() bool { return a.acting() && a.e.orc.Load() != nil })
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	orc := a.e.orc.Load()
	ctx := t.Context()
	if err := a.e.withGuildDB(func(db *store.DB) error {
		_, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES ('meu-app', 'lucas/meu-app', 'lucas/meu-app', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	cat := &presets.Catalog{DB: orcDB(t, a), Clock: clock.Real{}}
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
	mk := func(id, name, machine string, role characters.Role, st characters.State) characters.Character {
		c := characters.Character{
			ID: id, Name: name, Kind: characters.KindCharacter, Role: role, Workspace: "meu-app", Table: "t1", Machine: machine,
			Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", Dir: work, State: st, Reason: "start",
			CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, Lifecycle: characters.Ephemeral,
		}
		if err := orc.Registry.Insert(ctx, c); err != nil {
			t.Fatal(err)
		}
		return c
	}
	master := mk("c0", "Mestre", a.id, characters.RoleTableMaster, characters.Idle)
	dev := mk("c1", "Dev-1", b.id, characters.RoleCharacter, characters.Starting)
	m := &machineRouter{towerClient: a.e.towerClient(), local: a.id, reg: characters.Registry{DB: orcDB(t, a)}}
	req := sessionzero.CharacterRequest{
		ID: dev.ID, Name: dev.Name, Role: dev.Role, Workspace: dev.Workspace, Machine: b.id, Dir: dev.Dir,
		Snapshot: presets.Snapshot{Sheet: presets.Sheet{Name: dev.Sheet, Agent: "claude-code", Class: "Dev"}, Base: presets.Base{Name: "claude-code", Ready: presets.Ready{Mode: presets.ReadyHook}}},
	}
	// the Tower refuses a request that is for another machine
	if _, err := m.Start(ctx, b.id, characters.StartCommand{Character: dev.ID, Terminal: "t-c1", Kind: characters.KindCharacter, Request: sessionzero.CharacterRequest{ID: dev.ID, Machine: a.id}}); err == nil {
		t.Fatal("the Tower started a character from a request for another machine")
	}
	// nor a worktree outside the workspaces of the Tower
	outside := req
	outside.Dir = filepath.Join(home, "work")
	if _, err := m.Start(ctx, b.id, characters.StartCommand{Character: dev.ID, Terminal: "t-c1", Kind: characters.KindCharacter, Request: outside}); err == nil {
		t.Fatal("the Tower started a character in a worktree outside its workspaces")
	}
	res, err := m.Start(ctx, b.id, characters.StartCommand{Character: dev.ID, Terminal: "t-c1", Kind: characters.KindCharacter, Request: req})
	if err != nil {
		t.Fatalf("the Citadel did not start the character on the Tower: %v", err)
	}
	if res.Terminal != "t-c1" {
		t.Errorf("terminal = %q", res.Terminal)
	}
	r.waitFor("the registry of the Citadel to hear the Tower's character idle", func() bool {
		got, err := orc.Registry.Get(ctx, dev.ID)
		return err == nil && got.State == characters.Idle
	})
	if got, _ := orc.Registry.Get(ctx, dev.ID); got.TerminalID != "t-c1" || got.PTY.PID == 0 {
		t.Errorf("the registry has the terminal %q and the mestre-pty %+v", got.TerminalID, got.PTY)
	}

	// the Citadel reaches the terminal of the Tower
	sc, err := m.ScreenLines(ctx, b.id, dev.ID, 0)
	if err != nil || sc.Text == "" {
		t.Errorf("the screen of the Tower's terminal: %q, %v", sc.Text, err)
	}
	if err := m.Input(ctx, b.id, dev.ID, []byte("x")); err != nil {
		t.Errorf("raw input into the Tower's terminal: %v", err)
	}
	// the result of a delivery comes in the ack, from inside a handler too, and
	// an item given again is not typed again
	for i := 0; i < 2; i++ {
		if res, err := m.Deliver(ctx, b.id, protocol.InboxDeliver{TerminalID: "t-c1", ItemID: "m1", Text: "olá", Header: "[de Motor]"}); err != nil || res.ItemID != "m1" || res.Status != protocol.InboxDelivered {
			t.Errorf("the delivery %d to the Tower: %+v, %v", i, res, err)
		}
	}

	// the notice of the Tower reaches the master of the table, as the registry says
	sk := sinks{b.e}
	if err := sk.Notify(ctx, characters.Notice{Kind: characters.NoticeStuck, To: characters.Recipient{Kind: characters.ToTableMaster}, Character: dev.ID, Name: "outro nome", Text: "Dev-1 travou"}); err != nil {
		t.Fatalf("the notice of the Tower: %v", err)
	}
	r.waitFor("the master of the table to hear the Tower", func() bool {
		n, err := orc.Inbox.Pending(ctx, master.ID)
		return err == nil && n > 0
	})
	// the item is the one the registry makes it: for the master, in the table of
	// the character, naming Dev-1 (the registry's name) and not the decoy
	items, err := orc.Inbox.Store().All(ctx, master.ID)
	if err != nil || len(items) != 1 {
		t.Fatalf("the inbox of the master has %d items (err %v), want the notice", len(items), err)
	}
	if it := items[0]; it.Target != master.ID || it.Refs.Table != "t1" || it.Kind != inbox.KindSystem ||
		!strings.Contains(it.Text, "Dev-1") || strings.Contains(it.Text+it.Sender, "outro nome") {
		t.Errorf("the item of the notice = %+v, want a system item for %s, of table t1, naming Dev-1 and not the decoy name", it, master.ID)
	}
	// a Tower tells nothing else to anyone else
	if err := sk.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToOwner}, Character: dev.ID, Text: "x"}); err == nil {
		t.Error("a Tower sent a notice to the Owner")
	}

	// and says nothing about a character that runs elsewhere
	link := b.e.towerLink()
	forged, err := link.Call(ctx, protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: "t0", State: "stopped", AgentID: master.ID, Seq: 1 << 60}})
	if err != nil || forged.Status != protocol.AckError || forged.Error == nil || forged.Error.Code != protocol.CodeForbidden {
		t.Errorf("a report of a character of another machine: %+v, %v", forged, err)
	}
	for _, forged := range []protocol.Outgoing{
		{Type: protocol.TypeCharacterTurn, Payload: protocol.CharacterTurn{AgentID: master.ID, LastMessage: "x"}},
		{Type: protocol.TypeCharacterNotice, Payload: protocol.CharacterNotice{Kind: string(characters.NoticeStuck), Character: master.ID, Text: "x"}},
	} {
		if res, err := link.Call(ctx, forged); err != nil || res.Status != protocol.AckError || res.Error == nil || res.Error.Code != protocol.CodeForbidden {
			t.Errorf("%s about a character of another machine: %+v, %v", forged.Type, res, err)
		}
	}
	if got, _ := orc.Registry.Get(ctx, master.ID); got.State != characters.Idle {
		t.Errorf("the master of the table is %s after the forged report", got.State)
	}
	// ... nor a notice of a kind the Tower does not tell, a report that names no
	// character, one that names a character that does not exist, nor the
	// confirmation of an item of a character of another machine
	item, err := orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: "meu-app", Target: master.ID, Kind: inbox.KindSystem, Sender: "Motor", Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		out  protocol.Outgoing
		code string
	}{
		"notice of the kind system":                  {protocol.Outgoing{Type: protocol.TypeCharacterNotice, Payload: protocol.CharacterNotice{Kind: string(characters.NoticeSystem), Character: dev.ID, Text: "x"}}, protocol.CodeForbidden},
		"report that names nobody":                   {protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: "t0", State: "idle"}}, protocol.CodeInvalidPayload},
		"report of a ghost":                          {protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: "t0", State: "idle", AgentID: "ghost"}}, protocol.CodeForbidden},
		"confirmation of an item of another machine": {protocol.Outgoing{Type: protocol.TypeInboxResult, Payload: protocol.InboxResult{ItemID: item.ID, Status: protocol.InboxConfirmed}}, protocol.CodeForbidden},
	} {
		if res, err := link.Call(ctx, c.out); err != nil || res.Status != protocol.AckError || res.Error == nil || res.Error.Code != c.code {
			t.Errorf("%s: %+v, %v; want %s", name, res, err, c.code)
		}
	}

	// the delivery that a change of state starts is made from inside the handler
	// of that very report: its answer cannot wait behind the handler (the ack
	// carries it), or the item would wait for the time out and be given twice
	report := func(state string) {
		t.Helper()
		if res, err := link.Call(ctx, protocol.Outgoing{Type: protocol.TypeTerminalStatus, Payload: protocol.TerminalStatus{TerminalID: "t-c1", State: state, AgentID: dev.ID}}); err != nil || res.Status != protocol.AckOK {
			t.Fatalf("report %s: %+v, %v", state, res, err)
		}
	}
	report("busy")
	queued, err := orc.Inbox.Enqueue(ctx, inbox.NewItem{Workspace: "meu-app", Target: dev.ID, Kind: inbox.KindSystem, Sender: "Motor", Text: "depois do turno"})
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := orc.Inbox.Pending(ctx, dev.ID); n != 1 {
		t.Fatalf("the item of a busy character is not queued: %d", n)
	}
	// the report of the change of state is acked when its handler ends, and the
	// handler asked the Tower to type the item: if the answer waited behind the
	// handler the ack would come only after the time out
	clk := clock.Real{}
	start := clk.Now()
	report("idle")
	if took := clk.Now().Sub(start); took > 8*time.Second {
		t.Errorf("the report of %s took %v: the answer of the delivery waited behind the handler", queued.ID, took)
	}
	// the other half of the criterion: the Citadel runs without `terminais` here, so a start on its
	// own machine is refused with the reason, and another machine is still the one that cannot be reached
	_, err = a.e.mach.Load().Start(ctx, a.id, characters.StartCommand{Character: dev.ID})
	if !errors.Is(err, errTerminalsNotWired) || !strings.Contains(err.Error(), "terminais") {
		t.Errorf("a start on a machine without terminals: %v; want the refusal that says they are not wired", err)
	}
}

// CA-CIDADELA-16: a Tower runs the commands of the characters only from the
// Citadel it follows: a third machine, and the Citadel of an older epoch, are
// refused whatever they ask.
func TestATowerTakesTheCommandsOfTheCharactersOnlyFromItsCitadel(t *testing.T) {
	t.Parallel()
	buildRuntimeBinaries(t)
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
	a := r.node("a")
	b := r.node("b", a)
	b.cfg.Terminais = &Terminals{Dir: runtimeBuilds.ptyFolde, Version: runtimePTYVersion}
	b.paths.CLISocket = filepath.Join(root, "b", "cli.sock")
	a.setup = func(e *Engine) { e.deps.HostOrchestration = true }
	b.setup = func(e *Engine) {
		e.deps.Spawner = &terminal.DirectSpawner{Suite: "full", System: terminal.NewSystem()}
		e.deps.CharacterPrep = runtimePrep{cfg: filepath.Join(root, "cfg"), home: root, sock: b.paths.CLISocket}
	}
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })

	router := protocol.NewRouter()
	b.e.registerTowerCharacters(router)
	b.e.registerTowerTables(router, &lateTower{})
	for _, from := range []protocol.Peer{
		{Role: protocol.RoleMachine, ID: "a-third-machine"},
		{Role: protocol.RoleMachine, ID: "a-citadel-of-an-older-epoch"},
		{Role: protocol.RoleBrowser, ID: b.e.epochs.Citadel()},
	} {
		for typ, payload := range map[string]any{
			protocol.TypeCharacterStart: &protocol.CharacterStart{Character: "c1", TerminalID: "t-c1", Kind: "character", Request: protocol.Object{}},
			protocol.TypeInboxDeliver:   &protocol.InboxDeliver{TerminalID: "t-c1", ItemID: "m1", Text: "x"},
			protocol.TypeTerminalInput:  &protocol.TerminalInput{TerminalID: "t-c1", Mode: protocol.InputRaw, Data: "x"},
			protocol.TypeTerminalScreen: &protocol.TerminalScreen{TerminalID: "t-c1"},
			protocol.TypeCharacterStop:  &protocol.CharacterStop{Character: "c1"},
			protocol.TypeTableWhere:     &protocol.TableRef{Workspace: "meu-app", Table: "3-login"},
			protocol.TypeTableRemove:    &protocol.TableRef{Workspace: "meu-app", Table: "3-login"},
			protocol.TypeTableDone:      &protocol.TableRef{Workspace: "meu-app", Table: "3-login"},
		} {
			h, ok := router.Lookup(typ)
			if !ok {
				t.Fatalf("no handler of %s", typ)
			}
			_, err := h.Handle(t.Context(), &protocol.Message{Env: protocol.Envelope{Type: typ}, Payload: payload, Peer: from})
			var pe *protocol.Error
			if !errors.As(err, &pe) || pe.Code != protocol.CodeForbidden {
				t.Errorf("%s from %+v: %v; want forbidden", typ, from, err)
			}
		}
	}
	// and the Citadel itself is heard (the character does not exist here: not forbidden)
	h, _ := router.Lookup(protocol.TypeCharacterStop)
	_, err = h.Handle(t.Context(), &protocol.Message{Env: protocol.Envelope{Type: protocol.TypeCharacterStop}, Payload: &protocol.CharacterStop{Character: "c1"}, Peer: protocol.Peer{Role: protocol.RoleMachine, ID: a.id}})
	var pe *protocol.Error
	if errors.As(err, &pe) && pe.Code == protocol.CodeForbidden {
		t.Errorf("the Citadel of the epoch was refused: %v", err)
	}
}

func orcDB(t *testing.T, n *inproc) *store.DB {
	t.Helper()
	var db *store.DB
	if err := n.e.withGuildDB(func(d *store.DB) error { db = d; return nil }); err != nil {
		t.Fatal(err)
	}
	return db
}
