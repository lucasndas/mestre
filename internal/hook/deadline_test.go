package hook_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// slowDecider is a degraded decision that ignores its context and ends
// only when gate closes, like an analysis that never ends in time; it
// would allow.
func slowDecider(gate chan struct{}) func(context.Context, classes.Env, *classes.Rules, providers.Event) classes.Decision {
	return func(context.Context, classes.Env, *classes.Rules, providers.Event) classes.Decision {
		<-gate
		return classes.Decision{Verdict: classes.Allow}
	}
}

// release lets what the call left running end, inside the bubble.
func release(gate chan struct{}) {
	close(gate)
	synctest.Wait()
}

// blocked reads a pre_tool answer that must block: exit 2 with the reason
// on stderr (Claude Code, Codex) or an explicit deny (Antigravity).
func blocked(t *testing.T, p providers.ID, r run) string {
	t.Helper()
	if p == providers.Antigravity {
		v, why := decision(t, p, r)
		if v != "deny" {
			t.Fatalf("%s: %s (%q); want deny", p, v, why)
		}
		return why
	}
	if r.code != 2 || r.stdout != "" {
		t.Fatalf("%s: exit %d, stdout %q; want exit 2 and nothing on stdout (a block)", p, r.code, r.stdout)
	}
	return r.stderr
}

// H5 of the M3 audit: with the engine down, a degraded decision that does
// not end in time is not left to the provider's limit (past its 30 s Codex
// goes on as if allowed): the call blocks at the provider's limit less the
// margin, and the queue gets the denial.
func TestASlowDegradedDecisionBlocksBeforeTheProviderLimit(t *testing.T) {
	t.Parallel()
	for _, p := range allProviders {
		synctest.Test(t, func(t *testing.T) {
			files, mc := newFiles(t), machine()
			files.snapshot(mc, rules(t, qaYAML))
			gate := make(chan struct{})
			defer release(gate)
			begin := clock.Real{}.Now()
			r := call{provider: p, event: "PreToolUse", stdin: payload(p, "npm test"), dial: down,
				files: files, machine: mc, decide: slowDecider(gate)}.run(t.Context())
			took := clock.Since(clock.Real{}, begin)
			why := blocked(t, p, r)
			if !has(why, "prazo") {
				t.Fatalf("%s: reason %q; want the deadline named", p, why)
			}
			if want := 28 * time.Second; took > want {
				t.Fatalf("%s: the call took %v; want at most %v (30 s of the provider less the margin)", p, took, want)
			}
			q := files.queued(mc.Queue)
			if len(q) != 1 || q[0].Decision == nil || q[0].Decision.Verdict != classes.Deny {
				t.Fatalf("%s: queue %+v; want the denial", p, q)
			}
		})
	}
}

// slowReader is a stdin that gives nothing until gate closes.
type slowReader struct{ gate chan struct{} }

func (r slowReader) Read([]byte) (int, error) {
	<-r.gate
	return 0, context.DeadlineExceeded
}

// The deadline covers the whole call, not only the degraded decision: a
// call that is stuck anywhere (here, reading its stdin) blocks just past
// the deadline, still within the provider's limit.
func TestACallStuckAnywhereBlocksWithinTheProviderLimit(t *testing.T) {
	t.Parallel()
	for _, p := range allProviders {
		synctest.Test(t, func(t *testing.T) {
			gate := make(chan struct{})
			defer release(gate)
			begin := clock.Real{}.Now()
			r := call{provider: p, event: "PreToolUse", reader: slowReader{gate}, dial: down,
				files: newFiles(t), machine: machine()}.run(t.Context())
			took := clock.Since(clock.Real{}, begin)
			blocked(t, p, r)
			if limit := 30 * time.Second; took >= limit-time.Second {
				t.Fatalf("%s: the call took %v; want it to answer at least a second before the %v of the provider", p, took, limit)
			}
		})
	}
	// An informative event out of time stays neutral (Codex gives
	// SessionEnd 3 s).
	synctest.Test(t, func(t *testing.T) {
		gate := make(chan struct{})
		defer release(gate)
		begin := clock.Real{}.Now()
		r := call{provider: providers.Codex, event: "SessionEnd", reader: slowReader{gate}, dial: down,
			files: newFiles(t), machine: machine()}.run(t.Context())
		if took := clock.Since(clock.Real{}, begin); r.code != 0 || r.stdout != "" || took >= 3*time.Second {
			t.Fatalf("SessionEnd: exit %d, stdout %q after %v; want neutral within the 3 s of Codex", r.code, r.stdout, took)
		}
	})
}

// A command over MaxCommand is denied before any analysis: the engine
// never gets it, and the degraded mode never reads it.
func TestACommandOverTheAnalysisCapIsDeniedUnread(t *testing.T) {
	t.Parallel()
	for _, p := range allProviders {
		t.Run(string(p), func(t *testing.T) {
			t.Parallel()
			s := &silent{}
			files, mc := newFiles(t), machine()
			long := "echo " + strings.Repeat("a", hook.MaxCommand)
			r := call{provider: p, event: "PreToolUse", stdin: payload(p, long), dial: engine(t, s),
				files: files, machine: mc, decide: func(context.Context, classes.Env, *classes.Rules, providers.Event) classes.Decision {
					t.Error("the degraded mode read the command")
					return classes.Decision{Verdict: classes.Allow}
				}}.run(t.Context())
			v, why := decision(t, p, r)
			if v != "deny" || !has(why, "KiB") {
				t.Fatalf("%s: %s (%q); want a denial for the size", p, v, why)
			}
			if s.count() != 0 {
				t.Fatalf("%s: the engine got %d calls; want none", p, s.count())
			}
			q := files.queued(mc.Queue)
			if len(q) != 1 || q[0].Decision == nil || q[0].Decision.Verdict != classes.Deny {
				b, _ := json.Marshal(q)
				t.Fatalf("%s: queue %s; want the denial", p, b)
			}
		})
	}
	// At the cap, the command is analysed as any other.
	r := call{provider: providers.Codex, event: "PreToolUse", stdin: payload(providers.Codex, "npm test "+strings.Repeat("a", hook.MaxCommand-len("npm test "))),
		dial: down, files: newFiles(t), machine: machine()}.run(t.Context())
	if v, why := decision(t, providers.Codex, r); has(why, "KiB") {
		t.Fatalf("at the cap: %s (%q); want the command analysed", v, why)
	}
}
