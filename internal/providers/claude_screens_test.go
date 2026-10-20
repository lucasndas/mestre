package providers_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/terminal/ptyd"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The screens of the table of docs/provedores/claude-code.md and what the
// adapter makes of each.
func TestClaudeScreensAreRecognized(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	const permission = "Bash command\n\n   rm -rf build\n\nDo you want to proceed?\n❯ 1. Yes\n  2. Yes, and don't ask again for Bash in this folder\n  3. No, and tell Claude what to do differently (esc)"
	tests := []struct {
		name, screen, id string
		action           providers.Action
	}{
		{"trust", "Do you trust the files in this folder?\n❯ 1. Yes, proceed\n  2. No, exit", "trust", providers.ActionRetrust},
		{"trust, new wording", "Quick safety check: Is this a project you created or one you trust?", "trust", providers.ActionRetrust},
		{"login", "Select login method:\n❯ 1. Claude account with subscription", "login", providers.ActionAlert},
		{"theme (first use)", "Let's get started.\nChoose the text style that looks best with your terminal", "first-use", providers.ActionAlert},
		{"external import", "Allow external CLAUDE.md file imports?", "external-import", providers.ActionEscalate},
		{"mcp servers", "New MCP server found in .mcp.json: github", "mcp-servers", providers.ActionEscalate},
		{"api key", "Detected a custom API key in your environment\nDo you want to use this API key?", "api-key", providers.ActionEscalate},
		{"usage limit", "You've hit your session limit · resets 3pm (UTC)\n\nWhat do you want to do?\n❯ 1. Stop and wait for limit to reset\n  2. Upgrade your plan", "usage-limit", providers.ActionAnswer},
		{"usage limit, another limit", "Usage limit reached\n❯ 1. Wait for the limit to reset\n  2. Upgrade", "usage-limit", providers.ActionAnswer},
		{"stale wait", "Waiting for the limit to reset (3pm)\nPress Enter to continue", "quota-stale", providers.ActionAnswer},
		{"permission", permission, "permission", providers.ActionAnswer},
		{"permission to edit", "Do you want to make this edit to a.txt?\n❯ 1. Yes\n  2. Yes, allow all edits during this session\n  3. No", "permission", providers.ActionAnswer},
	}
	for _, tt := range tests {
		got := claude.Screens(providers.ScreenInput{Text: tt.screen, Stable: true})
		if got.Known == nil || got.Known.ID != tt.id || got.Known.Action != tt.action {
			t.Errorf("%s: known = %+v, want %s (%s)", tt.name, got.Known, tt.id, tt.action)
		}
	}
	// A screen whose first option is not the safe one is not recognized: it
	// escalates to the Mestre da mesa (the "Se falhar" of the document).
	for name, screen := range map[string]string{
		"limit menu that starts with the upgrade":    "You've hit your session limit\n❯ 1. Upgrade your plan\n  2. Stop and wait for limit to reset",
		"limit message without a menu":               "You've hit your session limit · resets 3pm\n> ",
		"permission whose first option widens it":    "Do you want to proceed?\n❯ 1. Yes, and don't ask again for Bash\n  2. No",
		"permission with the cursor on the last":     "Do you want to proceed?\n  1. Yes\n❯ 2. No",
		"the text of a past limit above the prompt":  "You've hit your session limit · resets 3pm\n\nStopped. Waiting for the limit to reset…\n\n> ",
		"the answer in the conversation, not a menu": "I asked: Do you want to proceed?\nThey said 1. Yes\n> ",
		"a conversation about the trust screen":      "> explain why Claude asks whether you trust this folder",
	} {
		if got := claude.Screens(providers.ScreenInput{Text: screen, Stable: true}); got.Known != nil {
			t.Errorf("%s: recognized as %s", name, got.Known.ID)
		}
	}
}

