package characters_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/waits"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
)

// The paste of the adapters: the text between the markers of the bracketed
// paste, then the key of sending.
const (
	pasteStart = "\x1b[200~"
	pasteEnd   = "\x1b[201~"
)

// plain takes the markers off what was typed.
func plain(t *testing.T, typed string) string {
	t.Helper()
	body, ok := strings.CutPrefix(typed, pasteStart)
	if !ok {
		t.Fatalf("%q was not typed as a paste", typed)
	}
	body, ok = strings.CutSuffix(body, pasteEnd+"\r")
	if !ok {
		t.Fatalf("%q does not end with the end of the paste and Enter", typed)
	}
	return body
}

// grandMaster starts the Grand Master of the workspace and leaves it idle.
func (r *rig) grandMaster() characters.Character {
	r.t.Helper()
	res, err := r.create(characters.CreateRequest{Sheet: "Dev Claude", Role: characters.RoleGrandMaster})
	if err != nil {
		r.t.Fatal(err)
	}
	r.ready(res.Character)
	return res.Character
}

func (r *rig) enqueue(n inbox.NewItem) inbox.Item {
	r.t.Helper()
	if n.Workspace == "" {
		n.Workspace = workspace
	}
	it, err := r.ib.Enqueue(r.ctx, n)
	if err != nil {
		r.t.Fatal(err)
	}
	return it
}

func (r *rig) itemState(id string) inbox.State {
	r.t.Helper()
	it, err := r.ib.Store().Get(r.ctx, id)
	if err != nil {
		r.t.Fatal(err)
	}
	return it.State
}

func lucas(table string) *inbox.Origin {
	return &inbox.Origin{Channel: inbox.ChannelTelegram, User: "lucas", UserName: "Lucas", Level: characters.LevelOwner, Table: table}
}

// CA-ESP-10
func TestAMessageOfTheOwnerFromTelegramIsTypedWithTheHeaderAsTheFirstLine(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true})
	gm := r.grandMaster()
	it := r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: "como está o login?", Origin: lucas("42-login")})
	typed := r.machineOf(gm).host.proc(gm.ID).typedAll()
	if len(typed) != 1 {
		t.Fatalf("typed = %q", typed)
	}
	text := plain(t, typed[0])
	first, rest, _ := strings.Cut(text, "\n")
	if want := "[Mestre · user · de Lucas (Dono) · via Telegram · mesa 42-login · " + it.ID + "]"; first != want || rest != "como está o login?" {
		t.Fatalf("typed %q, want the line %q and then the text", text, want)
	}
	if got := r.state(gm); got != characters.Busy {
		t.Errorf("state = %s: the delivery puts the character to work", got)
	}
}

// CA-ESP-02, with the terminal: what arrives while the character works is typed
// at the end of the turn, one item per turn.
func TestWhatArrivesWhileTheCharacterWorksIsTypedOnlyAtTheEndOfTheTurn(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true})
	gm := r.grandMaster()
	proc := r.machineOf(gm).host.proc(gm.ID)
	first := r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: "primeira", Origin: lucas("")})
	second := r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindAgent, Sender: "Dev", Text: "segunda"})
	third := r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindSystem, Sender: inbox.SystemSender, Text: "terceira"})
	if n := len(proc.typedAll()); n != 1 {
		t.Fatalf("%d items typed while it works, want only the first", n)
	}
	r.hook(gm, providers.EventTurnStart) // the turn of the first begins: it is confirmed
	r.advance(5 * time.Minute)           // time passing does not type anything either
	if n := len(proc.typedAll()); n != 1 {
		t.Fatalf("%d items typed after 5 minutes", n)
	}
	if got := r.itemState(first.ID); got != inbox.Delivered {
		t.Fatalf("the first is %s after turn_start", got)
	}
	r.hook(gm, providers.EventTurnEnd)
	typed := proc.typedAll()
	if len(typed) != 2 || !strings.Contains(plain(t, typed[1]), "segunda") {
		t.Fatalf("typed = %q after the first turn", typed)
	}
	r.hook(gm, providers.EventTurnStart)
	r.hook(gm, providers.EventTurnEnd)
	typed = proc.typedAll()
	if len(typed) != 3 || !strings.Contains(plain(t, typed[2]), "terceira") {
		t.Fatalf("typed = %q after the second turn", typed)
	}
	r.hook(gm, providers.EventTurnStart)
	r.hook(gm, providers.EventTurnEnd)
	if len(proc.typedAll()) != 3 {
		t.Errorf("typed again with the queue empty")
	}
	for _, id := range []string{first.ID, second.ID, third.ID} {
		if got := r.itemState(id); got != inbox.Delivered {
			t.Errorf("%s is %s", id, got)
		}
	}
}

