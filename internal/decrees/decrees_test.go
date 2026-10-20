package decrees_test

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/users"
)

const devYAML = `version: 1
files: {read: ["**"], write: ["**"]}
commands: {allow: ["npm test"], default: deny}
tools: {default: deny}
`

// engineFor is the hook engine over the rules the Citadel builds for the dev
// character from the decrees that reach it now.
type engineFor struct {
	w      *world
	char   characters.Character
	worktr string
}

func (e *engineFor) Rules(ctx context.Context, id string) (*classes.Rules, error) {
	p, err := classes.ParsePermissions([]byte(devYAML))
	if err != nil {
		return nil, err
	}
	decs, err := e.w.svc.Decrees(ctx, e.char)
	if err != nil {
		return nil, err
	}
	return classes.Build(classes.Layers{AgentID: id, Class: "dev", Permissions: p, Decrees: decs, Now: e.w.clk.Now()})
}

func (e *engineFor) Env(_ context.Context, id string) (classes.Env, error) {
	return classes.Env{AgentID: id, Worktree: e.worktr}, nil
}

// run is a pre_tool of the shell command, as the mestre hook sends it.
func (e *engineFor) run(t *testing.T, command string) hook.Answer {
	t.Helper()
	var inProcess hook.Answer
	eng := &hook.Engine{Clock: e.w.clk, Rules: e, Ledger: e.w.svc, Characters: e,
		OnEvent: func(ev providers.Event, ans hook.Answer) {
			inProcess = ans
			if ans.Decree != 0 {
				if err := e.w.svc.Executed(context.Background(), ans.Decree, ev.AgentID, ev.Tool.Command); err != nil {
					t.Error(err)
				}
			}
		}}
	ev, err := json.Marshal(providers.Event{Version: providers.EventVersion, Provider: providers.ClaudeCode, NativeEvent: "PreToolUse", Event: providers.EventPreTool,
		AgentID: e.char.ID, Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: command}})
	if err != nil {
		t.Fatal(err)
	}
	resp := eng.Handle(t.Context(), &localchan.Call{
		Request: localchan.Request{AgentID: e.char.ID, Cmd: localchan.CmdHook, Args: []json.RawMessage{ev}},
		Caller:  localchan.Caller{Identified: true, AgentID: e.char.ID},
	})
	var ans hook.Answer
	if err := resp.DecodeData(&ans); err != nil {
		t.Fatal(err)
	}
	// the decree never travels: it is for the engine, which sees the answer in process
	ans.Decree = inProcess.Decree
	return ans
}

func (w *world) engine(t *testing.T) *engineFor {
	return &engineFor{w: w, char: dev("d1"), worktr: t.TempDir()}
}

// Part of CA-DEC-01 (the registration of the execution is made here by the
// OnEvent of the test: the engine does not register it from the events of the
// hooks yet)
func TestAPermanentDecreeConfirmedWithPINIsAppliedAndRegistered(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	admin := w.person("Bia", users.Administrator, "loja")
	e := w.engine(t)
	if ans := e.run(t, "npm install x"); ans.Verdict != hook.VerdictDeny {
		t.Fatalf("before the decree: %+v", ans)
	}
	p := allowCmd(admin, "dev", "npm install *")
	pend, err := w.svc.Propose(w.ctx, p)
	if err != nil || !pend.PIN {
		t.Fatalf("propose: %+v, %v; a permanent rule that frees asks the PIN", pend, err)
	}
	contains(t, pend.Text, "Confirma?")
	if _, err := w.svc.Confirm(w.ctx, pend.ID, admin.ID, ""); !errors.Is(err, decrees.ErrPINRequired) {
		t.Fatalf("confirm without PIN: %v", err)
	}
	d := w.confirm(p)
	if d.Number == 0 || d.State != decrees.Active || !d.PIN || d.Author != admin.ID || d.Origin.MessageID != 7 {
		t.Fatalf("decree %+v", d)
	}
	ans := e.run(t, "npm install x")
	if ans.Verdict != hook.VerdictAllow || ans.Decree != d.Number {
		t.Fatalf("with the decree: %+v", ans)
	}
	uses, err := w.svc.Uses(w.ctx, d.Number)
	if err != nil || len(uses) != 1 || uses[0].Character != "d1" || uses[0].Action != "npm install x" {
		t.Fatalf("uses %+v, %v", uses, err)
	}
}

