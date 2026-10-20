package app

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/users"
)

const decreePIN = "135790"

const decreeDevYAML = `version: 1
files: {read: ["**"], write: ["**"]}
commands: {allow: ["npm test"], default: deny}
tools: {default: deny}
`

// decreeWorld is the real composition of the decrees (the standard integration,
// the orchestration of the Citadel with a character) with the hook engine of the
// machine over the rules the Citadel builds from the decrees in force, and the
// ledger of the machine for the exceptions, as `mestre run` has them.
type decreeWorld struct {
	*approvalWorld
	machine *store.DB
	ledger  *classes.Store
	engine  *Engine // the engine whose OnHookEvent the hook reports to
	queue   *decreeQueue
	logs    *warnLog
	agents  string
	admin   users.User
}

func newDecreeWorld(t *testing.T) *decreeWorld {
	t.Helper()
	w := &decreeWorld{approvalWorld: newApprovalWorld(t)}
	mdb, err := guildtpl.OpenMachine(w.ctx, filepath.Join(t.TempDir(), "maquina.db"), store.Options{Synchronous: "OFF", Clock: w.clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mdb.Close() })
	w.machine, w.agents = mdb, t.TempDir()
	w.ledger = &classes.Store{DB: mdb, Clock: w.clk, Agents: w.agents}
	w.engine = &Engine{log: slog.New(slog.DiscardHandler)}
	w.logs = &warnLog{}
	w.queue = hostDecreeUses(w.run, w.orc, w.clk, w.logs.Warn)
	if w.queue == nil {
		t.Fatal("the period has no registration of the use of the decrees")
	}
	ctx, stop := context.WithCancel(w.ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = w.queue.Loop(ctx) }()
	t.Cleanup(func() { stop(); <-done })
	ex, _ := w.run.CharacterDecrees().(classes.ExceptionLedger)
	w.engine.period.Store(&periodHooks{decrees: w.queue.Enqueue, exceptions: ex})
	if err := w.run.users.SetPIN(w.ctx, w.owner.ID, decreePIN); err != nil {
		t.Fatal(err)
	}
	w.admin = w.person("Bia", users.Administrator)
	return w
}

func (w *decreeWorld) person(name string, level users.Level) users.User {
	w.t.Helper()
	u, err := w.run.users.Create(w.ctx, w.owner, users.NewUser{Name: name, Level: level, Workspaces: []string{w.ws.Slug}})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.run.users.SetPIN(w.ctx, u.ID, decreePIN); err != nil {
		w.t.Fatal(err)
	}
	return u
}

// confirm proposes and confirms a decree, giving the PIN when it is asked.
func (w *decreeWorld) confirm(p decrees.Proposal) decrees.Decree {
	w.t.Helper()
	pend, err := w.run.decrees.Propose(w.ctx, p)
	if err != nil {
		w.t.Fatalf("propose: %v", err)
	}
	pin := ""
	if pend.PIN {
		req, err := w.run.decrees.BeginPIN(w.ctx, pend.ID, p.Author)
		if err != nil {
			w.t.Fatal(err)
		}
		if err := w.run.users.CheckPIN(w.ctx, req.ID, decreePIN); err != nil {
			w.t.Fatal(err)
		}
		pin = req.ID
	}
	d, err := w.run.decrees.Confirm(w.ctx, pend.ID, p.Author, pin)
	if err != nil {
		w.t.Fatalf("confirm: %v", err)
	}
	return d
}

func (w *decreeWorld) allow(author users.User, until time.Time, cmds ...string) decrees.Proposal {
	return decrees.Proposal{Author: author.ID, Kind: decrees.KindRule, Workspace: w.ws.Slug, Until: until,
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: w.ws.Slug}, Effect: decrees.Effect{Kind: decrees.EffectPermission, Allow: &classes.Grant{Commands: cmds}},
		Text: "permita " + strings.Join(cmds, ", "), Origin: decrees.Origin{Channel: "telegram", MessageID: 7}}
}

