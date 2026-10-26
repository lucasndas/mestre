package hook_test

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// failingReader fails the test if the dispatcher reads its stdin.
type failingReader struct{ t *testing.T }

func (r failingReader) Read([]byte) (int, error) {
	r.t.Error("the dispatcher read its stdin")
	return 0, errors.New("no")
}

// CA-HOOK-01: without MESTRE_AGENT_ID, for every provider and event (known,
// unknown, and an unknown provider), the dispatcher answers the neutral
// answer of the provider without reading a file, its stdin or its folder,
// and without opening a connection.
func TestWithoutAgentIDTheAnswerIsNeutralAndNothingIsTouched(t *testing.T) {
	t.Parallel()
	events := map[providers.ID][]string{
		providers.ClaudeCode:  {"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PermissionRequest", "Stop", "Unheard"},
		providers.Codex:       {"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "Stop", "Interrupt", "Unheard"},
		providers.Antigravity: {"PreInvocation", "PreToolUse", "PostToolUse", "PostInvocation", "Stop", "Unheard"},
		"someone-else":        {"PreToolUse"},
	}
	for p, evs := range events {
		for _, ev := range evs {
			files := newFiles(t)
			files.strict = true
			var out, errb bytes.Buffer
			code := hook.Run(t.Context(), hook.Env{
				Getenv: func(k string) string {
					if k != localchan.EnvAgentID {
						t.Errorf("%s %s: read %s", p, ev, k)
					}
					return ""
				},
				Stdin: failingReader{t}, Stdout: &out, Stderr: &errb, Clock: clock.Real{}, Files: files,
				Getwd: func() (string, error) { t.Errorf("%s %s: read the folder", p, ev); return "", nil },
				Dial: func(string) localchan.Dialer {
					t.Errorf("%s %s: opened a connection", p, ev)
					return func(context.Context) (net.Conn, error) { return nil, errors.New("no") }
				},
			}, []string{"--provider", string(p), "--event", ev})
			want := ""
			if p == providers.Antigravity {
				want = "{}\n"
				if ev == "PreToolUse" {
					want = `{"decision":"allow"}` + "\n"
				}
			}
			if code != 0 || out.String() != want || errb.Len() != 0 {
				t.Errorf("%s %s: exit %d, stdout %q, stderr %q; want 0 and %q", p, ev, code, out.String(), errb.String(), want)
			}
		}
	}
}

// CA-HOOK-02: an engine that takes the call and does not answer; a pre_tool
// the local rules do not deny is allowed when the deadline passes (virtual
// time of the bubble: 800 ms, under 1 s), and the event is in the local
// queue with the decision.
func TestASilentEngineLetsAnAllowedToolRunInTimeAndQueuesIt(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		eng := &silent{}
		files, mc := newFiles(t), machine()
		files.snapshot(mc, rules(t, qaYAML))
		begin := clock.Real{}.Now()
		r := call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: payload(providers.ClaudeCode, "npm test"),
			dial: engine(t, eng), files: files, machine: mc}.run(t.Context())
		elapsed := clock.Since(clock.Real{}, begin)
		if v, why := decision(t, providers.ClaudeCode, r); v != "allow" {
			t.Fatalf("decision %s (%s)", v, why)
		}
		if elapsed != hook.DecisionDeadline || elapsed >= time.Second {
			t.Fatalf("answered after %v, want the deadline of %v", elapsed, hook.DecisionDeadline)
		}
		if eng.count() != 1 {
			t.Fatalf("the engine got %d calls, want the one it did not answer", eng.count())
		}
		q := files.queued(mc.Queue)
		if len(q) != 1 || q[0].Kind != hook.QueuedDecision || q[0].Decision.Verdict != classes.Allow ||
			q[0].Event.Tool == nil || q[0].Event.Tool.Command != "npm test" || q[0].Event.AgentID != agent {
			t.Fatalf("queue = %+v", q)
		}
	})
}

// CA-HOOK-03: with an engine that does not answer, a pre_tool of git push
// is denied with a reason, in the three providers.
func TestASilentEngineStillDeniesGitPush(t *testing.T) {
	t.Parallel()
	for _, p := range allProviders {
		synctest.Test(t, func(t *testing.T) {
			files, mc := newFiles(t), machine()
			files.snapshot(mc, rules(t, `{version: 1, commands: {allow: ["*"], default: allow}, tools: {default: allow}}`))
			r := call{provider: p, event: "PreToolUse", stdin: payload(p, "git push origin main"),
				dial: engine(t, &silent{}), files: files, machine: mc}.run(t.Context())
			v, why := decision(t, p, r)
			if v != "deny" || !has(why, "push") {
				t.Fatalf("%s: decision %s (%q), want deny citing the push", p, v, why)
			}
		})
	}
}

// panicky is an engine connection that panics: an internal error of the
// dispatcher in the middle of a decision.
func panicky(string) localchan.Dialer {
	return func(context.Context) (net.Conn, error) { panic("simulated internal error") }
}

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("simulated read error") }

// CA-HOOK-04: a simulated internal error in pre_tool (a panic in the middle
// of the decision, a stdin that cannot be read, a payload that is not JSON)
// ends with exit 2 and the reason on stderr in Claude Code and Codex, with
// deny in Antigravity, and never with exit 1.
func TestAnInternalErrorInPreToolBlocks(t *testing.T) {
	t.Parallel()
	for _, p := range allProviders {
		for _, fault := range []string{"panic", "stdin", "json"} {
			c := call{provider: p, event: "PreToolUse", stdin: payload(p, "npm test"), files: newFiles(t), machine: machine()}
			var r run
			switch fault {
			case "panic":
				c.dial = panicky
				r = c.run(t.Context())
			case "json":
				c.stdin = []byte(`{"tool_name": "Bash", `)
				r = c.run(t.Context())
			case "stdin":
				var out, errb bytes.Buffer
				code := hook.Run(t.Context(), hook.Env{
					Getenv: func(k string) string { return map[string]string{localchan.EnvAgentID: agent}[k] },
					Stdin:  brokenReader{}, Stdout: &out, Stderr: &errb, Clock: clock.Real{}, Files: newFiles(t), Machine: machine(),
				}, []string{"--provider", string(p), "--event", "PreToolUse"})
				r = run{code, out.String(), errb.String()}
			}
			if r.code == 1 {
				t.Fatalf("%s %s: exit 1", p, fault)
			}
			if p == providers.Antigravity {
				if v, why := decision(t, p, r); v != "deny" || !has(why, "erro interno") {
					t.Fatalf("%s %s: %s (%q)", p, fault, v, why)
				}
				continue
			}
			if r.code != 2 || r.stdout != "" || !has(r.stderr, "erro interno") {
				t.Fatalf("%s %s: exit %d, stdout %q, stderr %q", p, fault, r.code, r.stdout, r.stderr)
			}
		}
	}
	// A hook file the Mestre did not write (no --event) also blocks.
	var out, errb bytes.Buffer
	code := hook.Run(t.Context(), hook.Env{
		Getenv: func(k string) string { return map[string]string{localchan.EnvAgentID: agent}[k] },
		Stdout: &out, Stderr: &errb, Clock: clock.Real{},
	}, []string{"--provider", "claude-code"})
	if code != 2 || !strings.Contains(errb.String(), "usage") {
		t.Fatalf("bad arguments: exit %d, %q", code, errb.String())
	}
}