// CA-DEC-02
func TestRevokingTheDecreeDeniesTheSameActionAtTheNextDecision(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	admin := w.person("Bia", users.Administrator, "loja")
	e := w.engine(t)
	d := w.confirm(allowCmd(admin, "dev", "npm install *"))
	if ans := e.run(t, "npm install x"); ans.Verdict != hook.VerdictAllow {
		t.Fatalf("%+v", ans)
	}
	if err := w.svc.Revoke(w.ctx, d.Number, admin.ID, ""); err != nil {
		t.Fatal(err)
	}
	if ans := e.run(t, "npm install x"); ans.Verdict != hook.VerdictDeny {
		t.Fatalf("after the revocation: %+v", ans)
	}
	h, err := w.svc.History(w.ctx, d.Number)
	if err != nil || len(h) != 2 || h[0].State != decrees.Active || h[1].State != decrees.Revoked || h[1].By != admin.ID {
		t.Fatalf("history %+v, %v", h, err)
	}
	if err := w.svc.Revoke(w.ctx, d.Number, admin.ID, ""); !errors.Is(err, decrees.ErrEnded) {
		t.Fatalf("a second revocation: %v", err)
	}
}

// CA-DEC-03
func TestAnExceptionExpiresUnusedWhenTheHeadOfThePRMoves(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	bia := w.person("Bia", users.Approver, "loja")
	w.heads.set("lucas/loja", 42, "abc")
	exc := func() decrees.Proposal {
		return decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetCharacter, Name: "d1"},
			Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "gh pr merge 42"}, Text: "aprovo o merge do PR #42", RequestType: "merge",
			TargetRef: json.RawMessage(`{"repo":"lucas/loja","pr":42,"head":"abc","base":"main"}`)}
	}
	moved := w.confirm(exc())
	w.heads.set("lucas/loja", 42, "def")
	if err := w.svc.Redeem(w.ctx, moved.Number, "mestre", "gh pr merge 42"); !errors.Is(err, decrees.ErrTargetChanged) {
		t.Fatalf("redeem after the new commit: %v", err)
	}
	got, _ := w.svc.Get(w.ctx, moved.Number)
	if got.State != decrees.Expired {
		t.Fatalf("state %s; want expired unused", got.State)
	}
	if uses, _ := w.svc.Uses(w.ctx, moved.Number); len(uses) != 0 {
		t.Fatalf("uses %+v", uses)
	}
	// the same exception, with the head as it was approved, is spent once
	w.heads.set("lucas/loja", 42, "abc")
	ok := w.confirm(exc())
	if err := w.svc.Redeem(w.ctx, ok.Number, "mestre", "gh pr merge 42"); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Redeem(w.ctx, ok.Number, "mestre", "gh pr merge 42"); !errors.Is(err, decrees.ErrEnded) {
		t.Fatalf("a second use: %v", err)
	}
	// and a target that can not be read is not trusted
	w.heads.set("lucas/loja", 42, "abc")
	blind := w.confirm(exc())
	w.heads.set("lucas/loja", 42, "xyz")
	if err := w.svc.Redeem(w.ctx, blind.Number, "mestre", "x"); err == nil {
		t.Fatal("redeemed against a different head")
	}
}

