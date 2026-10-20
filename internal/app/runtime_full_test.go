//go:build full

package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/presets"
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
		"HOME="+p.home, "CLAUDE_CONFIG_DIR="+p.cfg, "FAKEAGENT_SCRIPT="+script, "GORACE=atexit_sleep_ms=0")
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
			Snapshot: presets.Snapshot{Sheet: presets.Sheet{Name: c.Sheet, Agent: "claude-code", Class: "Dev"}, Base: presets.Base{Name: "claude-code"}},
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
	sinks := periodSinks{&a.e.orc}
	if err := sinks.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToCreator}, Workspace: "meu-app",
		Character: c.ID, Name: c.Name, Creator: characters.Creator{Kind: characters.CreatedByCharacter, ID: c.ID}, Text: "aviso de teste"}); err != nil {
		t.Fatalf("the notice of the machine: %v", err)
	}
	if n, err := orc.Inbox.Pending(ctx, c.ID); err != nil || n == 0 {
		t.Errorf("the inbox of %s has %d items (err %v), want the notice", c.ID, n, err)
	}
}
