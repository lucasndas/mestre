package hook_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// takes is a hook that needs d to end with exit 0; cut before, it ran out
// of time.
type takes struct {
	d   time.Duration
	ran []string
}

func (h *takes) Run(ctx context.Context, c hook.ClassHook, _ providers.EventKind, _ []byte) (int, string, error) {
	h.ran = append(h.ran, c.Name)
	if err := clock.Sleep(ctx, clock.Real{}, h.d); err != nil {
		return 0, "", ctx.Err()
	}
	return 0, "", nil
}

// fixedHooks is an engine that allows and sends the hooks given.
type fixedHooks []hook.ClassHook

func (f fixedHooks) Handle(context.Context, *localchan.Call) localchan.Response {
	return localchan.OK(hook.Answer{Verdict: hook.VerdictAllow, ClassHooks: f})
}

// Part of CA-CLASSES-03: the synchronous hooks of one call share one budget,
// the provider's limit less the margin, so two blocking hooks of 20 s each
// under the 30 s of Codex are not 40 s: the second is cut at what is left,
// and the call denies before Codex gives up on it (past its limit Codex goes
// on as if allowed). An engine that sends no budget gives the largest limit
// of the hooks as the budget.
func TestTheSyncHooksOfACallShareOneBudgetBelowTheProviderLimit(t *testing.T) {
	t.Parallel()
	c := classWith(t, `
pre_tool:
  - {name: primeiro, run: "npm test", timeout: 20, blocking: true}
  - {name: segundo, run: "npm test", timeout: 20, blocking: true}
`)
	for _, tc := range []struct {
		name   string
		engine func(t *testing.T) localchan.Handler
		within time.Duration
	}{
		{"budget of the engine", func(t *testing.T) localchan.Handler { e, _ := engineWith(t, c); return e }, 28 * time.Second},
		{"no budget", func(*testing.T) localchan.Handler {
			return fixedHooks{{Name: "primeiro", Run: "x", TimeoutMS: 20000, Blocking: true}, {Name: "segundo", Run: "y", TimeoutMS: 20000, Blocking: true}}
		}, 20 * time.Second},
	} {
		synctest.Test(t, func(t *testing.T) {
			r := &takes{d: 15 * time.Second}
			begin := clock.Real{}.Now()
			res := call{provider: providers.Codex, event: "PreToolUse", stdin: payload(providers.Codex, "npm test"),
				dial: engine(t, tc.engine(t)), files: newFiles(t), machine: machine(), runner: r}.run(t.Context())
			took := clock.Since(clock.Real{}, begin)
			v, why := decision(t, providers.Codex, res)
			if v != "deny" || !has(why, "segundo") || !has(why, "bloqueante") {
				t.Fatalf("%s: %s (%q); want the second hook to deny, cut by the budget", tc.name, v, why)
			}
			if took > tc.within+time.Second || len(r.ran) != 2 {
				t.Fatalf("%s: the call took %v and ran %v; want both within %v (Codex gives up at 30 s)", tc.name, took, r.ran, tc.within)
			}
		})
	}
}
