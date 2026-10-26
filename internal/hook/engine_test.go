package hook_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/testkit/localrig"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// memRules is the effective rules of the machine, in memory.
type memRules struct {
	mu sync.Mutex
	r  map[string]*classes.Rules
}

func (m *memRules) Rules(_ context.Context, id string) (*classes.Rules, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.r[id]; ok {
		return r, nil
	}
	return nil, errors.New("no rules")
}

type memLedger struct {
	mu   sync.Mutex
	used map[int64]bool
}

func (l *memLedger) Use(_ context.Context, _ string, id int64) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.used == nil {
		l.used = map[int64]bool{}
	}
	if l.used[id] {
		return false, nil
	}
	l.used[id] = true
	return true, nil
}

// characters knows alice, who works in worktree().
type characters struct{}

func (characters) Env(_ context.Context, id string) (classes.Env, error) {
	if id != agent {
		return classes.Env{}, errors.New("unknown")
	}
	return classes.Env{AgentID: agent, Worktree: worktree(), Layout: machine().Layout}, nil
}

type approvals struct {
	mu   sync.Mutex
	reqs []hook.ApprovalRequest
	err  error
}

func (a *approvals) Open(_ context.Context, r hook.ApprovalRequest) (int64, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.err != nil {
		return 0, a.err
	}
	a.reqs = append(a.reqs, r)
	return int64(len(a.reqs)), nil
}

type states struct {
	mu      sync.Mutex
	waiting map[string]int64
}

func (s *states) WaitingApproval(_ context.Context, id string, n int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.waiting == nil {
		s.waiting = map[string]int64{}
	}
	s.waiting[id] = n
	return nil
}

func newEngine(t *testing.T, yaml string) *hook.Engine {
	t.Helper()
	return &hook.Engine{
		Clock: clock.Real{}, Rules: &memRules{r: map[string]*classes.Rules{agent: rules(t, yaml)}}, Ledger: &memLedger{},
		Characters: characters{},
	}
}

func claudeShell(command string) []byte { return payload(providers.ClaudeCode, command) }

func askEngine(t *testing.T, e *hook.Engine, command string) (string, string) {
	t.Helper()
	r := call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: claudeShell(command), dial: engine(t, e),
		files: newFiles(t), machine: machine()}.run(t.Context())
	return decision(t, providers.ClaudeCode, r)
}

// A command in ask opens the approval request with its risk, the character
// waits for the approval and the agent gets the denial with the number;
// without the approvals, or when the request cannot be opened, it is denied
// as well.
func TestAskOpensTheApprovalRequest(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newEngine(t, qaYAML)
		ap, st := &approvals{}, &states{}
		e.Approvals, e.States = ap, st
		v, why := askEngine(t, e, "npm install x")
		if v != "deny" || why != classes.AskReason(1, classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "npm install x"}) {
			t.Fatalf("%s (%q)", v, why)
		}
		if len(ap.reqs) != 1 || ap.reqs[0].Action.Command != "npm install x" || ap.reqs[0].AgentID != agent || st.waiting[agent] != 1 {
			t.Fatalf("requests %+v, waiting %v", ap.reqs, st.waiting)
		}
		if r := ap.reqs[0].Risk; r.Risk != classes.RiskLow || r.TableVersion != classes.RiskTableVersion || r.Rule == "" {
			t.Fatalf("the request has risk %+v; want baixo, with the table version and the row", r)
		}

		ap.err = errors.New("the approvals are down")
		if v, why := askEngine(t, e, "npm install x"); v != "deny" || !has(why, "não foi possível abrir") {
			t.Fatalf("%s (%q)", v, why)
		}
		e.Approvals = nil
		if v, why := askEngine(t, e, "npm install x"); v != "deny" || !has(why, "pede aprovação") {
			t.Fatalf("%s (%q)", v, why)
		}
	})
}