func TestATextAbove2000CharactersGoesToAFileAndOnlyTheHeaderAndTheRequestAreTyped(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true})
	gm := r.grandMaster()
	long := strings.Repeat("uma frase comprida. ", 120) // 2400 characters
	it := r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindAgent, Sender: "Dev", Text: long})
	typed := r.machineOf(gm).host.proc(gm.ID).typedAll()
	if len(typed) != 1 {
		t.Fatalf("typed = %q", typed)
	}
	text := plain(t, typed[0])
	rel := characters.InboxFile(gm.ID, it.ID)
	if rel != ".mestre/agents/"+gm.ID+"/inbox/"+it.ID+".md" {
		t.Fatalf("file = %s", rel)
	}
	head, request, _ := strings.Cut(text, "\n")
	if head != "[Mestre · agent · de Dev · "+it.ID+"]" || !strings.Contains(request, rel) || strings.Contains(text, "uma frase comprida") {
		t.Fatalf("typed %q: the header and the request to read %s, nothing else", text, rel)
	}
	dir := r.get(gm.ID).Dir
	if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err != nil || string(b) != long {
		t.Fatalf("the file %s = %d bytes, %v; want the whole text", rel, len(b), err)
	}
	// 2000 is the limit: a text of exactly that size is typed
	r.hook(gm, providers.EventTurnStart)
	r.hook(gm, providers.EventTurnEnd)
	exact := strings.Repeat("a", 2000)
	r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindAgent, Sender: "Dev", Text: exact})
	typed = r.machineOf(gm).host.proc(gm.ID).typedAll()
	if len(typed) != 2 || !strings.HasSuffix(plain(t, typed[1]), "\n"+exact) {
		t.Errorf("a text of 2000 characters was not typed as it is: %d typed", len(typed))
	}
}

// A text the adapter refuses as it is (here, above 8 KiB; also the several
// lines of a version of the provider that does not take them) goes through the
// file too, whatever its size in characters.
func TestATextTheAdapterRefusesGoesThroughTheFileToo(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true, longText: 100000})
	gm := r.grandMaster()
	huge := strings.Repeat("x", providers.MaxTyped+1)
	it := r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindAgent, Sender: "Dev", Text: huge})
	typed := r.machineOf(gm).host.proc(gm.ID).typedAll()
	if len(typed) != 1 || strings.Contains(plain(t, typed[0]), "xxxx") || !strings.Contains(plain(t, typed[0]), characters.InboxFile(gm.ID, it.ID)) {
		t.Fatalf("the huge text was typed: %d typed", len(typed))
	}
	dir := r.get(gm.ID).Dir
	if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(characters.InboxFile(gm.ID, it.ID)))); err != nil || len(b) != len(huge) {
		t.Errorf("file: %d bytes, %v", len(b), err)
	}
}

// Part of CA-PROTO-08 (the part of the machine and its router; the ack over the
// live connection is the protocol's): a terminal that became busy answers busy,
// types nothing, and the item goes back to the queue of the Citadel.
func TestATerminalThatBecameBusyAnswersBusyAndNothingIsTyped(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true})
	gm := r.grandMaster()
	mr := r.machineOf(gm)
	r.hook(gm, providers.EventTurnStart) // working
	h := mr.rt.InboxHandler()
	rep, err := h.Handle(r.ctx, &protocol.Message{Payload: &protocol.InboxDeliver{TerminalID: r.get(gm.ID).TerminalID, ItemID: "m99", Text: "oi", Header: "[Mestre · agent · de Dev · m99]"}})
	if err != nil || rep == nil || rep.Type != protocol.TypeInboxResult {
		t.Fatalf("handler = %+v, %v", rep, err)
	}
	res, ok := rep.Payload.(*protocol.InboxResult)
	if !ok || res.Status != protocol.InboxBusy || res.ItemID != "m99" {
		t.Fatalf("result = %+v", rep.Payload)
	}
	if n := len(mr.host.proc(gm.ID).typedAll()); n != 0 {
		t.Fatalf("typed %d things in a busy terminal", n)
	}
	// the Citadel, told "busy", gives the item back and delivers it when free
	it := r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindAgent, Sender: "Dev", Text: "depois"})
	if err := r.ib.OnResult(r.ctx, protocol.InboxResult{ItemID: it.ID, Status: protocol.InboxBusy}); err != nil {
		t.Fatal(err)
	}
	if got := r.itemState(it.ID); got != inbox.Queued {
		t.Fatalf("after busy the item is %s", got)
	}
	r.hook(gm, providers.EventTurnEnd)
	if n := len(mr.host.proc(gm.ID).typedAll()); n != 1 {
		t.Fatalf("typed %d after the turn ended, want the item once", n)
	}
	// an unknown terminal, and an id that is not an id
	if _, err := mr.rt.DeliverInbox(r.ctx, protocol.InboxDeliver{TerminalID: "nenhum", ItemID: "m1", Text: "x"}); err == nil {
		t.Error("a delivery to a terminal of no character")
	}
	if _, err := mr.rt.DeliverInbox(r.ctx, protocol.InboxDeliver{TerminalID: r.get(gm.ID).TerminalID, ItemID: "../../x", Text: "x"}); err == nil {
		t.Error("an id that names a path")
	}
}

