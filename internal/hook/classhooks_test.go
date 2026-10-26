package hook_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// classWith is the class `qa` read from files: the permissions of qaYAML and
// the hooks.yaml given.
func classWith(t testing.TB, hooksYAML string) *classes.Class {
	t.Helper()
	files, err := classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: qa\ndescription: Testa\napp: novo\n---\nTeste.\n")},
		{Path: "permissions.yaml", Data: []byte(qaYAML)},
		{Path: "hooks/hooks.yaml", Data: []byte(hooksYAML)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ex := classes.Examine(files, classes.ExamineOptions{Scope: classes.ClassWorkspace, Dir: "qa"})
	if !ex.OK() {
		t.Fatalf("the class does not pass: %v", ex.Problems)
	}
	return ex.Class
}

// memClasses is the classes of the workspace; it counts how each was asked
// for: Use looks at the disk, Approved does not.
type memClasses struct {
	mu            sync.Mutex
	class         *classes.Class
	uses, approve int
}

func (m *memClasses) Use(context.Context, string, string) (*classes.Class, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uses++
	return m.class, nil
}

func (m *memClasses) Approved(context.Context, string, string) (*classes.Class, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.approve++
	return m.class, nil
}

func engineWith(t *testing.T, c *classes.Class) (*hook.Engine, *memClasses) {
	t.Helper()
	e := newEngine(t, qaYAML)
	mc := &memClasses{class: c}
	e.ClassHooks = &hook.ClassHookSource{Classes: mc, Rules: e.Rules, Characters: e.Characters}
	return e, mc
}

// hangs is a hook that does not end by itself: it ends when its time limit
// cancels it.
type hangs struct{ ran []string }

func (h *hangs) Run(ctx context.Context, c hook.ClassHook, _ providers.EventKind, _ []byte) (int, string, error) {
	h.ran = append(h.ran, c.Name)
	<-ctx.Done()
	return 0, "", ctx.Err()
}

// CA-CLASSES-03
func TestABlockingPreToolHookDeniesWhenItExitsWith2FailsOrRunsOutOfTime(t *testing.T) {
	t.Parallel()
	const hooksYAML = `
pre_tool:
  - name: checar
    when: {kind: [shell]}
    run: "npm test"
    mode: sync
    timeout: 5
    blocking: %s
`
	for _, c := range []struct {
		name     string
		blocking string
		runner   hook.Runner
		want     string
		reason   string // the denial carries it
		logged   string // the stderr of the call carries it (the failure is registered)
	}{
		{"blocking, exit 2", "true", &runner{exit: 2}, "deny", "checar.sh recusou", ""},
		{"blocking, failure", "true", &runner{err: errors.New("não rodou")}, "deny", "bloqueante", ""},
		{"blocking, time out", "true", &hangs{}, "deny", "bloqueante", ""},
		// Exit 0 with a descendant still holding the output (ErrWaitDelay in
		// the real runner) is a failure, not a clean exit 0.
		{"blocking, exit 0 but output held", "true", &runner{err: errors.New("the hook ended but left a process holding its output: exec: WaitDelay expired before I/O complete")}, "deny", "bloqueante", ""},
		{"not blocking, exit 2", "false", &runner{exit: 2}, "deny", "checar.sh recusou", ""},
		{"not blocking, failure", "false", &runner{err: errors.New("não rodou")}, "allow", "", "não rodou"},
		{"not blocking, time out", "false", &hangs{}, "allow", "", "deadline exceeded"},
		{"not blocking, exit 1", "false", &runner{exit: 1}, "allow", "", "checar"},
		{"blocking, exit 0", "true", &runner{}, "allow", "", ""},
	} {
		synctest.Test(t, func(t *testing.T) {
			e, _ := engineWith(t, classWith(t, strings.Replace(hooksYAML, "%s", c.blocking, 1)))
			res := call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: claudeShell("npm test"), dial: engine(t, e),
				files: newFiles(t), machine: machine(), runner: c.runner}.run(t.Context())
			v, why := decision(t, providers.ClaudeCode, res)
			if v != c.want || !has(why, c.reason) {
				t.Fatalf("%s: %s (%q); want %s with %q", c.name, v, why, c.want, c.reason)
			}
			if c.logged != "" && !has(res.stderr, c.logged) {
				t.Errorf("%s: the failure was not registered; stderr %q", c.name, res.stderr)
			}
			if c.logged == "" && v == "allow" && res.stderr != "" {
				t.Errorf("%s: stderr %q on a clean run", c.name, res.stderr)
			}
		})
	}
}