// CA-DEC-04
func TestTwoOpposedDecreesOfTheSameLevelSuspendEachOther(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana, bia := w.person("Ana", users.Administrator, "loja"), w.person("Bia", users.Administrator, "loja")
	allow := w.confirm(allowCmd(ana, "dev", "npm install *"))
	deny := w.confirm(denyCmd(bia, "dev", "npm install *"))
	for _, n := range []int64{allow.Number, deny.Number} {
		if got, _ := w.svc.Get(w.ctx, n); got.State != decrees.Suspended {
			t.Fatalf("decree %d is %s; want suspended", n, got.State)
		}
	}
	if decs, _ := w.svc.Decrees(w.ctx, dev("d1")); len(decs) != 0 {
		t.Fatalf("suspended decrees reach the rules: %+v", decs)
	}
	for _, u := range []users.User{ana, bia} {
		if told := w.al.toldTo(u.ID); len(told) != 1 {
			t.Fatalf("%s was told %q", u.Name, told)
		}
	}
	if told := w.al.ownerTold(); len(told) != 1 {
		t.Fatalf("the Owner was told %q", told)
	}
	// The Owner decides: revokes one and reactivates the other, which becomes his.
	if err := w.svc.Revoke(w.ctx, deny.Number, w.owner.ID, w.pin(w.owner, "decree_revoke:"+strconv.FormatInt(deny.Number, 10))); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Reactivate(w.ctx, allow.Number, w.owner.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, allow.Number); got.State != decrees.Active || got.Author != w.owner.ID {
		t.Fatalf("%+v", got)
	}
}

// CA-DEC-05
func TestTheDecreeOfTheOwnerPrevailsOverTheOpposedOneOfAnAdministrator(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	mine := w.confirm(allowCmd(w.owner, "dev", "npm install *"))
	theirs := w.confirm(denyCmd(ana, "dev", "npm install *"))
	if m, _ := w.svc.Get(w.ctx, mine.Number); m.State != decrees.Active {
		t.Fatalf("%+v", m)
	}
	if o, _ := w.svc.Get(w.ctx, theirs.Number); o.State != decrees.Active {
		t.Fatalf("the lower decree stays active: %+v", o)
	}
	views, err := w.svc.List(w.ctx, w.owner.ID)
	if err != nil || len(views) != 2 {
		t.Fatalf("%+v, %v", views, err)
	}
	for _, v := range views {
		switch v.Number {
		case theirs.Number:
			if v.OverriddenBy != mine.Number {
				t.Fatalf("the decree of the Administrator is overridden by %d; want %d", v.OverriddenBy, mine.Number)
			}
		case mine.Number:
			if v.OverriddenBy != 0 {
				t.Fatalf("%+v", v)
			}
		}
	}
	e := w.engine(t)
	if ans := e.run(t, "npm install x"); ans.Verdict != hook.VerdictAllow || ans.Decree != mine.Number {
		t.Fatalf("the Owner's decree decides: %+v", ans)
	}
}

// CA-DEC-06
func TestADecreeWithValidityExpiresAndTheAuthorIsTold(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	p := allowCmd(ana, "dev", "npm install *")
	p.Until = w.clk.Now().Add(72 * time.Hour)
	d := w.confirm(p)
	contains(t, mustPropose(t, w, p).Text, "até 22/10/2026 12:00 UTC")
	w.clk.Advance(71 * time.Hour)
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, d.Number); got.State != decrees.Active {
		t.Fatalf("at 71 h: %s", got.State)
	}
	w.clk.Advance(time.Hour)
	// even before the sweep the decree no longer reaches the rules
	if decs, _ := w.svc.Decrees(w.ctx, dev("d1")); len(decs) != 0 {
		t.Fatalf("an expired decree reaches the rules: %+v", decs)
	}
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, d.Number); got.State != decrees.Expired {
		t.Fatalf("at 72 h: %s", got.State)
	}
	if told := w.al.toldTo(ana.ID); len(told) != 1 {
		t.Fatalf("the author was told %q", told)
	}
}

