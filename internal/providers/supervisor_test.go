package providers_test

import (
	"context"
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

// world records what the supervision did to the pieces of other subjects:
// the character state, the Mestre da mesa, the checklist, the inbox and the
// keyboard.
type world struct {
	mu          sync.Mutex
	blocked     []string // agent/reason
	reduced     []string // agent/reason
	notices     []providers.Notice
	alerts      []providers.Alert
	confirmed   []string
	unconfirmed []string
	typed       []string
	decision    map[string]string // screen id -> choice
}

func (w *world) Block(_ context.Context, agent string, r providers.BlockReason, _ string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.blocked = append(w.blocked, agent+"/"+string(r))
	return nil
}

func (w *world) ReduceMonitoring(_ context.Context, agent string, r providers.ReducedReason, _ string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reduced = append(w.reduced, agent+"/"+string(r))
	return nil
}

func (w *world) Notify(_ context.Context, n providers.Notice) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.notices = append(w.notices, n)
	return nil
}

func (w *world) Alert(_ context.Context, a providers.Alert) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.alerts = append(w.alerts, a)
	return nil
}

func (w *world) Confirmed(_ context.Context, id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.confirmed = append(w.confirmed, id)
	return nil
}

func (w *world) Unconfirmed(_ context.Context, id string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.unconfirmed = append(w.unconfirmed, id)
	return nil
}

func (w *world) Input(_ context.Context, b []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.typed = append(w.typed, string(b))
	return nil
}

func (w *world) Decision(_ context.Context, _, screen string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	c, ok := w.decision[screen]
	return c, ok
}

func (w *world) snapshot() world {
	w.mu.Lock()
	defer w.mu.Unlock()
	return world{
		blocked: slices.Clone(w.blocked), reduced: slices.Clone(w.reduced), notices: slices.Clone(w.notices),
		alerts: slices.Clone(w.alerts), confirmed: slices.Clone(w.confirmed), unconfirmed: slices.Clone(w.unconfirmed),
		typed: slices.Clone(w.typed),
	}
}

func supervisor(clk clock.Clock, a providers.Adapter, w *world, kb providers.Keyboard) *providers.Supervisor {
	if kb == nil {
		kb = w
	}
	return providers.NewSupervisor(providers.SupervisorConfig{
		Clock: clk, Adapter: a, AgentID: "a1", TableID: "3-login",
		Keyboard: kb, Characters: w, TableMaster: w, Checklist: w, Inbox: w, Decisions: w,
	})
}

// agentTerminal runs the fake agent as Claude Code in a trusted folder,
// with no hook configured, and renders its screen with the terminal's
// emulator.
type agentTerminal struct {
	proc terminal.Process
	em   ptyd.Emulator
	done chan struct{}
}