// The synchronous hooks reach the dispatcher already for this system and
// cut: the command of the system, the folder of the worktree, the time limit
// 2 s below the provider's, and the `when` of the hook. A hook whose `run`
// the permissions of the class do not allow goes as refused, and a blocking
// one then denies.
func TestTheSyncHooksReachTheDispatcherCutAndFilteredByWhen(t *testing.T) {
	t.Parallel()
	c := classWith(t, `
pre_tool:
  - {name: so-shell, when: {kind: [shell]}, run: "npm test", timeout: 600, blocking: true}
  - {name: negado, run: "curl x", timeout: 5, blocking: true}
post_tool:
  - {name: formatar, when: {kind: [edit, write], paths: ["src/**"]}, run: "npm test", run_windows: "npm.cmd test", timeout: 20}
  - {name: depois, run: "npm test", mode: async, timeout: 20}
`)
	e, mc := engineWith(t, c)
	src := e.ClassHooks.(*hook.ClassHookSource)
	m := machine()
	wt := worktree()
	ev := func(kind providers.EventKind, tool *providers.Tool) providers.Event {
		return providers.Event{Version: 1, Provider: providers.ClaudeCode, Event: kind, AgentID: agent, TableID: "1-login", Tool: tool}
	}
	_ = m

	got, err := src.Sync(t.Context(), ev(providers.EventPreTool, &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm test"}))
	if err != nil || len(got) != 2 {
		t.Fatalf("pre_tool hooks = %+v, %v", got, err)
	}
	first, second := got[0], got[1]
	if first.Name != "so-shell" || first.Run != "npm test" || first.Dir != wt || first.TimeoutMS != 28000 || !first.Blocking || first.Refused != "" {
		t.Errorf("first = %+v; want the command, the worktree and 28 s (600 s cut to 2 s below the provider's 30 s)", first)
	}
	if second.Name != "negado" || second.Refused == "" || !has(second.Refused, "curl") || !second.Blocking {
		t.Errorf("second = %+v; want it refused by the permissions of the class", second)
	}
	// The kind in `when` does not match a read.
	got, _ = src.Sync(t.Context(), ev(providers.EventPreTool, &providers.Tool{NativeName: "Read", Kind: providers.KindRead, Paths: []string{wt + "/src/a.go"}}))
	if len(got) != 1 || got[0].Name != "negado" {
		t.Errorf("for a read: %+v; want only the hook with no when", got)
	}
	// post_tool: the path must match, and the async hook is not for the dispatcher.
	post := func(rel string) []hook.ClassHook {
		out, err := src.Sync(t.Context(), ev(providers.EventPostTool, &providers.Tool{NativeName: "Edit", Kind: providers.KindEdit, Paths: []string{wt + "/" + rel}}))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if h := post("src/a/b.go"); len(h) != 1 || h[0].Name != "formatar" || h[0].TimeoutMS != 20000 {
		t.Errorf("post_tool on src = %+v", h)
	}
	if h := post("docs/a.md"); len(h) != 0 {
		t.Errorf("post_tool on docs = %+v", h)
	}
	// Only the start of a turn looks at the disk for a class changed outside
	// the Mestre.
	if mc.uses != 0 {
		t.Fatalf("Use was called %d times before any turn started", mc.uses)
	}
	if _, err := src.Sync(t.Context(), ev(providers.EventTurnStart, nil)); err != nil {
		t.Fatal(err)
	}
	if mc.uses != 1 {
		t.Errorf("Use calls at turn_start = %d; want 1", mc.uses)
	}
	run, refused, err := src.Async(t.Context(), ev(providers.EventPostTool, &providers.Tool{NativeName: "Edit", Kind: providers.KindEdit}))
	if err != nil || len(run) != 1 || run[0].Name != "depois" || run[0].Timeout != 20 || len(refused) != 0 {
		t.Errorf("async = %+v %v %v", run, refused, err)
	}
}

// The asynchronous hooks of the class run in the engine after the answer, a
// character at a time, and what they write goes where the hook asked.
type fakeExec struct {
	mu      sync.Mutex
	release chan struct{}
	started []string
	running int
	most    int
	// instant names the hooks that end at once.
	instant map[string]bool
}

func (f *fakeExec) Exec(ctx context.Context, _ string, h hook.AsyncHook, _ providers.EventKind, _ []byte) (hook.HookResult, error) {
	f.mu.Lock()
	f.started = append(f.started, h.Name)
	f.running++
	f.most = max(f.most, f.running)
	f.mu.Unlock()
	if !f.instant[h.Name] {
		select {
		case <-f.release:
		case <-ctx.Done():
		}
	}
	f.mu.Lock()
	f.running--
	f.mu.Unlock()
	if ctx.Err() != nil {
		return hook.HookResult{}, ctx.Err()
	}
	return hook.HookResult{Output: "42 passed from " + h.Name}, nil
}

type outputs struct {
	mu                 sync.Mutex
	logs, scroll, inbx []hook.AsyncResult
}

func (o *outputs) Log(_ context.Context, r hook.AsyncResult) {
	o.mu.Lock()
	o.logs = append(o.logs, r)
	o.mu.Unlock()
}
func (o *outputs) Scroll(_ context.Context, r hook.AsyncResult) error {
	o.mu.Lock()
	o.scroll = append(o.scroll, r)
	o.mu.Unlock()
	return nil
}
func (o *outputs) Agent(_ context.Context, r hook.AsyncResult) error {
	o.mu.Lock()
	o.inbx = append(o.inbx, r)
	o.mu.Unlock()
	return nil
}

// Part of CA-CLASSES-04 (the HookExec and the runner of mestre run are
// tested in internal/orchestration/characters and internal/app): the end of the turn does not wait for the
// asynchronous hook of the class, the next turn starts while it runs, the
// hooks of a character run one at a time, and the output goes to the scroll.
func TestAnAsyncTurnEndHookDoesNotHoldTheNextTurnAndItsOutputGoesToTheScroll(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e, _ := engineWith(t, classWith(t, `
turn_end:
  - {name: testes, run: "npm test", mode: async, timeout: 900, output: scroll}
  - {name: aviso, run: "npm test", mode: async, timeout: 60, output: agent}
`))
		fx := &fakeExec{release: make(chan struct{})}
		out := &outputs{}
		ar := &hook.AsyncRunner{Source: e.ClassHooks.(*hook.ClassHookSource), Exec: fx, Out: out, Clock: clock.Real{}}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ar.Start(ctx)
		e.OnEvent = ar.OnEvent
		dial := engine(t, e)

		stop, _ := json.Marshal(map[string]any{"session_id": "s1", "prompt_id": "p1", "cwd": worktree(), "hook_event_name": "Stop"})
		res := call{provider: providers.ClaudeCode, event: "Stop", stdin: stop, dial: dial, files: newFiles(t), machine: machine()}.run(ctx)
		if res.code != 0 {
			t.Fatalf("Stop: exit %d, %q", res.code, res.stderr)
		}
		synctest.Wait() // the first hook is running, blocked
		fx.mu.Lock()
		started := append([]string(nil), fx.started...)
		fx.mu.Unlock()
		if len(started) != 1 || started[0] != "testes" {
			t.Fatalf("started = %v; want the first hook only (one at a time)", started)
		}

		// The next turn starts, answered, while the hook is still running.
		prompt, _ := json.Marshal(map[string]any{"session_id": "s1", "prompt_id": "p2", "cwd": worktree(), "prompt": "outra"})
		res = call{provider: providers.ClaudeCode, event: "UserPromptSubmit", stdin: prompt, dial: dial, files: newFiles(t), machine: machine()}.run(ctx)
		if res.code != 0 {
			t.Fatalf("UserPromptSubmit: exit %d, %q", res.code, res.stderr)
		}
		out.mu.Lock()
		if len(out.scroll) != 0 {
			t.Fatal("the output was delivered before the hook ended")
		}
		out.mu.Unlock()

		close(fx.release)
		ar.Wait()
		out.mu.Lock()
		defer out.mu.Unlock()
		if len(out.scroll) != 1 || out.scroll[0].Hook != "testes" || out.scroll[0].Output != "42 passed from testes" || out.scroll[0].AgentID != agent {
			t.Errorf("scroll = %+v", out.scroll)
		}
		if len(out.inbx) != 1 || out.inbx[0].Hook != "aviso" {
			t.Errorf("inbox = %+v", out.inbx)
		}
		if fx.most != 1 {
			t.Errorf("%d hooks of the character ran at once; want one at a time", fx.most)
		}
	})
}

// A hook that runs out of time is logged and the next one still runs.
func TestAnAsyncHookThatRunsOutOfTimeIsLoggedAndTheQueueGoesOn(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e, _ := engineWith(t, classWith(t, `
turn_end:
  - {name: lento, run: "npm test", mode: async, timeout: 3, output: scroll}
  - {name: rapido, run: "npm test", mode: async, timeout: 3, output: log}
`))
		fx := &fakeExec{release: make(chan struct{}), instant: map[string]bool{"rapido": true}}
		out := &outputs{}
		ar := &hook.AsyncRunner{Source: e.ClassHooks.(*hook.ClassHookSource), Exec: fx, Out: out, Clock: clock.Real{}}
		ar.Start(t.Context())
		ar.OnEvent(providers.Event{Version: 1, Provider: providers.ClaudeCode, Event: providers.EventTurnEnd, AgentID: agent, TableID: "1-login"}, hook.Answer{})
		ar.Wait() // the first one is cut at its 3 s, the second ends at once
		out.mu.Lock()
		defer out.mu.Unlock()
		if len(out.scroll) != 0 || len(out.logs) != 2 {
			t.Fatalf("scroll %+v, logs %+v; want both logged and nothing on the scroll", out.scroll, out.logs)
		}
		if out.logs[0].Hook != "lento" || !errors.Is(out.logs[0].Err, context.DeadlineExceeded) || out.logs[1].Hook != "rapido" || out.logs[1].Err != nil {
			t.Errorf("logs = %+v", out.logs)
		}
	})
}