func mustPropose(t *testing.T, w *world, p decrees.Proposal) decrees.Pending {
	t.Helper()
	pend, err := w.svc.Propose(w.ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	return pend
}

// CA-DEC-07
func TestAnOrderFromAudioShowsTheTranscriptAndNothingHoldsWithoutTheClickOfTheAuthor(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana, bia := w.person("Ana", users.Administrator, "loja"), w.person("Bia", users.Administrator, "loja")
	p := denyCmd(ana, "dev", "curl *")
	p.Origin = decrees.Origin{Channel: "telegram", MessageID: 9, Audio: true, Attachment: "voz-9.ogg", Transcript: "proíbe o curl para o dev"}
	pend := mustPropose(t, w, p)
	contains(t, pend.Text, "proíbe o curl para o dev")
	contains(t, pend.Text, "Confirma?")
	if decs, _ := w.svc.Decrees(w.ctx, dev("d1")); len(decs) != 0 || w.count("decrees") != 0 {
		t.Fatalf("a proposal is not a decree: %+v", decs)
	}
	// only the author clicks
	if _, err := w.svc.Confirm(w.ctx, pend.ID, bia.ID, ""); !errors.Is(err, decrees.ErrNotAuthor) {
		t.Fatalf("another user confirms: %v", err)
	}
	if err := w.svc.Cancel(w.ctx, pend.ID, bia.ID); !errors.Is(err, decrees.ErrNotAuthor) {
		t.Fatalf("another user cancels: %v", err)
	}
	if w.count("decrees") != 0 {
		t.Fatal("something was kept without the click of the author")
	}
	d, err := w.svc.Confirm(w.ctx, pend.ID, ana.ID, "")
	if err != nil {
		t.Fatalf("a decree that only forbids asks no PIN: %v", err)
	}
	if d.PIN || !d.Origin.Audio || d.Origin.Transcript != "proíbe o curl para o dev" || d.Origin.Attachment != "voz-9.ogg" {
		t.Fatalf("%+v", d)
	}
	if _, err := w.svc.Confirm(w.ctx, pend.ID, ana.ID, ""); !errors.Is(err, decrees.ErrNoProposal) {
		t.Fatalf("a proposal is confirmed once: %v", err)
	}
	// a cancelled proposal never becomes a decree; the proposal also expires
	p2 := mustPropose(t, w, denyCmd(ana, "dev", "wget *"))
	if err := w.svc.Cancel(w.ctx, p2.ID, ana.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Confirm(w.ctx, p2.ID, ana.ID, ""); !errors.Is(err, decrees.ErrNoProposal) {
		t.Fatalf("%v", err)
	}
	p3 := mustPropose(t, w, denyCmd(ana, "dev", "ftp *"))
	w.clk.Advance(decrees.ProposalTTL)
	if _, err := w.svc.Confirm(w.ctx, p3.ID, ana.ID, ""); !errors.Is(err, decrees.ErrNoProposal) {
		t.Fatalf("an old proposal: %v", err)
	}
}

// A wrong or expired PIN leaves the proposal cancelled.
func TestAWrongOrExpiredPINCancelsTheProposal(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	pend := mustPropose(t, w, allowCmd(ana, "dev", "npm install *"))
	req, err := w.svc.BeginPIN(w.ctx, pend.ID, ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	// the request was never answered right: it is not spent
	if _, err := w.svc.Confirm(w.ctx, pend.ID, ana.ID, req.ID); !errors.Is(err, decrees.ErrPINFailed) {
		t.Fatalf("%v", err)
	}
	if _, err := w.svc.Confirm(w.ctx, pend.ID, ana.ID, w.pin(ana, "decree:"+pend.ID)); !errors.Is(err, decrees.ErrNoProposal) {
		t.Fatalf("a cancelled proposal: %v", err)
	}
	if w.count("decrees") != 0 {
		t.Fatal("a decree was made")
	}
	// the PIN of another action does not confirm this one
	pend2 := mustPropose(t, w, allowCmd(ana, "dev", "make test"))
	if _, err := w.svc.Confirm(w.ctx, pend2.ID, ana.ID, w.pin(ana, "decree:other")); !errors.Is(err, decrees.ErrPINFailed) {
		t.Fatalf("%v", err)
	}
}

// The checks come before the confirmation and again at the click, against what
// is stored then: a user who lost the level or the workspace confirms nothing.
func TestTheLevelAndTheWorkspacesAreCheckedBeforeAndAtTheClick(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	approver := w.person("Caio", users.Approver, "loja")
	ana := w.person("Ana", users.Administrator, "loja")
	// an Approver makes exceptions, not rules (the level table of the users)
	ref := w.refusal(allowCmd(approver, "dev", "npm install *"))
	if ref.Code != decrees.CodeLevel {
		t.Fatalf("%+v", ref)
	}
	contains(t, ref.Message, "Administrador")
	// a decree outside the workspaces of the author
	other := allowCmd(ana, "dev", "make test")
	other.Workspace = "blog"
	if ref := w.refusal(other); ref.Code != decrees.CodeWorkspace {
		t.Fatalf("%+v", ref)
	}
	if w.count("decree_proposals") != 0 {
		t.Fatal("a refused proposal was kept")
	}
	// proposed by an Administrator, confirmed after he was demoted
	pend := mustPropose(t, w, denyCmd(ana, "dev", "curl *"))
	if _, err := w.users.Edit(w.ctx, w.owner, ana.ID, users.Edit{Level: ptr(users.Approver)}); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Confirm(w.ctx, pend.ID, ana.ID, ""); err == nil {
		t.Fatal("a demoted author confirmed a rule")
	}
	if w.count("decrees") != 0 {
		t.Fatal("a decree was made")
	}
	// nothing is made for an unknown author, an empty text or an unknown effect
	for _, p := range []decrees.Proposal{
		{Author: "nobody", Kind: decrees.KindRule, Workspace: "loja", Text: "x"},
		func() decrees.Proposal { p := denyCmd(w.owner, "dev", "x"); p.Text = " "; return p }(),
		func() decrees.Proposal { p := denyCmd(w.owner, "dev", "x"); p.Effect.Kind = "magic"; return p }(),
	} {
		if _, err := w.svc.Propose(w.ctx, p); err == nil {
			t.Fatalf("%+v was proposed", p)
		}
	}
}

func ptr[T any](v T) *T { return &v }

// CA-DEC-12
func TestADecreeThatChangesTheOrchestratorClassIsRefusedBeforeTheConfirmation(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	for _, field := range []string{"fixed_rules", "files.deny", "git.commit", "commands.default"} {
		p := allowCmd(ana, classes.ReservedOrchestrator, "x")
		p.Effect = decrees.Effect{Kind: decrees.EffectClassEdit, Field: field}
		p.Text = "tire a regra fixa " + field + " do orquestrador"
		ref := w.refusal(p)
		if ref.Code != decrees.CodeBuiltIn {
			t.Fatalf("%s: %+v", field, ref)
		}
	}
	if w.count("decree_proposals") != 0 || w.count("decrees") != 0 {
		t.Fatal("something was kept")
	}
	// the class is what it was: the built-in one still has no way to be edited
	if c, err := classes.Orchestrator(); err != nil || c.Name != classes.ReservedOrchestrator {
		t.Fatalf("%v", err)
	}
	// a liberation for an orchestrator is a decree as for any character
	d := w.confirm(allowCmd(ana, classes.ReservedOrchestrator, "npm install *"))
	gm := characters.Character{ID: "gm", Name: "Grão-Mestre", Role: characters.RoleGrandMaster, Workspace: "loja"}
	decs, err := w.svc.Decrees(w.ctx, gm)
	if err != nil || len(decs) != 1 || decs[0].ID != d.Number || decs[0].Level != classes.LevelAdmin {
		t.Fatalf("%+v, %v", decs, err)
	}
	// and a character of another class is not reached by it
	if decs, _ := w.svc.Decrees(w.ctx, dev("d1")); len(decs) != 0 {
		t.Fatalf("%+v", decs)
	}
	// a class edit is never a decree, for any class
	other := allowCmd(ana, "dev", "x")
	other.Effect = decrees.Effect{Kind: decrees.EffectClassEdit, Field: "commands.default"}
	if ref := w.refusal(other); ref.Code != decrees.CodeInvalid {
		t.Fatalf("%+v", ref)
	}
}

// Part of CA-DEC-09 (the part of the decree: the push stays denied, the engine
// fulfils it and the unmeetable is refused with the explanation)
func TestADecreeThatFreesAnInvariantIsKeptButTheAgentStillHasNoPush(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	d := w.confirm(allowCmd(ana, "dev", "git push *"))
	if !d.PIN {
		t.Fatalf("a push asks the PIN: %+v", d)
	}
	e := w.engine(t)
	if ans := e.run(t, "git push origin feat/x"); ans.Verdict != hook.VerdictDeny {
		t.Fatalf("the push of the agent is still denied: %+v", ans)
	}
	// what the engine can not do is refused before the confirmation
	p := allowCmd(ana, "dev", "x")
	p.Effect = decrees.Effect{Kind: decrees.EffectPolicy, Setting: "revisao_obrigatoria", Loosens: true}
	p.Text = "o dev pode ignorar a revisão obrigatória"
	if ref := w.refusal(p); ref.Code != decrees.CodeInvariant {
		t.Fatalf("%+v", ref)
	} else {
		contains(t, ref.Message, "contornar a proteção do GitHub (revisao_obrigatoria) nem o motor pode")
	}
	if n := w.count("decree_proposals"); n != 1 {
		t.Fatalf("%d proposals: the refused one was kept to confirm", n)
	}
	for _, g := range []classes.Grant{{Files: []string{".mestre/regras/*"}}, {Commands: []string{"claude --resume x"}}, {Commands: []string{"mestre vault get x"}}} {
		p := allowCmd(ana, "dev", "x")
		p.Effect = decrees.Effect{Kind: decrees.EffectPermission, Allow: &g}
		if ref := w.refusal(p); ref.Code != decrees.CodeInvariant {
			t.Fatalf("%+v: %+v", g, ref)
		}
	}
	// the execution by the engine is registered in the decree
	if err := w.svc.Executed(w.ctx, d.Number, "d1", "mestre pr update"); err != nil {
		t.Fatal(err)
	}
	if uses, _ := w.svc.Uses(w.ctx, d.Number); len(uses) != 1 || uses[0].Action != "mestre pr update" {
		t.Fatalf("%+v", uses)
	}
}

// The tiers of the PIN: only what loosens the security asks it.
func TestOnlyWhatLoosensTheSecurityAsksThePIN(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	bia := w.person("Bia", users.Approver, "loja")
	cases := map[string]struct {
		p   decrees.Proposal
		pin bool
	}{
		"a rule that only forbids":   {denyCmd(ana, "dev", "curl *"), false},
		"a rule that frees":          {allowCmd(ana, "dev", "npm install *"), true},
		"a behavior":                 {decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetClass, Name: "dev"}, Effect: decrees.Effect{Kind: decrees.EffectBehavior, Instruction: "use pnpm"}, Text: "use pnpm"}, false},
		"a tightened policy":         {decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectPolicy, Setting: "acao_destrutiva"}, Text: "me pergunte tudo"}, false},
		"a loosened policy of medio": {decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectPolicy, Setting: "migracoes", Loosens: true}, Text: "pode aprovar migrações"}, false},
		"a wider ceiling":            {decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectCeiling, Allow: &classes.Grant{Domains: []string{"api.exemplo.io"}}}, Text: "amplie o teto"}, true},
		"an exception of the Approver": {decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetCharacter, Name: "d1"},
			Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "npm install x"}, Text: "libera"}, false},
		"an exception of a merge": {decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetCharacter, Name: "d1"},
			Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "gh pr merge 42"}, Text: "libera o merge"}, true},
	}
	for name, c := range cases {
		pend, err := w.svc.Propose(w.ctx, c.p)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if pend.PIN != c.pin {
			t.Errorf("%s: PIN %v; want %v (%s)", name, pend.PIN, c.pin, pend.Why)
		}
	}
	// the Administrator does not widen the ceiling of the Owner's reach: delegation is the Owner's
	del := decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"},
		Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "configuracao_repositorio"}, Text: "delegue"}
	if ref := w.refusal(del); ref.Code != decrees.CodeLevel {
		t.Fatalf("%+v", ref)
	}
}

