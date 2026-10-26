//go:build full

package hook_test

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/providers"
)

// CA-HOOK-07: with the engine side real (the local channel on a socket, the
// identification of the caller by its process, the permission pipeline over
// the machine database) and the fake agent on a real PTY, the agent fires
// 200 pre_tool; the time of each `mestre hook` process, from its start to
// its exit, measured by the helper that starts it, has a p95 under 1 s.
func TestTwoHundredPreToolCallsAreUnderASecondAtP95(t *testing.T) {
	t.Parallel()
	e := newFullEngine(t, agent)
	var decided atomic.Int32
	e.Hook.OnEvent = func(ev providers.Event, a hook.Answer) {
		if ev.Event == providers.EventPreTool && a.Verdict == hook.VerdictAllow {
			decided.Add(1)
		}
	}
	if err := e.Rules.Save(e.ctx, rules(t, `{version: 1, commands: {allow: ["npm test"], default: deny}, tools: {default: deny}}`)); err != nil {
		t.Fatal(err)
	}
	const calls = 200
	var script strings.Builder
	script.WriteString("prompts:\n  - match: \".*\"\n    steps:\n")
	for range calls {
		script.WriteString("      - tool: {name: Bash, input: {command: npm test}}\n")
	}
	script.WriteString("      - say: DONE-200\n")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	times := filepath.Join(e.root, "times")
	command := strings.Join([]string{quoteArg(self), helperTimer, quoteArg(times), quoteArg(builds.mestre),
		"hook", "--provider", "claude-code", "--event", "PreToolUse"}, " ")
	term, out, _ := e.claude("t7", agent, command, script.String())

	say(t, e.ctx, term, "go")
	out.waitFor(t, "DONE-200")

	if ran := strings.Count(out.String(), "Tool Bash ran"); ran != calls || decided.Load() != calls {
		t.Fatalf("%d tools ran, the engine allowed %d; want %d of each:\n%s", ran, decided.Load(), calls, out.String())
	}
	data, err := os.ReadFile(times)
	if err != nil {
		t.Fatal(err)
	}
	var took []time.Duration
	for _, l := range strings.Fields(string(data)) {
		n, err := strconv.ParseInt(l, 10, 64)
		if err != nil {
			t.Fatal(err)
		}
		took = append(took, time.Duration(n))
	}
	if len(took) != calls {
		t.Fatalf("%d hook processes timed, want %d", len(took), calls)
	}
	sort.Slice(took, func(i, j int) bool { return took[i] < took[j] })
	p95 := took[calls*95/100-1]
	t.Logf("mestre hook, from start to exit: p50 %v, p95 %v, max %v", took[calls/2-1], p95, took[calls-1])
	if p95 >= time.Second {
		t.Fatalf("p95 of %v, want under 1 s", p95)
	}
}

// CA-PERM-05: with npm install * in ask, the fake agent that runs
// `npm install x` gets the denial with request #1 from the real `mestre
// hook`, and the character is in the approval wait ("aguardando
// aprovação", not the native prompt's "aguardando permissão"); approved (the
// exception bound to the action arrives in the effective rules and the
// message is typed to the agent, as the inbox does), the agent repeats it
// and it runs once; the next one asks again, with request #2.
func TestAnApprovedActionPassesOnceAndTheNextAsksAgain(t *testing.T) {
	t.Parallel()
	e := newFullEngine(t, agent)
	ap, st := &approvals{}, &states{}
	e.Hook.Approvals, e.Hook.States = ap, st
	r := rules(t, qaYAML)
	if err := e.Rules.Save(e.ctx, r); err != nil {
		t.Fatal(err)
	}
	script := "prompts:\n  - match: \"instale|repita\"\n    steps:\n      - tool: {name: Bash, input: {command: npm install x}}\n"
	term, out, _ := e.claude("t5", agent, quoteArg(builds.mestre)+" hook --provider claude-code --event PreToolUse", script)
	action := classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "npm install x"}

	say(t, e.ctx, term, "instale")
	out.waitPrompt(t, "Tool Bash blocked by hook: "+classes.AskReason(1, action))
	st.mu.Lock()
	waiting := st.waiting[agent]
	st.mu.Unlock()
	if waiting != 1 {
		t.Fatalf("the character is not waiting for the approval of #1: %v", waiting)
	}

	// Approved: the Citadel resends the effective rules with the exception
	// bound to that action, and the inbox delivers the message.
	ap.mu.Lock()
	bound := ap.reqs[0].Action
	ap.mu.Unlock()
	approved := *r
	approved.GeneratedAt = r.GeneratedAt.Add(time.Minute)
	approved.Decrees = append(append([]classes.Decree(nil), r.Decrees...), classes.Decree{
		ID: 1, Kind: classes.DecreeException, Level: classes.LevelGrandMaster,
		Effect: classes.Effect{Kind: classes.EffectPermission}, Action: &bound,
	})
	if err := e.Rules.Save(e.ctx, &approved); err != nil {
		t.Fatal(err)
	}
	say(t, e.ctx, term, "pedido #1 aprovado: repita")
	out.waitFor(t, "pedido #1 aprovado: repita")
	out.waitPrompt(t, "Tool Bash ran") // the turn ended: the agent takes the next message

	say(t, e.ctx, term, "instale de novo")
	out.waitFor(t, "Tool Bash blocked by hook: "+classes.AskReason(2, action))
	if ran := strings.Count(out.String(), "Tool Bash ran"); ran != 1 {
		t.Fatalf("the approved action ran %d times:\n%s", ran, out.String())
	}
}

// The prompt is read from the screen, not from the bytes: ConPTY repaints
// the prompt with cursor positioning and no line break before it (the
// capture of the Windows runner), where a PTY passes "\r\n> " on.
func TestThePromptIsReadFromTheScreenNotFromTheBytes(t *testing.T) {
	t.Parallel()
	const start = "\x1b[?9001h\x1b[?1004h\x1b[?25l\x1b[2J\x1b[m\x1b[H\r\n\r\n\r\n> go\r\n* Working…\r\n"
	for name, tt := range map[string]struct {
		tail string
		want bool
	}{
		"PTY":                   {"Tool Bash ran \r\n\r\n> ", true},
		"ConPTY":                {"\x1b[?25lTool Bash ran \x1b[7;1H> \x1b[?25h", true},
		"the turn not over yet": {"Tool Bash ran \r\n", false},
		"the typed line only":   {"Tool Bash ran \r\n> next", false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			o := newOutbuf(t)
			_, _ = o.Write([]byte(start + tt.tail))
			o.mu.Lock()
			screen := o.em.History(-1)
			o.mu.Unlock()
			if got := promptAfter(screen, "Tool Bash ran"); got != tt.want {
				t.Fatalf("prompt seen = %v, want %v on the screen:\n%s", got, tt.want, screen)
			}
		})
	}
}