// Rules and Env make the world the rules source of the hook: the rules the Citadel
// builds for the character from the decrees that reach it now.
func (w *decreeWorld) Rules(ctx context.Context, id string) (*classes.Rules, error) {
	p, err := classes.ParsePermissions([]byte(decreeDevYAML))
	if err != nil {
		return nil, err
	}
	decs, err := w.run.decrees.Decrees(ctx, w.master)
	if err != nil {
		return nil, err
	}
	return classes.Build(classes.Layers{AgentID: id, Class: "dev", Permissions: p, Decrees: decs, Now: w.clk.Now()})
}

func (w *decreeWorld) Env(_ context.Context, id string) (classes.Env, error) {
	return classes.Env{AgentID: id, Worktree: w.t.TempDir()}, nil
}

func (w *decreeWorld) event(command string) providers.Event {
	return providers.Event{Version: providers.EventVersion, Provider: providers.ClaudeCode, NativeEvent: "PreToolUse", Event: providers.EventPreTool,
		AgentID: w.master.ID, Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: command}}
}

// run is a pre_tool of the shell command of the Master, as the hook of the machine
// answers it; the answer is told to the engine as it is in production.
func (w *decreeWorld) run1(command string) (ans hook.Answer) {
	w.t.Helper()
	var inProcess hook.Answer
	eng := &hook.Engine{Clock: w.clk, Rules: w, Ledger: periodLedger{e: w.engine, local: w.ledger}, Characters: w, OnEvent: func(ev providers.Event, a hook.Answer) {
		inProcess = a
		w.told(ev, a)
	}}
	raw, err := json.Marshal(w.event(command))
	if err != nil {
		w.t.Fatal(err)
	}
	resp := eng.Handle(w.ctx, &localchan.Call{Request: localchan.Request{AgentID: w.master.ID, Cmd: localchan.CmdHook, Args: []json.RawMessage{raw}},
		Caller: localchan.Caller{Identified: true, AgentID: w.master.ID}})
	if err := resp.DecodeData(&ans); err != nil {
		w.t.Fatal(err)
	}
	ans.Decree = inProcess.Decree // the decree never travels
	return ans
}

func (w *decreeWorld) uses(n int64) []decrees.Use {
	w.t.Helper()
	u, err := w.run.decrees.Uses(w.ctx, n)
	if err != nil {
		w.t.Fatal(err)
	}
	return u
}

func TestAPermanentDecreeThatFreesACommandIsRegisteredInTheDecreeAtEachUseInTheHook(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	if ans := w.run1("npm install x"); ans.Verdict == hook.VerdictAllow {
		t.Fatalf("before the decree: %+v", ans)
	}
	d := w.confirm(w.allow(w.admin, time.Time{}, "npm install *"))
	if ans := w.run1("npm install x"); ans.Verdict != hook.VerdictAllow || ans.Decree != d.Number {
		t.Fatalf("with the decree: %+v", ans)
	}
	if u := w.uses(d.Number); len(u) != 1 || u[0].Character != w.master.ID || u[0].Action != "npm install x" {
		t.Fatalf("uses = %+v; want the one the hook allowed, registered once", u)
	}
	if ans := w.run1("npm install y"); ans.Verdict != hook.VerdictAllow {
		t.Fatalf("the rule stays: %+v", ans)
	}
	if u := w.uses(d.Number); len(u) != 2 {
		t.Fatalf("uses = %+v; want one per allowed call", u)
	}
	// what the decree does not free is no use of it
	if ans := w.run1("rm -rf /tmp/x"); ans.Verdict == hook.VerdictAllow {
		t.Fatalf("outside the decree: %+v", ans)
	}
	if u := w.uses(d.Number); len(u) != 2 {
		t.Fatalf("uses = %+v after a call the decree did not free", u)
	}
}