// Revoking: nobody revokes a decree of a level above the own; a revocation of a
// decree that restricted something asks the PIN; an Approver revokes only his
// own exceptions not used yet.
func TestRevokingByLevel(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	bia := w.person("Bia", users.Administrator, "loja")
	caio := w.person("Caio", users.Approver, "loja")
	dani := w.person("Dani", users.Approver, "loja")
	forbidding := w.confirm(denyCmd(ana, "dev", "curl *"))
	byOwner := w.confirm(allowCmd(w.owner, "dev", "make test"))
	exc := func(u users.User, cmd string) decrees.Decree {
		return w.confirm(decrees.Proposal{Author: u.ID, Kind: decrees.KindException, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetCharacter, Name: "d1"},
			Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: cmd}, Text: "libera " + cmd})
	}
	excCaio := exc(caio, "make a")
	// an Administrator does not revoke the Owner's
	if err := w.svc.Revoke(w.ctx, byOwner.Number, bia.ID, ""); !errors.Is(err, decrees.ErrNotAllowed) {
		t.Fatalf("%v", err)
	}
	// an Approver does not revoke a rule, nor the exception of another
	if err := w.svc.Revoke(w.ctx, forbidding.Number, caio.ID, ""); !errors.Is(err, decrees.ErrNotAllowed) {
		t.Fatalf("%v", err)
	}
	if err := w.svc.Revoke(w.ctx, excCaio.Number, dani.ID, ""); !errors.Is(err, decrees.ErrNotAllowed) {
		t.Fatalf("%v", err)
	}
	// revoking what restricted something asks the PIN
	if err := w.svc.Revoke(w.ctx, forbidding.Number, bia.ID, ""); !errors.Is(err, decrees.ErrPINRequired) {
		t.Fatalf("%v", err)
	}
	if err := w.svc.Revoke(w.ctx, forbidding.Number, bia.ID, w.pin(bia, "decree_revoke:"+strconv.FormatInt(forbidding.Number, 10))); err != nil {
		t.Fatal(err)
	}
	if told := w.al.toldTo(ana.ID); len(told) != 1 {
		t.Fatalf("the author was told %q", told)
	}
	// the Approver revokes his own exception not used yet
	if err := w.svc.Revoke(w.ctx, excCaio.Number, caio.ID, ""); err != nil {
		t.Fatal(err)
	}
	// outside the workspaces of the user nothing is revoked
	blogger := w.person("Eva", users.Administrator, "blog")
	if err := w.svc.Revoke(w.ctx, byOwner.Number, blogger.ID, ""); !errors.Is(err, decrees.ErrNotAllowed) {
		t.Fatalf("%v", err)
	}
	if err := w.svc.Revoke(w.ctx, 999, w.owner.ID, ""); !errors.Is(err, decrees.ErrNoDecree) {
		t.Fatalf("%v", err)
	}
}