// The supervision of the adapter: no turn within 10 s, the delivery is recorded
// as unconfirmed, the table master is told, and it is never typed again.
func TestADeliveryThatStartsNoTurnIn10SecondsIsUnconfirmedAndNeverTypedAgain(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true})
	gm := r.grandMaster()
	proc := r.machineOf(gm).host.proc(gm.ID)
	it := r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindAgent, Sender: "Dev", Text: "oi"})
	r.clk.Advance(9 * time.Second)
	if got, _ := r.ib.Store().Get(r.ctx, it.ID); got.State != inbox.Delivering {
		t.Fatalf("state = %s at 9 s", got.State)
	}
	r.clk.Advance(time.Second)
	got, _ := r.ib.Store().Get(r.ctx, it.ID)
	if got.State != inbox.Delivered || !got.Unconfirmed {
		t.Fatalf("at 10 s: %+v", got)
	}
	if len(r.notes.of(characters.NoticeUnconfirmed)) != 1 {
		t.Errorf("notices = %+v", r.notes.of(characters.NoticeUnconfirmed))
	}
	if n := len(proc.typedAll()); n != 1 {
		t.Errorf("typed %d times, want once", n)
	}
}

// 30 s after the delivery with no turn: the key Enter, once; persisting, stuck.
func TestADeliveryThatStartsNoTurnIn30SecondsGetsEnterOnceAndThenTheCharacterIsStuck(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true})
	gm := r.grandMaster()
	proc := r.machineOf(gm).host.proc(gm.ID)
	r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindAgent, Sender: "Dev", Text: "oi"})
	r.clk.Advance(29 * time.Second)
	if n := len(proc.typedAll()); n != 1 {
		t.Fatalf("typed %d things at 29 s", n)
	}
	r.clk.Advance(time.Second)
	typed := proc.typedAll()
	if len(typed) != 2 || typed[1] != "\r" {
		t.Fatalf("typed = %q at 30 s, want Enter alone", typed)
	}
	if got := r.state(gm); got != characters.Busy {
		t.Fatalf("state = %s", got)
	}
	r.clk.Advance(29 * time.Second)
	if got := r.state(gm); got != characters.Busy {
		t.Fatalf("state = %s at 59 s", got)
	}
	r.clk.Advance(time.Second)
	if got := r.state(gm); got != characters.Stuck {
		t.Fatalf("state = %s at 60 s, want stuck", got)
	}
	if n := len(proc.typedAll()); n != 2 {
		t.Errorf("Enter was typed %d times, want once", n-1)
	}
}

func TestATurnThatStartsStopsTheWatchOfTheDelivery(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true})
	gm := r.grandMaster()
	proc := r.machineOf(gm).host.proc(gm.ID)
	r.enqueue(inbox.NewItem{Target: gm.ID, Kind: inbox.KindAgent, Sender: "Dev", Text: "oi"})
	r.clk.Advance(5 * time.Second)
	r.hook(gm, providers.EventTurnStart)
	r.clk.Advance(2 * time.Minute)
	if n := len(proc.typedAll()); n != 1 || r.state(gm) != characters.Busy {
		t.Fatalf("typed %d, state %s: a turn that began must not get Enter or be stuck", n, r.state(gm))
	}
}