func TestAnExceptionIsSpentOnceInTheDecreeAndTheSameEventDoesNotSpendItTwice(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	bia := w.person("Ana", users.Approver)
	d := w.confirm(decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: w.ws.Slug, Target: decrees.Target{Kind: decrees.TargetCharacter, Name: w.master.ID},
		Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "npm install y"}, Text: "aprovo npm install y"})
	ans := w.run1("npm install y")
	if ans.Verdict != hook.VerdictAllow || ans.Decree != d.Number {
		t.Fatalf("with the exception: %+v", ans)
	}
	got, err := w.run.decrees.Get(w.ctx, d.Number)
	if err != nil || got.State != decrees.Used {
		t.Fatalf("decree = %+v, %v; want used", got, err)
	}
	if u := w.uses(d.Number); len(u) != 1 {
		t.Fatalf("uses = %+v", u)
	}
	// the same answer told again (a replay of the event) is not another use
	w.told(w.event("npm install y"), ans)
	if u := w.uses(d.Number); len(u) != 1 {
		t.Fatalf("uses = %+v after the event was told twice", u)
	}
	if again := w.run1("npm install y"); again.Verdict == hook.VerdictAllow {
		t.Fatalf("a second use of the exception: %+v", again)
	}
}

func TestADecreeIsNeverRegisteredForACharacterOfAnotherWorkspaceNorOutlivesItsValidityOrRevocation(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	d := w.confirm(w.allow(w.admin, w.clk.Now().Add(time.Hour), "npm install *"))
	ans := w.run1("npm install x")
	if ans.Verdict != hook.VerdictAllow || len(w.uses(d.Number)) != 1 {
		t.Fatalf("answer %+v, uses %+v", ans, w.uses(d.Number))
	}
	// a character of another workspace never counts as a use of it, whatever number the event carries
	other := w.master
	other.Workspace = "outro"
	uses := decreeUses{reg: func(context.Context, string) (characters.Character, error) { return other, nil }, applier: w.run.decrees}
	uses.onEvent(w.event("npm install x"), ans)
	if n := len(w.uses(d.Number)); n != 1 {
		t.Fatalf("uses = %d; a character of another workspace was registered", n)
	}
	// revoked: the rules of the machine may still carry it, but no use is registered
	if err := w.run.decrees.Revoke(w.ctx, d.Number, w.admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	w.told(w.event("npm install x"), ans)
	if u := w.uses(d.Number); len(u) != 2 || !strings.HasPrefix(u[1].Action, "[depois do fim]") {
		t.Fatalf("uses = %+v after the revocation; want the late one marked", u)
	}
	// past its validity
	e := w.confirm(w.allow(w.admin, w.clk.Now().Add(time.Hour), "npm install *"))
	ans = w.run1("npm install z")
	if ans.Decree != e.Number || len(w.uses(e.Number)) != 1 {
		t.Fatalf("answer %+v, uses %+v", ans, w.uses(e.Number))
	}
	w.clk.Advance(2 * time.Hour)
	w.told(w.event("npm install z"), ans)
	if u := w.uses(e.Number); len(u) != 2 || !strings.HasPrefix(u[1].Action, "[depois do fim]") {
		t.Fatalf("uses = %+v after the validity; want the late one marked", u)
	}
	// a denial, or an event that is not a decision, registers nothing
	f := w.confirm(w.allow(w.admin, time.Time{}, "npm install *"))
	w.told(w.event("npm install z"), hook.Answer{Verdict: hook.VerdictDeny, Decree: f.Number})
	post := w.event("npm install z")
	post.Event = providers.EventPostTool
	w.told(post, hook.Answer{Verdict: hook.VerdictAllow, Decree: f.Number})
	if n := len(w.uses(f.Number)); n != 0 {
		t.Fatalf("uses = %d; nothing was freed by it", n)
	}
}

func TestAnEventOfATowerRegistersTheUseOfTheDecreeInTheCitadelOnlyForACharacterOfThatMachine(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	d := w.confirm(w.allow(w.admin, time.Time{}, "npm install *"))
	ev, ans := w.event("npm install x"), hook.Answer{Verdict: hook.VerdictAllow, Decree: d.Number}
	payload, err := agentEventPayload(ev, ans, nil)
	if err != nil {
		t.Fatal(err)
	}
	var back hook.Answer
	if err := json.Unmarshal(payload["answer"], &back); err != nil || back.Decree != 0 {
		t.Fatalf("the answer on the wire carries the decree: %+v, %v", back, err)
	}
	w.engine.deps.AgentMachine = func(context.Context, string) (string, bool) { return "torre-1", true }
	send := func(from string) error {
		_, err := w.engine.handleAgentEvent(w.ctx, &protocol.Message{Payload: &payload, Peer: protocol.Peer{ID: from}})
		return err
	}
	if err := send("torre-2"); err == nil {
		t.Fatal("the event of a Tower was taken for a character that runs on another")
	}
	if n := len(w.uses(d.Number)); n != 0 {
		t.Fatalf("uses = %d after a forged event", n)
	}
	if err := send("torre-1"); err != nil {
		t.Fatal(err)
	}
	w.queue.Wait()
	if u := w.uses(d.Number); len(u) != 1 || u[0].Action != "npm install x" {
		t.Fatalf("uses = %+v; want the use the Tower reported", u)
	}
}

func TestTheExceptionBoundToAPRIsSpentOnlyOnTheLiveTargetOfTheWorkspaceRepository(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	action := &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "gh pr merge"}
	exc := func(repo, head string) decrees.Proposal {
		ref := approvals.PRTarget(repo, w.pr, head, "main")
		return decrees.Proposal{Author: w.owner.ID, Kind: decrees.KindException, Workspace: w.ws.Slug, Target: decrees.Target{Kind: decrees.TargetCharacter, Name: w.master.ID},
			Action: action, RequestType: string(classes.RequestMerge), TargetRef: ref, Text: "aprovo o merge do PR"}
	}
	// the repository of the workspace, in the spelling the workspace has it, with the head GitHub has
	ok := w.confirm(exc("lucas/Meu_App", w.head))
	if err := w.run.decrees.Redeem(w.ctx, ok.Number, redeemReq(ok), w.master.ID, "gh pr merge"); err != nil {
		t.Fatalf("redeem of the live target: %v", err)
	}
	if err := w.run.decrees.Redeem(w.ctx, ok.Number, redeemReq(ok), w.master.ID, "gh pr merge"); !errors.Is(err, decrees.ErrEnded) {
		t.Fatalf("a second redeem: %v", err)
	}
	if u := w.uses(ok.Number); len(u) != 1 {
		t.Fatalf("uses = %+v", u)
	}
	// the head moved after the exception was made: it expires unused
	moved := w.confirm(exc("lucas/Meu_App", w.head))
	w.gh.SetPull("lucas/meu_app", w.pr, strings.Repeat("b", 40), false, "clean")
	if err := w.run.decrees.Redeem(w.ctx, moved.Number, redeemReq(moved), w.master.ID, "gh pr merge"); !errors.Is(err, decrees.ErrTargetChanged) {
		t.Fatalf("redeem after a new commit: %v", err)
	}
	if got, _ := w.run.decrees.Get(w.ctx, moved.Number); got.State != decrees.Expired || len(w.uses(moved.Number)) != 0 {
		t.Fatalf("decree = %+v; want expired with no use", got)
	}
	// a repository that is not the one of the workspace is never read nor spent
	foreign := w.confirm(exc("lucas/outro", strings.Repeat("b", 40)))
	if err := w.run.decrees.Redeem(w.ctx, foreign.Number, redeemReq(foreign), w.master.ID, "gh pr merge"); err == nil {
		t.Fatal("an exception of another repository was spent")
	}
	if got, _ := w.run.decrees.Get(w.ctx, foreign.Number); got.State != decrees.Active || len(w.uses(foreign.Number)) != 0 {
		t.Fatalf("decree = %+v, uses %+v", got, w.uses(foreign.Number))
	}
}