// An exception is spent once, by the ledger of the hook.
func TestAnExceptionIsUsedOnceByTheLedger(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	bia := w.person("Bia", users.Approver, "loja")
	d := w.confirm(decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetCharacter, Name: "d1"},
		Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "npm install x"}, Text: "libera"})
	e := w.engine(t)
	if ans := e.run(t, "npm install x"); ans.Verdict != hook.VerdictAllow || ans.Decree != d.Number {
		t.Fatalf("%+v", ans)
	}
	if ans := e.run(t, "npm install x"); ans.Verdict != hook.VerdictDeny {
		t.Fatalf("an exception is spent once: %+v", ans)
	}
	if got, _ := w.svc.Get(w.ctx, d.Number); got.State != decrees.Used {
		t.Fatalf("%s", got.State)
	}
	// an exception not used expires with the time of the approvals
	again := w.confirm(decrees.Proposal{Author: bia.ID, Kind: decrees.KindException, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetCharacter, Name: "d1"},
		Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "make x"}, Text: "libera"})
	w.clk.Advance(decrees.DefaultExceptionTTL)
	if ans := e.run(t, "make x"); ans.Verdict != hook.VerdictDeny {
		t.Fatalf("an old exception: %+v", ans)
	}
	if err := w.svc.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, again.Number); got.State != decrees.Expired {
		t.Fatalf("%s", got.State)
	}
}

