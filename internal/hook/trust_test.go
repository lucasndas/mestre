package hook_test

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// tabled knows alice at the table 1-login.
type tabled struct{}

func (tabled) Env(_ context.Context, id string) (classes.Env, error) {
	env, err := characters{}.Env(context.Background(), id)
	env.TableID = "1-login"
	return env, err
}

// tableClasses records the table each class was asked for.
type tableClasses struct {
	memClasses
	mu     sync.Mutex
	tables []string
}

func (c *tableClasses) Approved(ctx context.Context, name, table string) (*classes.Class, error) {
	c.mu.Lock()
	c.tables = append(c.tables, table)
	c.mu.Unlock()
	return c.memClasses.Approved(ctx, name, table)
}

// The table of an approval request and of the class of the character is the
// registry's, not the MESTRE_MESA the process of the character wrote.
func TestTheTableIsTheRegistrysNotTheCallers(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		e := newEngine(t, qaYAML)
		e.Characters = tabled{}
		ap := &approvals{}
		e.Approvals = ap
		tc := &tableClasses{memClasses: memClasses{class: classWith(t, "pre_tool:\n  - {name: checar, run: \"npm test\", timeout: 5}\n")}}
		e.ClassHooks = &hook.ClassHookSource{Classes: tc, Rules: e.Rules, Characters: e.Characters}
		env := map[string]string{localchan.EnvAgentID: agent, localchan.EnvEndpoint: "unix:///run/mestre/principal/cli.sock", "MESTRE_MESA": "9-outra"}
		for _, command := range []string{"npm install x", "npm test"} {
			call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: claudeShell(command), env: env, dial: engine(t, e),
				files: newFiles(t), machine: machine(), runner: &runner{}}.run(t.Context())
		}
		if len(ap.reqs) != 1 || ap.reqs[0].TableID != "1-login" || ap.reqs[0].Event.TableID != "1-login" {
			t.Fatalf("approval requests %+v; want the table 1-login of the registry", ap.reqs)
		}
		if len(tc.tables) == 0 {
			t.Fatal("the class was never asked for")
		}
		for _, table := range tc.tables {
			if table != "1-login" {
				t.Fatalf("the class was asked for the table %q; want 1-login", table)
			}
		}
	})
}

// slowRules answers after d of the clock of the bubble.
type slowRules struct {
	classes.RulesSource
	d time.Duration
}

func (s slowRules) Rules(ctx context.Context, id string) (*classes.Rules, error) {
	_ = clock.Sleep(ctx, clock.Real{}, s.d)
	return s.RulesSource.Rules(context.Background(), id)
}

// Once the dispatcher no longer waits (its connection is gone, or its
// deadline passed and it decided alone), the engine opens no approval
// request and keeps no waiting state: the character was told the engine is
// away, not to wait for an approval.
func TestNoApprovalIsOpenedOnceTheDispatcherStoppedWaiting(t *testing.T) {
	t.Parallel()
	ev, _ := json.Marshal(providers.Event{Version: 1, Provider: providers.ClaudeCode, NativeEvent: "PreToolUse", Event: providers.EventPreTool,
		AgentID: agent, Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm install x"}})
	handle := func(t *testing.T, e *hook.Engine, gone <-chan struct{}) hook.Answer {
		resp := e.Handle(t.Context(), &localchan.Call{
			Request: localchan.Request{AgentID: agent, Cmd: localchan.CmdHook, Args: []json.RawMessage{ev}},
			Caller:  localchan.Caller{Identified: true, AgentID: agent}, Gone: gone,
		})
		var ans hook.Answer
		if err := resp.DecodeData(&ans); err != nil {
			t.Fatal(err)
		}
		return ans
	}
	synctest.Test(t, func(t *testing.T) {
		gone := make(chan struct{})
		close(gone)
		slow := newEngine(t, qaYAML)
		slow.Rules = slowRules{RulesSource: slow.Rules, d: hook.DecisionDeadline + time.Second}
		for name, c := range map[string]struct {
			e    *hook.Engine
			gone <-chan struct{}
		}{
			"connection gone": {newEngine(t, qaYAML), gone},
			"deadline passed": {slow, nil},
		} {
			ap, st := &approvals{}, &states{}
			c.e.Approvals, c.e.States = ap, st
			ans := handle(t, c.e, c.gone)
			synctest.Wait()
			if ans.Verdict != hook.VerdictDeny || ans.Request != 0 || len(ap.reqs) != 0 || len(st.waiting) != 0 {
				t.Errorf("%s: answer %+v, requests %+v, waiting %v; want a denial and nothing opened", name, ans, ap.reqs, st.waiting)
			}
		}
		// The control: a dispatcher that waits gets the request.
		e := newEngine(t, qaYAML)
		ap := &approvals{}
		e.Approvals = ap
		if ans := handle(t, e, make(chan struct{})); ans.Request != 1 || len(ap.reqs) != 1 {
			t.Fatalf("answer %+v, requests %+v", ans, ap.reqs)
		}
	})
}

// A hook whose `run` is a script of the package would run the copy in the
// worktree, which the agent can write, not the approved one: until the
// engine runs the approved copy (M4) it does not run, and a blocking one
// denies. The script never reaches the runner.
func TestAHookScriptOfThePackageDoesNotRunFromTheWorktree(t *testing.T) {
	t.Parallel()
	const hooksYAML = "pre_tool:\n  - {name: checar, run: \"./hooks/checar.sh --rapido\", run_windows: \"sh hooks\\\\checar.sh\", timeout: 5, blocking: true}\n"
	files, err := classes.NewFiles([]classes.File{
		{Path: "CLASS.md", Data: []byte("---\nname: qa\ndescription: Testa\napp: novo\n---\nTeste.\n")},
		{Path: "permissions.yaml", Data: []byte(`version: 1
files: {read: ["**"], write: ["**"]}
commands: {allow: ["*"], default: allow}
tools: {default: allow}
`)},
		{Path: "hooks/hooks.yaml", Data: []byte(hooksYAML)},
		{Path: "hooks/checar.sh", Data: []byte("#!/bin/sh\nexit 0\n")},
	})
	if err != nil {
		t.Fatal(err)
	}
	ex := classes.Examine(files, classes.ExamineOptions{Scope: classes.ClassWorkspace, Dir: "qa"})
	if !ex.OK() {
		t.Fatalf("the class does not pass: %v", ex.Problems)
	}
	synctest.Test(t, func(t *testing.T) {
		e, _ := engineWith(t, ex.Class)
		e.Rules = &memRules{r: map[string]*classes.Rules{agent: rules(t, `{version: 1, commands: {allow: ["*"], default: allow}, tools: {default: allow}}`)}}
		e.ClassHooks.(*hook.ClassHookSource).Rules = e.Rules
		r := &runner{}
		res := call{provider: providers.ClaudeCode, event: "PreToolUse", stdin: claudeShell("npm test"), dial: engine(t, e),
			files: newFiles(t), machine: machine(), runner: r}.run(t.Context())
		if v, why := decision(t, providers.ClaudeCode, res); v != "deny" || !has(why, "cópia aprovada") || len(r.ran) != 0 {
			t.Fatalf("%s (%q), ran %v; want a denial and the script not run", v, why, r.ran)
		}
	})
}