func TestTheEffectiveRulesArePublishedAgainWhenTheDecreesInForceChange(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	n := 0
	w.run.DecreesChanged(func(context.Context) { n++ })
	d := w.confirm(w.allow(w.admin, time.Time{}, "npm install *"))
	if n != 1 {
		t.Fatalf("published %d times after the decree was made, want 1", n)
	}
	if err := w.run.decrees.Revoke(w.ctx, d.Number, w.admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("published %d times after the revocation, want 2", n)
	}
	// a proposal that is only pending changes no rule in force
	if _, err := w.run.decrees.Propose(w.ctx, w.allow(w.admin, time.Time{}, "npm install *")); err != nil || n != 2 {
		t.Fatalf("propose: %v; published %d", err, n)
	}
}

func TestThePeriodWithoutThePeopleOfTheChainRegistersNoUseOfDecrees(t *testing.T) {
	t.Parallel()
	if hostDecreeUses(nil, nil, nil, nil) != nil {
		t.Fatal("a period with no decrees registers uses")
	}
}

// redeemReq is the request an exception bound to a target was made for.
func redeemReq(d decrees.Decree) chain.Request {
	return chain.Request{Workspace: d.Workspace, Target: d.TargetRef, Impression: d.Impression}
}

// told tells the engine an answered event and waits for the registration of the
// use, which runs off the path of the hook.
func (w *decreeWorld) told(ev providers.Event, ans hook.Answer) {
	w.engine.onHookEvent(ev, ans)
	w.queue.Wait()
}