// A suspended or removed author takes his exceptions with him and leaves his
// rules suspended; a demoted one the same for what the new level does not allow.
func TestTheDecreesOfAnAuthorWhoLeavesAreInvalidatedOrSuspended(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	rule := w.confirm(allowCmd(ana, "dev", "npm install *"))
	exc := w.confirm(decrees.Proposal{Author: ana.ID, Kind: decrees.KindException, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetCharacter, Name: "d1"},
		Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "make x"}, Text: "libera"})
	if err := w.users.Suspend(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.InvalidateBy(w.ctx, ana.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := w.svc.Get(w.ctx, exc.Number); got.State != decrees.Revoked {
		t.Fatalf("the exception: %s", got.State)
	}
	if got, _ := w.svc.Get(w.ctx, rule.Number); got.State != decrees.Suspended {
		t.Fatalf("the rule: %s", got.State)
	}
	if decs, _ := w.svc.Decrees(w.ctx, dev("d1")); len(decs) != 0 {
		t.Fatalf("%+v", decs)
	}
	if told := w.al.ownerTold(); len(told) != 1 {
		t.Fatalf("the Owner was told %q", told)
	}
	contains(t, w.al.ownerTold()[0], "decreto "+strconv.FormatInt(rule.Number, 10))
	// the Owner reactivates it and it becomes his
	if err := w.svc.Reactivate(w.ctx, rule.Number, w.owner.ID); err != nil {
		t.Fatal(err)
	}
	decs, _ := w.svc.Decrees(w.ctx, dev("d1"))
	if len(decs) != 1 || decs[0].Level != classes.LevelOwner {
		t.Fatalf("%+v", decs)
	}
	// the level that counts is the one the author has now
	bia := w.person("Bia", users.Administrator, "loja")
	r2 := w.confirm(allowCmd(bia, "dev", "make y"))
	if _, err := w.users.Edit(w.ctx, w.owner, bia.ID, users.Edit{Level: ptr(users.Approver)}); err != nil {
		t.Fatal(err)
	}
	for _, d := range func() []classes.Decree { x, _ := w.svc.Decrees(w.ctx, dev("d1")); return x }() {
		if d.ID == r2.Number && d.Level != classes.LevelApprover {
			t.Fatalf("the decree of a demoted author counts as %s", d.Level)
		}
	}
	if err := w.svc.Reactivate(w.ctx, r2.Number, bia.ID); !errors.Is(err, decrees.ErrNotAllowed) {
		t.Fatalf("only the Owner reactivates: %v", err)
	}
}

// The scope of a decree: workspace, table and target.
func TestADecreeReachesOnlyItsTargetAndScope(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	p := allowCmd(ana, "dev", "make a")
	p.Mesa = "42-login"
	w.confirm(p)
	q := allowCmd(ana, "dev", "make b")
	q.Target = decrees.Target{Kind: decrees.TargetCharacter, Name: "d2"}
	w.confirm(q)
	d1 := dev("d1")
	if decs, _ := w.svc.Decrees(w.ctx, d1); len(decs) != 1 || decs[0].Effect.Allow.Commands[0] != "make a" {
		t.Fatalf("%+v", decs)
	}
	otherTable := d1
	otherTable.Table = "43-api"
	if decs, _ := w.svc.Decrees(w.ctx, otherTable); len(decs) != 0 {
		t.Fatalf("%+v", decs)
	}
	otherWS := d1
	otherWS.Workspace = "blog"
	if decs, _ := w.svc.Decrees(w.ctx, otherWS); len(decs) != 0 {
		t.Fatalf("%+v", decs)
	}
	if decs, _ := w.svc.Decrees(w.ctx, dev("d2")); len(decs) != 2 {
		t.Fatalf("%+v", decs)
	}
}

// The behavior decrees reach the Mestre layer.
func TestBehaviorDecreesBecomeNotesOfTheMestreLayer(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.person("Ana", users.Administrator, "loja")
	d := w.confirm(decrees.Proposal{Author: ana.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetClass, Name: "dev"},
		Effect: decrees.Effect{Kind: decrees.EffectBehavior, Instruction: "use pnpm, não npm"}, Text: "o dev usa pnpm"})
	notes, err := w.svc.Notes(w.ctx, dev("d1"))
	if err != nil || len(notes) != 1 || notes[0].Number != d.Number || notes[0].Author != "Ana" || notes[0].Level != "Administrador" || notes[0].Text != "use pnpm, não npm" {
		t.Fatalf("%+v, %v", notes, err)
	}
	if decs, _ := w.svc.Decrees(w.ctx, dev("d1")); len(decs) != 0 {
		t.Fatalf("a behavior is no permission: %+v", decs)
	}
}