func startClaude(t *testing.T, clk clock.Clock, env ...string) *agentTerminal {
	t.Helper()
	root := t.TempDir()
	work, home, cfg := filepath.Join(root, "work"), filepath.Join(root, "home"), filepath.Join(root, "cfg")
	testkit.WriteFiles(t, root, map[string]string{
		"work/.git/HEAD":        "ref: refs/heads/main\n",
		"home/.keep":            "",
		"cfg/.credentials.json": "{}",
		"cfg/.claude.json":      fmt.Sprintf(`{"projects": {%q: {"hasTrustDialogAccepted": true}}}`, work),
	})
	l := &fakeagent.MemLauncher{Clock: clk, Reporter: t}
	p, err := l.Start(t.Context(), terminal.Spec{
		Path: "claude", Dir: work, Cols: 200, Rows: 50,
		Env: append([]string{"HOME=" + home, "MESTRE_AGENT_ID=a1", "CLAUDE_CONFIG_DIR=" + cfg}, env...),
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

// Input types into the fake agent's terminal.
func (a *agentTerminal) Input(_ context.Context, b []byte) error {
	_, err := a.proc.PTY().Write(b)
	return err
}

// CA-ADAPT-07
func TestNoHookSixtySecondsAfterTheFirstPromptReducesMonitoring(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.NewFake(at)
		agent := startClaude(t, clk)
		w := &world{}
		sup := supervisor(clk, providers.NewClaudeCode(providers.Options{}), w, agent)
		defer sup.Close()
		sup.Start(t.Context())
		synctest.Wait()
		sup.Screen(agent.em.Screen(), true)

		if err := sup.Deliver(t.Context(), "d1", "hello"); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if out := agent.proc.(*fakeagent.MemProcess).Output(); !strings.Contains(out, fakeagent.BusyMarker) {
			t.Fatalf("the agent did not run the prompt:\n%s", out)
		}

		clk.Advance(59 * time.Second)
		if s := w.snapshot(); len(s.reduced) != 0 || len(s.alerts) != 0 {
			t.Fatalf("at 59 s: reduced %v, alerts %v", s.reduced, s.alerts)
		}
		clk.Advance(time.Second)
		s := w.snapshot()
		if !slices.Equal(s.reduced, []string{"a1/no_hooks"}) {
			t.Errorf("reduced monitoring = %v, want a1/no_hooks", s.reduced)
		}
		if len(s.alerts) != 1 || s.alerts[0].Item != providers.ChecklistNoHooks || s.alerts[0].AgentID != "a1" {
			t.Errorf("checklist alerts = %+v, want one %s for a1", s.alerts, providers.ChecklistNoHooks)
		}
	})
}

// A hook after the first prompt keeps the monitoring full.
func TestAHookAfterTheFirstPromptKeepsMonitoring(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(at)
	w := &world{}
	sup := supervisor(clk, providers.NewClaudeCode(providers.Options{}), w, nil)
	defer sup.Close()
	sup.Start(t.Context())
	if err := sup.Deliver(t.Context(), "d1", "hello"); err != nil {
		t.Fatal(err)
	}
	sup.Hook(providers.Event{Event: providers.EventTurnStart, TurnID: "p1"})
	clk.Advance(2 * time.Minute)
	s := w.snapshot()
	if len(s.reduced) != 0 || len(s.alerts) != 0 || len(s.notices) != 0 || !slices.Equal(s.confirmed, []string{"d1"}) {
		t.Errorf("reduced %v, alerts %v, notices %v, confirmed %v", s.reduced, s.alerts, s.notices, s.confirmed)
	}
}

// CA-ADAPT-09
func TestUnknownStartScreenForThirtySecondsBlocksForInput(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.NewFake(at)
		const odd = "Pick the colours of this terminal"
		agent := startClaude(t, clk, "FAKEAGENT_START_SCREEN="+odd)
		w := &world{}
		sup := supervisor(clk, providers.NewClaudeCode(providers.Options{}), w, agent)
		defer sup.Close()
		sup.Start(t.Context())
		synctest.Wait()
		screen := agent.em.Screen()
		if !strings.Contains(screen, odd) {
			t.Fatalf("the agent is not on the unknown screen:\n%s", screen)
		}
		sup.Screen(screen, true)

		clk.Advance(29 * time.Second)
		if s := w.snapshot(); len(s.blocked) != 0 || len(s.notices) != 0 {
			t.Fatalf("at 29 s: blocked %v, notices %v", s.blocked, s.notices)
		}
		clk.Advance(time.Second)
		s := w.snapshot()
		if !slices.Equal(s.blocked, []string{"a1/input"}) {
			t.Errorf("blocked = %v, want a1/input", s.blocked)
		}
		if len(s.notices) != 1 || s.notices[0].Kind != providers.NoticeBlockedInput || s.notices[0].AgentID != "a1" ||
			s.notices[0].TableID != "3-login" || !strings.Contains(s.notices[0].Screen, odd) {
			t.Errorf("notices to the Mestre da mesa = %+v", s.notices)
		}
	})
}

// A start that reaches the prompt, or a hook, ends the 30 s watch.
func TestStartWatchEndsAtTheReadyScreenOrAHook(t *testing.T) {
	t.Parallel()
	for _, how := range []string{"ready", "hook"} {
		clk := clock.NewFake(at)
		w := &world{}
		sup := supervisor(clk, providers.NewClaudeCode(providers.Options{}), w, nil)
		sup.Start(t.Context())
		sup.Screen("Claude Code\nloading", true)
		if how == "ready" {
			sup.Screen("Claude Code\n\n>", true)
		} else {
			sup.Hook(providers.Event{Event: providers.EventSessionStart})
		}
		clk.Advance(time.Minute)
		if s := w.snapshot(); len(s.blocked) != 0 || len(s.notices) != 0 {
			t.Errorf("%s: blocked %v, notices %v", how, s.blocked, s.notices)
		}
		sup.Close()
	}
}

// CA-ADAPT-11
func TestDeliveryWithoutTurnStartIsUnconfirmedAndNotTypedAgain(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(at)
	w := &world{}
	a := providers.NewClaudeCode(providers.Options{})
	sup := supervisor(clk, a, w, nil)
	defer sup.Close()
	sup.Start(t.Context())
	sup.Screen("Claude Code\n\n>", true)

	if err := sup.Deliver(t.Context(), "d1", "linha 1\nlinha 2"); err != nil {
		t.Fatal(err)
	}
	want, _ := a.TypeMessage("linha 1\nlinha 2")
	clk.Advance(9 * time.Second)
	if s := w.snapshot(); len(s.unconfirmed) != 0 || len(s.notices) != 0 {
		t.Fatalf("at 9 s: unconfirmed %v, notices %v", s.unconfirmed, s.notices)
	}
	clk.Advance(time.Second)
	s := w.snapshot()
	if !slices.Equal(s.unconfirmed, []string{"d1"}) {
		t.Errorf("unconfirmed deliveries = %v, want d1", s.unconfirmed)
	}
	if len(s.notices) != 1 || s.notices[0].Kind != providers.NoticeUnconfirmed || s.notices[0].DeliveryID != "d1" || s.notices[0].AgentID != "a1" {
		t.Errorf("notices to the Mestre da mesa = %+v", s.notices)
	}
	// Later: a late turn_start does not confirm it, and nothing is typed again.
	sup.Hook(providers.Event{Event: providers.EventTurnStart})
	clk.Advance(10 * time.Minute)
	s = w.snapshot()
	if !slices.Equal(s.typed, []string{string(want)}) {
		t.Errorf("typed = %q, want the delivery once: %q", s.typed, want)
	}
	if len(s.confirmed) != 0 {
		t.Errorf("confirmed = %v after it was reported unconfirmed", s.confirmed)
	}
}

