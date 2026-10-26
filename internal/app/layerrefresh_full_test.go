//go:build full

package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/github"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/accounts"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/orchestration/tables"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const refreshPIN = "482913"

type refreshAlerts struct{}

func (refreshAlerts) Unauthorized(context.Context, int64, string) {}
func (refreshAlerts) OwnerAlert(context.Context, string)          {}
func (refreshAlerts) Tell(context.Context, string, string)        {}

// refreshBirths keeps the births in memory (BirthsDB has its own tests).
type refreshBirths struct {
	mu sync.Mutex
	m  map[string]sessionzero.Birth
}

func (b *refreshBirths) Get(_ context.Context, id string) (sessionzero.Birth, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.m[id]
	return v, ok, nil
}

func (b *refreshBirths) Put(_ context.Context, v sessionzero.Birth) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.m[v.AgentID] = v
	return nil
}

func (b *refreshBirths) SetVersion(_ context.Context, id, version string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	v := b.m[id]
	v.Version = version
	b.m[id] = v
	return nil
}

func (b *refreshBirths) Delete(_ context.Context, id string) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.m, id)
	return nil
}

type refreshAccounts struct{ dir string }

func (a refreshAccounts) Resolve(_ context.Context, p providers.ID, _ string) (accounts.Profile, error) {
	return accounts.Profile{Name: "padrao", Provider: p, Dir: a.dir, Default: true, Step: accounts.StepDone}, nil
}

type refreshIdentities struct{}

func (refreshIdentities) For(_ context.Context, w github.Who) (github.Identity, error) {
	return github.Identity{Name: "mestre-" + w.Role + "[bot]", Email: "1+mestre@users.noreply.github.com", App: w.Role}, nil
}

// CA-SZ-PERS-05
func TestADecreeCreatedForAnIdleCharacterReachesItsLayerAtTheStartOfTheNextTurn(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	root := t.TempDir()
	db, err := guildtpl.Open(ctx, filepath.Join(root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('loja', 'lucas/loja', 'lucas/loja', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	us, err := users.New(users.Options{DB: db, Clock: clk, Alerts: refreshAlerts{}, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := us.CreateOwner(ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	ana, err := us.Create(ctx, owner, users.NewUser{Name: "Ana", Level: users.Administrator, Workspaces: []string{"loja"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := us.SetPIN(ctx, ana.ID, refreshPIN); err != nil {
		t.Fatal(err)
	}
	dec, err := decrees.New(decrees.Options{DB: db, Clock: clk, PINs: us, Alerts: refreshAlerts{}})
	if err != nil {
		t.Fatal(err)
	}

	layout := workspaces.Layout{Root: filepath.Join(root, "workspaces")}
	dir := layout.Principal("loja")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	sb, err := NewSessionBuilder(SessionBuilderOptions{
		Rules: func(_ context.Context, id string) (*classes.Rules, error) {
			return &classes.Rules{Version: 1, AgentID: id, Class: "orquestrador", Files: classes.FileRules{Read: []string{"**"}},
				Commands: classes.PatternRules{Default: classes.Ask}, Tools: classes.PatternRules{Default: classes.Ask}}, nil
		},
		Notes: dec, Identities: refreshIdentities{}, Libraries: func(string) *classes.Library { return nil },
		Layout: layout, Machine: "cidadela", Tables: tables.Store{DB: db}, Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}

	agents := filepath.Join(root, "agentes")
	reg := providers.NewRegistry(providers.Options{})
	files := &classes.OSNativeFiles{}
	ch, err := sessionzero.NewCharacter(sessionzero.CharacterOptions{Providers: reg, Binary: filepath.Join(root, "bin", "mestre"), Endpoint: "unix:///run/mestre/cli.sock",
		Agents: agents, Accounts: refreshAccounts{dir: filepath.Join(root, "profile")}, Births: &refreshBirths{m: map[string]sessionzero.Birth{}}, Files: files,
		Native: &classes.NativeSync{Adapters: reg, Files: files}, Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}

	gm := characters.Character{ID: "grao-mestre", Name: "Grão-Mestre", Role: characters.RoleGrandMaster, Workspace: "loja", Machine: "cidadela", Dir: dir}
	snap := presets.Snapshot{Sheet: presets.Sheet{Name: "Grão-Mestre", Base: "claude-code", Agent: presets.AgentClaudeCode, Class: "Grão-Mestre"},
		Base: presets.Base{Name: "claude-code", Command: "claude", Ready: presets.Ready{Mode: presets.ReadyHook}, Platforms: []presets.Platform{{OS: presets.Linux}, {OS: presets.Windows}}}}
	req, err := sb.Build(ctx, characters.BuildInput{Character: gm, Snapshot: snap, Dir: dir, NewSessionID: "11111111-2222-4333-8444-555555555555"})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := ch.Prepare(ctx, req)
	if err != nil {
		t.Fatal(err)
	}

	e := &Engine{log: slog.New(slog.DiscardHandler), clk: clk}
	e.period.Store(&periodHooks{layers: func(ctx context.Context, _ string) (sessionzero.Dynamic, error) { return sb.Refresh(ctx, gm) }})
	ctxs := e.refreshingLayers(ch).Contexts()
	turn := providers.Event{Event: providers.EventTurnStart, AgentID: gm.ID}
	if got, err := ctxs.Context(ctx, turn); err != nil || got != "" {
		t.Fatalf("an update with nothing changed: %q, %v", got, err)
	}

	// The decree is made by the flow of the people: proposed, and confirmed with
	// the PIN of its author.
	pend, err := dec.Propose(ctx, decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
		Effect: decrees.Effect{Kind: decrees.EffectBehavior, Instruction: "Do not touch src/legacy."}, Text: "não mexa em src/legacy"})
	if err != nil {
		t.Fatal(err)
	}
	pinID := ""
	if pend.PIN {
		pr, err := dec.BeginPIN(ctx, pend.ID, ana.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := us.CheckPIN(ctx, pr.ID, refreshPIN); err != nil {
			t.Fatal(err)
		}
		pinID = pr.ID
	}
	d, err := dec.Confirm(ctx, pend.ID, ana.ID, pinID)
	if err != nil {
		t.Fatal(err)
	}

	// Never in the middle of a turn.
	if got, _ := ctxs.Context(ctx, providers.Event{Event: providers.EventPreTool, AgentID: gm.ID}); got != "" {
		t.Errorf("an update in the middle of a turn: %q", got)
	}
	got, err := ctxs.Context(ctx, turn)
	if err != nil || !strings.Contains(got, "Update of the Mestre layer") || !strings.Contains(got, "Do not touch src/legacy.") || !strings.Contains(got, "by Ana (Administrador)") ||
		strings.Contains(got, "## Context") || strings.Contains(got, "## Mestre rules") {
		t.Errorf("the hook of the start of the turn gave %q, %v; want only the section of the decrees, updated (decree #%d)", got, err, d.Number)
	}
	onDisk, err := os.ReadFile(plan.MestreMD)
	if err != nil || !strings.Contains(string(onDisk), "Do not touch src/legacy.") {
		t.Errorf("the MESTRE.md on disk lacks the decree: %v\n%s", err, onDisk)
	}
	if again, _ := ctxs.Context(ctx, turn); again != "" {
		t.Errorf("the update came twice: %q", again)
	}
}