// A screen that is a known one is acted on by what the document says: the
// permission request by the decision registered, none meaning deny; the
// usage limit by waiting; the rest escalated.
func TestSupervisorAnswersTheClaudeScreens(t *testing.T) {
	t.Parallel()
	const (
		permission = "Do you want to proceed?\n❯ 1. Yes\n  2. Yes, and don't ask again\n  3. No"
		limit      = "You've hit your session limit · resets 3pm\n❯ 1. Stop and wait for limit to reset\n  2. Upgrade your plan"
		stale      = "Waiting for the limit to reset\nPress Enter to continue"
	)
	tests := []struct {
		name     string
		screen   string
		decision map[string]string
		typed    []string
		blocked  []string
		alert    string
	}{
		{"permission allowed once", permission, map[string]string{"permission": "allow"}, []string{"\r"}, nil, ""},
		{"permission denied", permission, map[string]string{"permission": "deny"}, []string{"\x1b"}, nil, ""},
		{"permission without a decision is denied", permission, nil, []string{"\x1b"}, nil, ""},
		{"permission with a decision nobody knows is escalated", permission, map[string]string{"permission": "always"}, nil, []string{"a1/input"}, ""},
		{"usage limit waits and blocks for the quota", limit, nil, []string{"\r"}, []string{"a1/quota"}, ""},
		{"stale wait without the confirmation of the engine", stale, nil, nil, []string{"a1/input"}, ""},
		{"stale wait after the engine confirmed the reset", stale, map[string]string{"quota-stale": "resume"}, []string{"\r"}, nil, ""},
		{"external import", "Allow external CLAUDE.md file imports?", nil, nil, []string{"a1/input"}, ""},
		{"mcp servers", "New MCP server found in .mcp.json", nil, nil, []string{"a1/input"}, ""},
		{"api key", "Detected a custom API key in your environment", nil, nil, []string{"a1/input"}, ""},
		{"first use", "Choose the text style that looks best with your terminal", nil, nil, []string{"a1/input"}, "ALL-11"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(at)
			w := &world{decision: tt.decision}
			sup := supervisor(clk, providers.NewClaudeCode(providers.Options{}), w, nil)
			defer sup.Close()
			sup.Start(t.Context())
			sup.Screen(tt.screen, true)
			sup.Screen(tt.screen, true) // the same screen again: acted on once
			clk.Advance(time.Minute)
			s := w.snapshot()
			if !slices.Equal(s.typed, tt.typed) {
				t.Errorf("typed = %q, want %q", s.typed, tt.typed)
			}
			if !slices.Equal(s.blocked, tt.blocked) {
				t.Errorf("blocked = %v, want %v", s.blocked, tt.blocked)
			}
			if tt.alert != "" && (len(s.alerts) != 1 || s.alerts[0].Item != tt.alert) {
				t.Errorf("alerts = %+v, want %s", s.alerts, tt.alert)
			}
		})
	}
}

type recoverer struct {
	mu    sync.Mutex
	calls []string
	err   error
}

func (r *recoverer) RecoverTrust(_ context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, agentID)
	return r.err
}