// The known screens of the adapters: an answer only from a decision, the
// login as a checklist alert, the rest escalated.
func TestKnownScreens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		adapter  providers.Adapter
		screen   string
		decision map[string]string
		typed    []string
		blocked  bool
		alert    string
	}{
		{"codex trust with the decision", providers.NewCodex(providers.Options{}), "Do you trust the contents of this directory?\n❯ 1. Yes, continue",
			map[string]string{"trust": "trust"}, []string{"\r"}, false, ""},
		{"codex trust without a decision", providers.NewCodex(providers.Options{}), "Do you trust the contents of this directory?", nil, nil, true, ""},
		{"codex hook review", providers.NewCodex(providers.Options{}), "Hooks need review: 1 new hook", nil, nil, true, ""},
		{"antigravity trust with the decision", providers.NewAntigravity(providers.Options{}), "Do you trust the contents of this project?",
			map[string]string{"trust": "trust"}, []string{"\r"}, false, ""},
		{"claude login", providers.NewClaudeCode(providers.Options{}), "Select login method:", nil, nil, true, "ALL-11"},
		{"claude trust (pre-trust failed)", providers.NewClaudeCode(providers.Options{}), "Do you trust the files in this folder?", map[string]string{"trust": "trust"}, nil, true, "ALL-12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clk := clock.NewFake(at)
			w := &world{decision: tt.decision}
			sup := supervisor(clk, tt.adapter, w, nil)
			defer sup.Close()
			sup.Start(t.Context())
			sup.Screen(tt.screen, true)
			sup.Screen(tt.screen, true) // the same screen again: acted on once
			clk.Advance(time.Minute)    // a known screen is not the 30 s of an unknown one
			s := w.snapshot()
			if !slices.Equal(s.typed, tt.typed) {
				t.Errorf("typed = %q, want %q", s.typed, tt.typed)
			}
			if got := len(s.blocked) == 1 && s.blocked[0] == "a1/input"; got != tt.blocked || len(s.blocked) > 1 {
				t.Errorf("blocked = %v, want blocked %v", s.blocked, tt.blocked)
			}
			if tt.alert != "" && (len(s.alerts) != 1 || s.alerts[0].Item != tt.alert) {
				t.Errorf("alerts = %+v, want %s", s.alerts, tt.alert)
			}
		})
	}
}

// Readiness: the input prompt with the screen stable, or session_start and
// the prompt; the null adapter reads the base's pattern.
func TestScreensReadiness(t *testing.T) {
	t.Parallel()
	claude := providers.NewClaudeCode(providers.Options{})
	for _, c := range []struct {
		in   providers.ScreenInput
		want providers.State
	}{
		{providers.ScreenInput{Text: "Claude Code\n\n>", Stable: true}, providers.StateReady},
		{providers.ScreenInput{Text: "Claude Code\n\n>"}, providers.StateStarting},
		{providers.ScreenInput{Text: "Claude Code\n\n>", SessionStarted: true}, providers.StateReady},
		{providers.ScreenInput{Text: "> hi\n* Working…", Stable: true}, providers.StateBusy},
		{providers.ScreenInput{Text: "Claude Code", Stable: true}, providers.StateStarting},
	} {
		if got := claude.Screens(c.in).State; got != c.want {
			t.Errorf("claude %q stable=%v started=%v: %s, want %s", c.in.Text, c.in.Stable, c.in.SessionStarted, got, c.want)
		}
	}
	null := providers.NewNull()
	if got := null.Screens(providers.ScreenInput{Text: "user@host:~$", Stable: true}).State; got != providers.StateReady {
		t.Errorf("bash prompt: %s", got)
	}
	if got := null.Screens(providers.ScreenInput{Text: "running...", Stable: true}).State; got != providers.StateBusy {
		t.Errorf("shell without the prompt: %s", got)
	}
}

// A provider without some events of the map (Antigravity, always) gives its
// characters reduced monitoring from the start, without a checklist alert.
func TestProviderWithoutEventsOfTheMapIsAlwaysReduced(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		a    providers.Adapter
		want []string
	}{
		{providers.NewAntigravity(providers.Options{}), []string{"a1/provider_events"}},
		{providers.NewClaudeCode(providers.Options{}), nil},
		{providers.NewCodex(providers.Options{}), nil},
	} {
		w := &world{}
		sup := supervisor(clock.NewFake(at), c.a, w, nil)
		sup.Start(t.Context())
		sup.Close()
		if s := w.snapshot(); !slices.Equal(s.reduced, c.want) || len(s.alerts) != 0 {
			t.Errorf("%s: reduced %v, alerts %v; want %v", c.a.Info().ID, s.reduced, s.alerts, c.want)
		}
	}
}