// warnLog collects the warnings of the registration.
type warnLog struct {
	mu   sync.Mutex
	msgs []string
}

func (l *warnLog) Warn(msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.msgs = append(l.msgs, msg)
}

func (l *warnLog) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.msgs)
}

func TestAPointExceptionOfAClassIsSpentOnceForTheWholeDecreeAndLeavesTheRulesOfTheOthers(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	published := make(chan struct{}, 4)
	w.run.decrees.SetChanged(func(context.Context) { published <- struct{}{} })
	bia := w.person("Ana", users.Approver)
	d := w.confirm(decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: w.ws.Slug, Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: w.ws.Slug},
		Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "npm install y"}, Text: "aprovo npm install y"})
	<-published // the decree made
	if ans := w.run1("npm install y"); ans.Verdict != hook.VerdictAllow || ans.Decree != d.Number {
		t.Fatalf("first use: %+v", ans)
	}
	<-published // the use republishes the effective rules: the exception leaves the rules of the others
	decs, err := w.run.decrees.Decrees(w.ctx, w.master)
	if err != nil || len(decs) != 0 {
		t.Fatalf("decrees still in the rules after the use: %+v, %v", decs, err)
	}
	// another character of the workspace, whose ledger of the machine never saw it
	// (the rules were not republished there yet): the decree is the authority
	other := w.master
	other.ID = "outro-personagem"
	if ok, err := (periodLedger{e: w.engine, local: w.ledger}).Use(w.ctx, other.ID, d.Number); err != nil || ok {
		t.Fatalf("second character spent the exception: %v, %v", ok, err)
	}
	// and what an event of it says is a use that ended: refused with a warning
	before := w.logs.count()
	du := decreeUses{reg: func(context.Context, string) (characters.Character, error) { return other, nil }, applier: w.run.decrees, log: w.logs.Warn}
	du.onEvent(w.event("npm install y"), hook.Answer{Verdict: hook.VerdictAllow, Decree: d.Number})
	if w.logs.count() != before+1 {
		t.Fatalf("the second use was not logged: %v", w.logs.msgs)
	}
	if u := w.uses(d.Number); len(u) != 2 || !strings.HasPrefix(u[1].Action, "[uso repetido]") {
		t.Fatalf("uses = %+v", u)
	}
}

