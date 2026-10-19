package characters_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/terminal"
)

// Part of CA-CLASSES-04: the asynchronous hook of a class runs in the terminal
// of the character, as the character, and never anywhere else.
func TestTheAsynchronousHookOfAClassRunsInTheTerminalOfTheCharacter(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{})
	c := r.make("Dev Claude", "Dev", "3-login")
	rt := r.mach[c.Machine].rt
	proc := r.mach[c.Machine].host.proc(c.ID)
	exec := rt.HookExec()

	proc.hookResult = terminal.HookResult{Exit: 2, Output: "falhou"}
	res, err := exec.Exec(t.Context(), c.ID, hook.AsyncHook{Name: "testes", Run: "npm test", Dir: "/w", Timeout: 900}, providers.EventTurnEnd, []byte("{}"))
	if err != nil || res.Exit != 2 || res.Output != "falhou" {
		t.Fatalf("result = %+v, %v", res, err)
	}
	if len(proc.hooks) != 1 || proc.hooks[0] != (hookCall{"npm test", "/w", "turn_end", "{}", 900 * time.Second}) {
		t.Fatalf("what the terminal was asked = %+v", proc.hooks)
	}

	// a hook that ran out of time is a failure, not an exit code
	proc.hookResult = terminal.HookResult{TimedOut: true}
	if _, err := exec.Exec(t.Context(), c.ID, hook.AsyncHook{Name: "lento", Run: "sleep 9", Timeout: 1}, providers.EventTurnEnd, nil); err == nil || !strings.Contains(err.Error(), "ran out") {
		t.Fatalf("a hook past its time: %v", err)
	}
	// a terminal that cannot (an old mestre-pty) fails the hook
	proc.hookErr = terminal.ErrUnsupported
	if _, err := exec.Exec(t.Context(), c.ID, hook.AsyncHook{Name: "x", Run: "ls", Timeout: 1}, providers.EventTurnEnd, nil); !errors.Is(err, terminal.ErrUnsupported) {
		t.Fatalf("an old mestre-pty: %v; want ErrUnsupported", err)
	}
	// a character this machine does not run
	if _, err := exec.Exec(t.Context(), "ghost", hook.AsyncHook{Name: "x", Run: "ls", Timeout: 1}, providers.EventTurnEnd, nil); !errors.Is(err, characters.ErrNoProcess) {
		t.Fatalf("a character that does not run here: %v; want ErrNoProcess", err)
	}
}