// The engine decides only for the character the server verified: a process
// in another character's terminal is refused by the channel (the dispatcher
// blocks), and a call whose event names another character is denied.
func TestTheEngineDecidesForTheVerifiedCharacterOnly(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newEngine(t, `{version: 1, commands: {allow: ["*"], default: allow}, tools: {default: allow}}`)
		r := localrig.New(clock.Real{}, localrig.Options{Config: localchan.Config{Local: map[string]localchan.Handler{localchan.CmdHook: e}}})
		r.Start(t)
		r.Character("t2", "bob")
		r.Process("bob-proc", "t2")
		res := call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: claudeShell("npm test"),
			dial: func(string) localchan.Dialer { return r.Dialer("bob-proc") }, files: newFiles(t), machine: machine()}.run(t.Context())
		if v, why := decision(t, providers.ClaudeCode, res); v != "deny" || !has(why, "not_identified") {
			t.Fatalf("%s (%q)", v, why)
		}
	})
	e := newEngine(t, `{version: 1, commands: {allow: ["*"], default: allow}, tools: {default: allow}}`)
	ev, _ := json.Marshal(providers.Event{Version: 1, Provider: providers.ClaudeCode, NativeEvent: "PreToolUse", Event: providers.EventPreTool,
		AgentID: agent, Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm test"}})
	resp := e.Handle(t.Context(), &localchan.Call{
		Request: localchan.Request{AgentID: agent, Cmd: localchan.CmdHook, Args: []json.RawMessage{ev}},
		Caller:  localchan.Caller{Identified: true, AgentID: "bob"},
	})
	var ans hook.Answer
	if err := resp.DecodeData(&ans); err != nil || ans.Verdict != hook.VerdictDeny {
		t.Fatalf("answer %+v (%v)", ans, err)
	}
	// An unknown character (no terminal here) is denied too.
	if v, why := askEngine(t, &hook.Engine{Rules: e.Rules, Characters: nil}, "npm test"); v != "deny" || !has(why, "desconhecido") {
		t.Fatalf("%s (%q)", v, why)
	}
}

type classHooks []hook.ClassHook

func (c classHooks) Sync(context.Context, providers.Event) ([]hook.ClassHook, error) { return c, nil }

// runner runs the class hooks of one call, one after the other.
type runner struct {
	exit int
	err  error
	ran  []string
}

func (r *runner) Run(_ context.Context, h hook.ClassHook, ev providers.EventKind, stdin []byte) (int, string, error) {
	var got providers.Event
	if json.Unmarshal(stdin, &got) != nil || got.Event != ev {
		return 0, "", errors.New("the hook did not get the event")
	}
	r.ran = append(r.ran, h.Name)
	return r.exit, "checar.sh recusou", r.err
}

// The synchronous pre_tool hooks of the class run only after the pipeline
// allows, and can only deny: exit 2 denies with its stderr, a failure denies
// only a blocking hook, and a tool the pipeline denied never runs them.
func TestClassHooksCanOnlyDeny(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		run      runner
		blocking bool
		command  string
		want     string
		ran      bool
	}{
		{"exit 0", runner{}, false, "npm test", "allow", true},
		{"exit 2", runner{exit: 2}, false, "npm test", "deny", true},
		{"failure", runner{err: errors.New("timeout")}, false, "npm test", "allow", true},
		{"blocking failure", runner{err: errors.New("timeout")}, true, "npm test", "deny", true},
		{"denied tool", runner{}, true, "curl x", "deny", false},
	} {
		synctest.Test(t, func(t *testing.T) {
			e := newEngine(t, qaYAML)
			e.ClassHooks = classHooks{{Name: "checar", Run: "./checar.sh", Dir: worktree(), TimeoutMS: 5000, Blocking: c.blocking}}
			res := call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: claudeShell(c.command), dial: engine(t, e),
				files: newFiles(t), machine: machine(), runner: &c.run}.run(t.Context())
			v, why := decision(t, providers.ClaudeCode, res)
			if v != c.want || (len(c.run.ran) == 1) != c.ran {
				t.Fatalf("%s: %s (%q), ran %v", c.name, v, why, c.run.ran)
			}
			if c.name == "exit 2" && !has(why, "checar.sh recusou") {
				t.Fatalf("the denial does not carry the hook's reason: %q", why)
			}
		})
	}
}

type contexts string

func (c contexts) Context(context.Context, providers.Event) (string, error) { return string(c), nil }

// turn_start gets the context of the turn from the engine.
func TestTurnStartGetsTheContext(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newEngine(t, qaYAML)
		e.Contexts = contexts("decreto 12 em vigor")
		stdin, _ := json.Marshal(map[string]any{"session_id": "s1", "prompt_id": "p2", "cwd": worktree(), "prompt": "oi"})
		res := call{provider: providers.ClaudeCode, event: "UserPromptSubmit", stdin: stdin, dial: engine(t, e),
			files: newFiles(t), machine: machine()}.run(t.Context())
		var m map[string]map[string]any
		if err := json.Unmarshal([]byte(res.stdout), &m); err != nil || m["hookSpecificOutput"]["additionalContext"] != "decreto 12 em vigor" {
			t.Fatalf("exit %d, %q (%v)", res.code, res.stdout, err)
		}
	})
}

// The engine keeps env.json of the character next to policy.json, and the
// dispatcher with the engine down reads both: a write inside the worktree is
// judged by the snapshot's files.deny, not as a write outside it.
func TestTheDegradedModeUsesTheEnvironmentTheEngineKept(t *testing.T) {
	t.Parallel()
	agents := t.TempDir()
	r := rules(t, qaYAML)
	synctest.Test(t, func(t *testing.T) {
		e := newEngine(t, qaYAML)
		e.Agents = agents
		if v, _ := askEngine(t, e, "npm test"); v != "allow" {
			t.Fatalf("engine: %s", v)
		}
	})
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(agents, agent, classes.SnapshotName), b, 0o640); err != nil {
		t.Fatal(err)
	}
	mc := machine()
	mc.Agents, mc.Queue = agents, t.TempDir()
	edit := func(rel string) (string, string) {
		stdin, _ := json.Marshal(map[string]any{"session_id": "s1", "prompt_id": "p1", "cwd": worktree(), "tool_name": "Write",
			"tool_input": map[string]any{"file_path": filepath.Join(worktree(), rel), "content": "x"}})
		res := call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: stdin, dial: down, files: hook.OSFiles{}, machine: mc}.run(t.Context())
		return decision(t, providers.ClaudeCode, res)
	}
	if v, why := edit(filepath.Join("src", "a.ts")); v != "deny" || !has(why, `files.deny "src/**"`) {
		t.Fatalf("src/a.ts: %s (%q)", v, why)
	}
	if v, why := edit(filepath.Join("tests", "a.ts")); v != "allow" {
		t.Fatalf("tests/a.ts: %s (%q)", v, why)
	}
}