// A character with no class (the Grand Master, a Master of a table) has no
// hooks of a class, and that is not an error that would block its tools.
func TestACharacterWithNoClassHasNoClassHooks(t *testing.T) {
	t.Parallel()
	e, mc := engineWith(t, classWith(t, "pre_tool:\n  - {name: a, run: \"npm test\", timeout: 5, blocking: true}\n"))
	r := rules(t, qaYAML)
	r.Class = ""
	e.Rules = &memRules{r: map[string]*classes.Rules{agent: r}}
	src := &hook.ClassHookSource{Classes: mc, Rules: e.Rules, Characters: e.Characters}
	got, err := src.Sync(t.Context(), providers.Event{Version: 1, Provider: providers.ClaudeCode, Event: providers.EventPreTool, AgentID: agent,
		Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm test"}})
	if err != nil || len(got) != 0 || mc.uses+mc.approve != 0 {
		t.Errorf("hooks = %+v, %v (class asked %d times)", got, err, mc.uses+mc.approve)
	}
}

// An event the provider gives less time than the rest (Codex SessionEnd, 3 s)
// cuts the limit and the budget of its hooks to that event's, not to the
// provider's general one.
func TestTheTimeLimitOfAnEventCanBeShorterThanTheProviders(t *testing.T) {
	t.Parallel()
	c := classWith(t, "session_end:\n  - {name: fim, run: \"npm test\", timeout: 20}\nturn_end:\n  - {name: turno, run: \"npm test\", timeout: 20}\n")
	e, mc := engineWith(t, c)
	src := &hook.ClassHookSource{Classes: mc, Rules: e.Rules, Characters: e.Characters,
		EventSeconds: func(p providers.ID, ev providers.EventKind) int {
			if p == providers.Codex && ev == providers.EventSessionEnd {
				return 3
			}
			return 0
		}}
	for _, tc := range []struct {
		provider   providers.ID
		event      providers.EventKind
		ms, budget int64
	}{
		{providers.Codex, providers.EventSessionEnd, 1000, 1000}, // 3 s less the 2 s margin
		{providers.Codex, providers.EventTurnEnd, 20000, 28000},  // the rest of Codex keeps the general limit
		{providers.ClaudeCode, providers.EventSessionEnd, 20000, 28000},
	} {
		got, err := src.Sync(t.Context(), providers.Event{Version: 1, Provider: tc.provider, Event: tc.event, AgentID: agent})
		if err != nil || len(got) != 1 || got[0].TimeoutMS != tc.ms || got[0].BudgetMS != tc.budget {
			t.Errorf("%s %s: %+v, %v; want a limit of %d ms in a budget of %d ms", tc.provider, tc.event, got, err, tc.ms, tc.budget)
		}
	}
}

// A hook whose `run` can reach a script of the package, however it is spelled,
// goes as refused (a blocking one then denies) and an asynchronous one does
// not run: the copy in the worktree is the agent's to change.
func TestAHookThatCanReachAScriptOfThePackageIsRefused(t *testing.T) {
	t.Parallel()
	for _, run := range []string{"cd hooks && ./checar.sh", "true;hooks/checar.sh", "(hooks/checar.sh)", "$PWD/hooks/checar.sh",
		".//hooks/checar.sh", `h""ooks/checar.sh`, "cd h* && ./checar.sh"} {
		c := classWith(t, "pre_tool:\n  - {name: sync, run: '"+run+"', timeout: 5, blocking: true}\n"+
			"post_tool:\n  - {name: async, run: '"+run+"', mode: async, timeout: 5}\n")
		e, _ := engineWith(t, c)
		src := e.ClassHooks.(*hook.ClassHookSource)
		ev := func(kind providers.EventKind) providers.Event {
			return providers.Event{Version: 1, Provider: providers.ClaudeCode, Event: kind, AgentID: agent, TableID: "1-login",
				Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm test"}}
		}
		got, err := src.Sync(t.Context(), ev(providers.EventPreTool))
		if err != nil || len(got) != 1 || !has(got[0].Refused, "script do pacote") {
			t.Errorf("%q: sync = %+v, %v; want it refused as a script of the package", run, got, err)
		}
		runs, refused, err := src.Async(t.Context(), ev(providers.EventPostTool))
		if err != nil || len(runs) != 0 || len(refused) != 1 {
			t.Errorf("%q: async = %+v, %v, %v; want it refused and not run", run, runs, refused, err)
		}
	}
}
