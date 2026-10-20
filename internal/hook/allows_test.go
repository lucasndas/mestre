package hook_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/providers"
)

// Part of CA-RIT-11: the pre-run of a ritual is decided by the engine of the
// machine like a command of the character: what the rules allow passes, what
// they deny or would ask for is refused, with the reason.
func TestAnActionTheEngineRunsIsDecidedLikeTheCharactersOwn(t *testing.T) {
	t.Parallel()
	e := newEngine(t, qaYAML)
	ev := func(cmd string) providers.Event {
		return providers.Event{Event: providers.EventPreTool, AgentID: agent,
			Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: cmd}}
	}
	if ok, why := e.Allows(t.Context(), ev("npm test")); !ok {
		t.Errorf("a command the rules allow was refused: %s", why)
	}
	for _, cmd := range []string{"npm install x", "rm -rf /"} {
		if ok, why := e.Allows(t.Context(), ev(cmd)); ok || why == "" {
			t.Errorf("%q: allowed=%v reason=%q; want refused with a reason", cmd, ok, why)
		}
	}
	e.Rules = nil
	if ok, _ := e.Allows(t.Context(), ev("npm test")); ok {
		t.Error("a machine with no rules for the character allowed a command")
	}
}