func (r *recoverer) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// The trust screen is never answered: the character restarts once, with the
// trust of the worktree written again; if the screen comes back, it
// escalates (blocked, input, ALL-12).
func TestTrustScreenRestartsOnceThenEscalates(t *testing.T) {
	t.Parallel()
	const trust = "Do you trust the files in this folder?\n❯ 1. Yes, proceed\n  2. No, exit"
	build := func(r *recoverer, retried bool) (*providers.Supervisor, *world, *clock.Fake) {
		clk := clock.NewFake(at)
		w := &world{}
		cfg := providers.SupervisorConfig{
			Clock: clk, Adapter: providers.NewClaudeCode(providers.Options{}), AgentID: "a1", TableID: "3-login",
			Keyboard: w, Characters: w, TableMaster: w, Checklist: w, Inbox: w, Decisions: w, TrustRetried: retried,
		}
		if r != nil {
			cfg.Trust = r
		}
		return providers.NewSupervisor(cfg), w, clk
	}

	t.Run("once, then escalates", func(t *testing.T) {
		t.Parallel()
		r := &recoverer{}
		sup, w, clk := build(r, false)
		defer sup.Close()
		sup.Start(t.Context())
		sup.Screen(trust, true)
		if s := w.snapshot(); r.count() != 1 || len(s.typed) != 0 || len(s.blocked) != 0 || len(s.alerts) != 0 {
			t.Fatalf("first time: recoveries %d, typed %q, blocked %v, alerts %v; want one restart and nothing typed", r.count(), s.typed, s.blocked, s.alerts)
		}
		// The new process shows the same screen.
		sup.Screen("Claude Code\nloading", true)
		sup.Screen(trust, true)
		s := w.snapshot()
		if r.count() != 1 || len(s.typed) != 0 || !slices.Equal(s.blocked, []string{"a1/input"}) || len(s.alerts) != 1 || s.alerts[0].Item != "ALL-12" ||
			len(s.notices) != 1 || s.notices[0].Kind != providers.NoticeBlockedInput {
			t.Errorf("second time: recoveries %d, typed %q, blocked %v, alerts %+v, notices %+v", r.count(), s.typed, s.blocked, s.alerts, s.notices)
		}
		clk.Advance(time.Minute)
		if got := w.snapshot(); len(got.blocked) != 1 {
			t.Errorf("blocked again by the 30 s watch: %v", got.blocked)
		}
	})
	t.Run("the restart starts the 30 s watch of the new process", func(t *testing.T) {
		t.Parallel()
		r := &recoverer{}
		sup, w, clk := build(r, false)
		defer sup.Close()
		sup.Start(t.Context())
		sup.Screen(trust, true)
		clk.Advance(29 * time.Second)
		if s := w.snapshot(); len(s.blocked) != 0 {
			t.Fatalf("blocked at 29 s: %v", s.blocked)
		}
		clk.Advance(time.Second)
		if s := w.snapshot(); !slices.Equal(s.blocked, []string{"a1/input"}) {
			t.Errorf("a new process that shows nothing known for 30 s: blocked = %v", s.blocked)
		}
	})
	t.Run("already restarted", func(t *testing.T) {
		t.Parallel()
		r := &recoverer{}
		sup, w, _ := build(r, true)
		defer sup.Close()
		sup.Start(t.Context())
		sup.Screen(trust, true)
		if s := w.snapshot(); r.count() != 0 || !slices.Equal(s.blocked, []string{"a1/input"}) || len(s.alerts) != 1 || s.alerts[0].Item != "ALL-12" {
			t.Errorf("recoveries %d, blocked %v, alerts %+v", r.count(), s.blocked, s.alerts)
		}
	})
	t.Run("the restart fails", func(t *testing.T) {
		t.Parallel()
		r := &recoverer{err: errors.New("cannot write .claude.json")}
		sup, w, _ := build(r, false)
		defer sup.Close()
		sup.Start(t.Context())
		sup.Screen(trust, true)
		if s := w.snapshot(); !slices.Equal(s.blocked, []string{"a1/input"}) || len(s.alerts) != 1 {
			t.Errorf("blocked %v, alerts %+v", s.blocked, s.alerts)
		}
	})
	t.Run("nobody restarts", func(t *testing.T) {
		t.Parallel()
		sup, w, _ := build(nil, false)
		defer sup.Close()
		sup.Start(t.Context())
		sup.Screen(trust, true)
		if s := w.snapshot(); !slices.Equal(s.blocked, []string{"a1/input"}) || len(s.alerts) != 1 {
			t.Errorf("blocked %v, alerts %+v", s.blocked, s.alerts)
		}
	})
}

// fakeRig is the folders of a fake Claude Code in a trusted worktree.
type fakeRig struct{ root, work, home, cfg string }

func newFakeRig(t *testing.T, files map[string]string) fakeRig {
	t.Helper()
	root := t.TempDir()
	r := fakeRig{root: root, work: filepath.Join(root, "work"), home: filepath.Join(root, "home"), cfg: filepath.Join(root, "cfg")}
	all := map[string]string{
		"work/.git/HEAD":        "ref: refs/heads/main\n",
		"home/.keep":            "",
		"cfg/.credentials.json": "{}",
		"cfg/.claude.json":      fmt.Sprintf(`{"projects": {%q: {"hasTrustDialogAccepted": true}}}`, filepath.ToSlash(r.work)),
	}
	for k, v := range files {
		all[k] = v
	}
	testkit.WriteFiles(t, root, all)
	return r
}

// start runs the fake as Claude Code in the rig, with the arguments and
// the doubles given, on a terminal with the emulator of the product.
func (r fakeRig) start(t *testing.T, clk clock.Clock, l *fakeagent.MemLauncher, args []string, env ...string) *agentTerminal {
	t.Helper()
	l.Clock, l.Reporter = clk, t
	p, err := l.Start(t.Context(), terminal.Spec{
		Path: "claude", Dir: r.work, Cols: 200, Rows: 50, Args: args,
		Env: append([]string{"HOME=" + r.home, "MESTRE_AGENT_ID=a1", "CLAUDE_CONFIG_DIR=" + r.cfg}, env...),
	})
	if err != nil {
		t.Fatal(err)
	}
	at := &agentTerminal{proc: p, em: ptyd.NewVTEmulator(200, 50, 100, nil), done: make(chan struct{})}
	go func() {
		defer close(at.done)
		buf := make([]byte, 4096)
		for {
			n, err := p.PTY().Read(buf)
			at.em.Write(buf[:n])
			if err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		_ = p.Kill()
		<-at.done
		at.em.Close()
	})
	return at
}