func TestAForgedDecreeNumberRegistersNothingForACallTheDecreeDoesNotFree(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	rule := w.confirm(w.allow(w.admin, time.Time{}, "npm install *"))
	bia := w.person("Ana", users.Approver)
	exc := w.confirm(decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: w.ws.Slug, Target: decrees.Target{Kind: decrees.TargetCharacter, Name: w.master.ID},
		Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "npm install y"}, Text: "aprovo npm install y"})
	bound := w.confirm(decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: w.ws.Slug, Target: decrees.Target{Kind: decrees.TargetCharacter, Name: w.master.ID},
		Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "gh pr merge"}, RequestType: string(classes.RequestMerge),
		TargetRef: approvals.PRTarget("lucas/Meu_App", w.pr, w.head, "main"), Text: "aprovo o merge"})
	allow := func(n int64) hook.Answer { return hook.Answer{Verdict: hook.VerdictAllow, Decree: n} }
	w.told(w.event("rm -rf /"), allow(rule.Number))     // the rule does not cover the call
	w.told(w.event("npm install z"), allow(exc.Number)) // not the action of the exception
	w.told(w.event("gh pr merge"), allow(bound.Number)) // bound to a target: never goes to the hook
	for _, n := range []int64{rule.Number, exc.Number, bound.Number} {
		if u := w.uses(n); len(u) != 0 {
			t.Fatalf("decree %d: uses = %+v; a forged number registered a use", n, u)
		}
	}
	if got, _ := w.run.decrees.Get(w.ctx, exc.Number); got.State != decrees.Active {
		t.Fatalf("the exception was spent by a forged event: %s", got.State)
	}
	if got, _ := w.run.decrees.Get(w.ctx, bound.Number); got.State != decrees.Active {
		t.Fatalf("the bound exception was spent by a forged event: %s", got.State)
	}
	// the action recorded is bounded
	long := w.event("npm install " + strings.Repeat("x", 5000))
	w.told(long, allow(rule.Number))
	if u := w.uses(rule.Number); len(u) != 1 || len([]rune(u[0].Action)) > maxEventAction+1 {
		t.Fatalf("uses = %d, action of %d runes", len(u), len([]rune(u[0].Action)))
	}
}

func TestAUseTheHookAllowedIsKeptWhenTheDecreeEndsBeforeTheEventIsDelivered(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	d := w.confirm(w.allow(w.admin, time.Time{}, "npm install *"))
	ev := w.event("npm install x")
	ev.ReceivedAt = w.clk.Now()
	w.clk.Advance(time.Minute)
	if err := w.run.decrees.Revoke(w.ctx, d.Number, w.admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	w.clk.Advance(time.Minute)
	w.told(ev, hook.Answer{Verdict: hook.VerdictAllow, Decree: d.Number}) // the event waited in an outbox
	u := w.uses(d.Number)
	if len(u) != 1 || u[0].Action != "npm install x" || !u[0].At.Equal(ev.ReceivedAt.Truncate(time.Millisecond)) {
		t.Fatalf("uses = %+v; want the use at the instant of the event, unmarked", u)
	}
	// an event that happened after the revocation is marked
	late := w.event("npm install y")
	late.ReceivedAt = w.clk.Now()
	w.told(late, hook.Answer{Verdict: hook.VerdictAllow, Decree: d.Number})
	if u := w.uses(d.Number); len(u) != 2 || !strings.HasPrefix(u[1].Action, "[depois do fim]") || w.logs.count() != 1 {
		t.Fatalf("uses = %+v, warnings %d", u, w.logs.count())
	}
}

func TestADecreeQueueThatIsFullDropsTheUseWithAWarning(t *testing.T) {
	t.Parallel()
	w := newDecreeWorld(t)
	log := &warnLog{}
	q := newDecreeQueue(decreeUses{applier: w.run.decrees, log: log.Warn, reg: w.orc.Registry.Get}) // no worker: nothing leaves the queue
	ans := hook.Answer{Verdict: hook.VerdictAllow, Decree: 1}
	for range decreeQueueSize + 3 {
		q.Enqueue(w.event("npm install x"), ans)
	}
	if log.count() != 3 {
		t.Fatalf("warnings = %d; want 3 dropped uses", log.count())
	}
}
