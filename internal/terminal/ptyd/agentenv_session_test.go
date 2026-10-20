package ptyd_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/terminal/ptyd"
	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/internal/testkit/fakepty"
	"github.com/lucasndas/mestre/pkg/clock"
)

type hookSpy struct{ spec ptyd.HookSpec }

func (h *hookSpy) Run(_ context.Context, spec ptyd.HookSpec) (ptyd.HookResult, error) {
	h.spec = spec
	return ptyd.HookResult{}, nil
}

// CA-WIN-03 (the wiring: the agent and its hooks get the cleaned environment)
func TestOnWindowsTheSessionStartsTheAgentAndItsHooksWithTheCleanedEnvironment(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	runner := &fakepty.Runner{Clock: clk}
	spy := &hookSpy{}
	sess := ptyd.NewSession(ptyd.SessionConfig{Runner: runner, Clock: clk, OS: "windows", Hooks: spy, OnEvent: func(ptywire.Msg) {}})
	t.Cleanup(sess.Close)
	env := []string{`Path=C:\Windows\System32;D:\wt\bin;.`, "A=1"}
	if _, err := sess.Start(t.Context(), ptyd.StartSpec{Path: `C:\tools\agent.exe`, Dir: `D:\wt`, Env: env}); err != nil {
		t.Fatal(err)
	}
	want := []string{`Path=C:\Windows\System32`, "A=1", "NoDefaultCurrentDirectoryInExePath=1"}
	if got := runner.Last().Spec.Env; !slices.Equal(got, want) {
		t.Errorf("env of the agent = %q, want %q", got, want)
	}
	if _, err := sess.RunHook(t.Context(), ptyd.HookSpec{Run: "x", Timeout: ptywire.MinHookTimeout}); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(spy.spec.Env, want) {
		t.Errorf("env of the hook = %q, want %q", spy.spec.Env, want)
	}
}

// CA-WIN-03 (a bare program name is never taken from the start folder)
func TestOnWindowsABareProgramNameIsNotTakenFromTheStartFolder(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	for _, name := range []string{`.\claude.exe`, `sub\claude.exe`, "claude"} {
		sess := ptyd.NewSession(ptyd.SessionConfig{Runner: &fakepty.Runner{Clock: clk}, Clock: clk, OS: "windows", OnEvent: func(ptywire.Msg) {}})
		_, err := sess.Start(t.Context(), ptyd.StartSpec{Path: name, Dir: `D:\wt`, Env: []string{`PATH=D:\wt;.`}})
		if err == nil || !strings.Contains(err.Error(), "program") {
			t.Errorf("%q: err = %v, want a refusal (nothing in the PATH of the agent)", name, err)
		}
		sess.Close()
	}
}
