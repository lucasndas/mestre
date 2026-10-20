package telegram_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

const lockedAgent = "alice"

// lockRules are the effective rules of a character that may run `npm test`,
// built as CitadelClasses builds them: the lock of the Mestre decides Locked.
type lockRules struct{ locked func(context.Context) bool }

func (l lockRules) Rules(ctx context.Context, id string) (*classes.Rules, error) {
	return &classes.Rules{Version: classes.RulesVersion, AgentID: id, FixedRulesVersion: classes.FixedRulesVersion,
		Files:    classes.FileRules{Read: []string{"**"}},
		Commands: classes.PatternRules{Allow: []string{"npm test"}, Default: classes.Deny},
		Tools:    classes.PatternRules{Default: classes.Deny}, Locked: l.locked(ctx)}, nil
}

type lockChars struct{}

func (lockChars) Env(context.Context, string) (classes.Env, error) {
	return classes.Env{AgentID: lockedAgent}, nil
}

// useTool is what a character's `mestre hook` asks the engine when it tries
// `npm test`, and the verdict that comes back.
func useTool(t *testing.T, e *hook.Engine) hook.Answer {
	t.Helper()
	ev, _ := json.Marshal(providers.Event{Version: providers.EventVersion, Provider: providers.ClaudeCode, NativeEvent: "PreToolUse", Event: providers.EventPreTool,
		AgentID: lockedAgent, Tool: &providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "npm test"}})
	resp := e.Handle(t.Context(), &localchan.Call{Request: localchan.Request{AgentID: lockedAgent, Cmd: localchan.CmdHook, Args: []json.RawMessage{ev}},
		Caller: localchan.Caller{Identified: true, AgentID: lockedAgent}})
	var ans hook.Answer
	if err := resp.DecodeData(&ans); err != nil {
		t.Fatal(err)
	}
	return ans
}

// decreeLines are the lines of the last /decretos answer.
func decreeLines(w *world, chat int64) []string {
	text := lastTextOf(w, chat)
	i := strings.Index(text, "Decretos:\n")
	if i < 0 {
		return nil
	}
	return strings.Split(text[i+len("Decretos:\n"):], "\n")
}

// CA-CANAL-04
func TestAfterTheConfirmedTravarTheHookDeniesEveryToolAndEveryDecreeIsShownSuspended(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{decrees: true, lockdown: true})
	ana, anaTG := w.pinned("Ana", users.Administrator, "loja")
	bia, biaTG := w.pinned("Bia", users.Administrator, "loja")
	w.confirmNow(ana, allowProposal(ana, "dev", "make x"))
	w.confirmNow(bia, allowProposal(bia, "dev", "make y"))
	e := &hook.Engine{Clock: clock.Real{}, Rules: lockRules{locked: w.lock.Locked}, Characters: lockChars{}}

	if ans := useTool(t, e); ans.Verdict != hook.VerdictAllow {
		t.Fatalf("unlocked: %+v", ans)
	}
	w.say(anaTG, "/decretos")
	w.drain()
	if list := lastText(w, anaTG); strings.Contains(list, "SUSPENSO") {
		t.Fatalf("a decree shown suspended before the lock: %q", list)
	}

	// /travar confirmed by an Administrator
	w.say(biaTG, "/travar")
	w.drain()
	msg, keys := w.buttons(biaTG)
	w.click(biaTG, msg, keys["Confirmar travamento"])
	if !w.lock.Locked(w.ctx) {
		t.Fatal("the confirmed /travar did not lock")
	}
	w.drain()

	// a character trying a tool: the hook denies
	ans := useTool(t, e)
	if ans.Verdict != hook.VerdictDeny || !strings.Contains(ans.Reason, "sistema travado") {
		t.Fatalf("locked: %+v, want deny by the lock", ans)
	}
	// all the decrees appear suspended, with no Revogar to offer
	w.say(anaTG, "/decretos")
	w.drain()
	list := lastText(w, anaTG)
	lines := decreeLines(w, anaTG.ID)
	if len(lines) != 2 {
		t.Fatalf("/decretos says %q", list)
	}
	for _, l := range lines {
		if !strings.Contains(l, "SUSPENSO (sistema travado)") {
			t.Errorf("a decree is not shown suspended: %q", l)
		}
	}
	sent := w.sent("sendMessage")
	for i := len(sent) - 1; i >= 0; i-- {
		if sent[i].ChatID != anaTG.ID {
			continue
		}
		if kb, ok := sent[i].Params["reply_markup"].(map[string]any); ok && kb["inline_keyboard"] != nil {
			t.Errorf("buttons offered while locked: %v", kb["inline_keyboard"])
		}
		break
	}
	// the state in the database is untouched: the lock is not a suspension of the decree
	for _, n := range []int64{1, 2} {
		if d, _ := w.ds.Get(w.ctx, n); d.State != decrees.Active {
			t.Errorf("decree %d is %s in the database", n, d.State)
		}
	}
}