// The answer of an ask is the done of the turn that handled it, or the last
// message of the agent, through the events of the real runtime; the answer of an
// ask nobody waits for comes to the inbox of the asker, whole.
func TestTheEndOfTheTurnOfTheRuntimeAnswersTheAskOfAnotherCharacter(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true})
	dev := r.make("Dev Claude", "Dev", "3-login")
	rev := r.make("Dev Claude", "Revisor", "3-login")
	r.ready(dev)
	r.ready(rev)
	if err := r.conns.Link(r.ctx, workspace, "3-login", connectionsChar(dev), connectionsChar(rev), ""); err != nil {
		t.Fatal(err)
	}
	r.hook(dev, providers.EventTurnStart) // Dev is inside its shell tool
	out, err := r.wt.Ask(r.ctx, waits.AskRequest{From: dev, To: rev, Prompt: "revise o PR", NoWait: true})
	if err != nil || out.Status != waits.StatusSent {
		t.Fatalf("Ask = %+v, %v", out, err)
	}
	typed := r.machineOf(rev).host.proc(rev.ID).typedAll()
	if len(typed) != 1 || !strings.Contains(plain(t, typed[0]), "revise o PR") || !strings.HasPrefix(plain(t, typed[0]), "[Mestre · agent · de Dev · ") {
		t.Fatalf("the question was typed as %q", typed)
	}
	r.hook(rev, providers.EventTurnStart)
	if err := r.svc.Done(r.ctx, rev.ID, "PR aprovado"); err != nil {
		t.Fatal(err)
	}
	end := event(rev, providers.EventTurnEnd)
	end.LastMessage = "isto não é a resposta"
	r.machineOf(rev).rt.OnHook(end, hookAnswer())
	items, _ := r.ib.List(r.ctx, dev.ID)
	if len(items) != 1 || items[0].Kind != inbox.KindAnswer || !strings.Contains(items[0].Text, "PR aprovado") || strings.Contains(items[0].Text, "não é a resposta") {
		t.Fatalf("the inbox of the asker = %+v", items)
	}
	// the same with no done: the last message of the agent is the answer
	if _, err := r.wt.Ask(r.ctx, waits.AskRequest{From: dev, To: rev, Prompt: "e o outro PR?", NoWait: true}); err != nil {
		t.Fatal(err)
	}
	r.hook(rev, providers.EventTurnStart)
	end.LastMessage = "o outro também está bom"
	r.machineOf(rev).rt.OnHook(end, hookAnswer())
	items, _ = r.ib.List(r.ctx, dev.ID)
	if len(items) != 2 || !strings.Contains(items[1].Text, "o outro também está bom") {
		t.Fatalf("the inbox of the asker = %+v", items)
	}
	// and the asker, once its turn ends, gets both, one per turn
	r.hook(dev, providers.EventTurnEnd)
	if n := len(r.machineOf(dev).host.proc(dev.ID).typedAll()); n != 1 {
		t.Fatalf("typed %d in the asker, want the first answer", n)
	}
}

func connectionsChar(c characters.Character) connections.Ref { return connections.Char(c.ID) }

func hookAnswer() hook.Answer { return hook.Answer{} }

// CA-CLI-CONV-07: a target waiting for a permission (blocked by permission)
// refuses `ask --raw` with 3 and permission_prompt, and nothing is typed. The
// runtime of the machine is the last word, past what the registry says.
func TestAskRawIntoATargetWaitingForAPermissionIsExit3AndNothingIsTyped(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true})
	dev := r.make("Dev Claude", "Dev", "3-login")
	rev := r.make("Dev Claude", "Revisor", "3-login")
	r.ready(dev)
	r.ready(rev)
	if err := r.conns.Link(r.ctx, workspace, "3-login", connectionsChar(dev), connectionsChar(rev), ""); err != nil {
		t.Fatal(err)
	}
	r.busy(rev)
	r.hook(rev, providers.EventPermissionRequest) // the native prompt of the provider is on the screen
	fwd := localchan.NewLocalForwarder()
	r.wt.Register(fwd)
	call := func() localchan.Response {
		return fwd.Forward(r.ctx, &localchan.Call{Request: localchan.Request{Cmd: "ask", Args: localchan.TextArgs("Revisor"), Flags: map[string]any{"raw": `y\n`}},
			Caller: localchan.Caller{Identified: true, AgentID: dev.ID}})
	}
	resp := call()
	if resp.OK || resp.Error.Code != "permission_prompt" || resp.ExitCode() != 3 {
		t.Fatalf("ask --raw = %+v", resp)
	}
	// even when the Citadel still thinks the target is free, the runtime refuses
	if _, err := r.gdb.Writer().Exec(`UPDATE characters SET state = 'idle', reason = '' WHERE id = ?`, rev.ID); err != nil {
		t.Fatal(err)
	}
	resp = call()
	if resp.OK || resp.Error.Code != "permission_prompt" || resp.ExitCode() != 3 {
		t.Fatalf("ask --raw with a stale registry = %+v", resp)
	}
	if typed := r.machineOf(rev).host.proc(rev.ID).typedAll(); len(typed) != 0 {
		t.Fatalf("typed = %q: nothing is typed in a permission prompt", typed)
	}
}