func (a *agentTerminal) output() string { return a.proc.(*fakeagent.MemProcess).Output() }

// commands answers the commands of the fake: "fail" exits 1, the rest 0.
type commands struct{}

func (commands) RunCommand(_ context.Context, command, _ string, _ []string) (string, int, error) {
	if strings.Contains(command, "fail") {
		return "failed\n", 1, nil
	}
	return "done\n", 0, nil
}

// The fake agent shows the permission screen of a tool nobody decided, the
// supervision answers it, and the tool runs only on "allow".
func TestPermissionScreenOfTheFakeAgent(t *testing.T) {
	t.Parallel()
	const script = "prompts:\n  - match: go\n    steps:\n      - tool: {name: Bash, input: {command: \"rm -rf build\"}, ask: true}\n"
	for _, tt := range []struct {
		name     string
		decision map[string]string
		ran      bool
	}{
		{"allow", map[string]string{"permission": "allow"}, true},
		{"deny", map[string]string{"permission": "deny"}, false},
		{"no decision", nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				clk := clock.NewFake(at)
				rig := newFakeRig(t, map[string]string{"script.yaml": script})
				agent := rig.start(t, clk, &fakeagent.MemLauncher{Commands: commands{}}, nil, "FAKEAGENT_SCRIPT="+filepath.Join(rig.root, "script.yaml"))
				w := &world{decision: tt.decision}
				sup := supervisor(clk, providers.NewClaudeCode(providers.Options{}), w, agent)
				defer sup.Close()
				sup.Start(t.Context())
				synctest.Wait()
				sup.Screen(agent.em.Screen(), true)
				if err := sup.Deliver(t.Context(), "d1", "go"); err != nil {
					t.Fatal(err)
				}
				synctest.Wait()
				screen := agent.em.Screen()
				if !strings.Contains(screen, "Do you want to proceed?") || !strings.Contains(screen, "❯ 1. Yes") {
					t.Fatalf("the fake agent did not ask:\n%s", screen)
				}
				sup.Screen(screen, true)
				synctest.Wait()
				clk.Advance(time.Second) // an Esc alone is told by the quiet after it
				synctest.Wait()
				out := agent.output()
				if ran := strings.Contains(out, "Tool Bash ran"); ran != tt.ran || strings.Contains(out, "Tool Bash denied") == tt.ran {
					t.Errorf("tool ran = %v, want %v:\n%s", ran, tt.ran, out)
				}
			})
		})
	}
}

// The fake agent hits the usage limit: the supervision recognizes the menu,
// answers the option that waits, and the character is blocked for the quota.
func TestUsageLimitMenuOfTheFakeAgent(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.NewFake(at)
		rig := newFakeRig(t, map[string]string{"script.yaml": "prompts:\n  - match: go\n    steps:\n      - limit: session\n"})
		agent := rig.start(t, clk, &fakeagent.MemLauncher{}, nil, "FAKEAGENT_SCRIPT="+filepath.Join(rig.root, "script.yaml"))
		w := &world{}
		sup := supervisor(clk, providers.NewClaudeCode(providers.Options{}), w, agent)
		defer sup.Close()
		sup.Start(t.Context())
		synctest.Wait()
		sup.Screen(agent.em.Screen(), true)
		if err := sup.Deliver(t.Context(), "d1", "go"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		screen := agent.em.Screen()
		if !strings.Contains(screen, "What do you want to do?") {
			t.Fatalf("the fake agent did not show the menu:\n%s", screen)
		}
		sup.Screen(screen, true)
		synctest.Wait()
		if s := w.snapshot(); !slices.Equal(s.blocked, []string{"a1/quota"}) {
			t.Errorf("blocked = %v, want a1/quota", s.blocked)
		}
		if out := agent.output(); !strings.Contains(out, "Stopped. Waiting for the limit to reset") {
			t.Errorf("the fake agent did not take the wait option:\n%s", out)
		}
	})
}