// CA-CANAL-05
func TestTheOwnersDestravarWithThePINBringsTheDecreesBackAsTheyWereAndDeliversWhatWasKept(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{decrees: true, lockdown: true})
	ana, anaTG := w.pinned("Ana", users.Administrator, "loja")
	bia, _ := w.pinned("Bia", users.Administrator, "loja")
	if err := w.users.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	owner := w.pairOwnerTelegram()
	// decree 1 is active; 2 and 3 oppose each other at the same level and stay suspended by the conflict
	w.confirmNow(ana, allowProposal(ana, "dev", "make x"))
	w.confirmNow(ana, allowProposal(ana, "dev", "make z"))
	deny := allowProposal(bia, "dev", "make z")
	deny.Effect = decrees.Effect{Kind: decrees.EffectPermission, Deny: &classes.Grant{Commands: []string{"make z"}}}
	w.confirmNow(bia, deny)
	e := &hook.Engine{Clock: clock.Real{}, Rules: lockRules{locked: w.lock.Locked}, Characters: lockChars{}}

	state := func() map[int64]string {
		w.say(anaTG, "/decretos")
		w.drain()
		out := map[int64]string{}
		for _, l := range decreeLines(w, anaTG.ID) {
			var n int64
			if i := strings.Index(l, "#"); i >= 0 {
				_, _ = fmt.Sscan(l[i+1:], &n)
			}
			switch {
			case strings.Contains(l, "(sistema travado)"):
				out[n] = "travado"
			case strings.Contains(l, "SUSPENSO"):
				out[n] = "conflito"
			default:
				out[n] = "ativo"
			}
		}
		return out
	}
	before := state()
	if before[1] != "ativo" || before[2] != "conflito" || before[3] != "conflito" {
		t.Fatalf("before the lock: %v", before)
	}
	if err := w.lock.Lock(w.ctx, mustUser(t, w, "Ana")); err != nil {
		t.Fatal(err)
	}
	w.drain()
	if got := state(); got[1] != "travado" || got[2] != "travado" || got[3] != "travado" {
		t.Fatalf("locked: %v", got)
	}
	if ans := useTool(t, e); ans.Verdict != hook.VerdictDeny {
		t.Fatalf("locked: %+v", ans)
	}
	w.say(owner, "uma mensagem guardada")
	w.drain()
	if w.router.count() != 0 {
		t.Fatal("a message reached the Grand Master while locked")
	}

	w.say(owner, "/destravar")
	w.drain()
	w.say(owner, "123456")
	w.drain()
	if w.lock.Locked(w.ctx) {
		t.Fatal("the right PIN did not unlock")
	}
	after := state()
	for n, want := range before {
		if after[n] != want {
			t.Errorf("decree %d is %q after the unlock, was %q", n, after[n], want)
		}
	}
	if ans := useTool(t, e); ans.Verdict != hook.VerdictAllow {
		t.Errorf("unlocked: %+v", ans)
	}
	if got := w.router.all(); len(got) != 1 || got[0].Text != "uma mensagem guardada" {
		t.Errorf("the Grand Master got %+v", got)
	}
}

func lastTextOf(w *world, chat int64) string {
	all := texts(w, chat)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}
