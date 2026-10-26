package chain_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/localchan"

	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/users"
)

func (w *world) approveAsOwner(id int64) {
	w.t.Helper()
	if _, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: "u1", Level: string(users.Owner), Answer: chain.AnswerApprove}); err != nil {
		w.t.Fatal(err)
	}
}

func destroy(who string, version string) chain.DestructiveAsk {
	return chain.DestructiveAsk{Requester: who, Command: "note delete", Object: "loja/42-login/p0003", Version: version, About: `"Plano" (p0003)`}
}

// CA-APROV-FLUXO-10
func TestAnApprovedDestructiveActionIsSpentOnceAndOnlyForTheStateThatWasApproved(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	ok, id, err := w.ch.Destructive(w.ctx, destroy(dev.ID, "4"))
	if err != nil || ok || id == 0 {
		t.Fatalf("first ask = %v, %d, %v; want a request and no go-ahead", ok, id, err)
	}
	if r := w.request(id); r.Type != "acao_destrutiva" || r.Requester != dev.ID || r.Impression == "" {
		t.Fatalf("the request = %+v", r)
	}
	if ok, again, _ := w.ch.Destructive(w.ctx, destroy(dev.ID, "4")); ok || again != id {
		t.Fatalf("asking twice = %v, %d, want the same request %d", ok, again, id)
	}
	// nobody else spends it
	other := w.recruit("QA")
	if ok, oid, _ := w.ch.Destructive(w.ctx, destroy(other.ID, "4")); ok || oid == id {
		t.Fatalf("another character got %v, %d", ok, oid)
	}
	w.approveAsOwner(id)
	// the object changed after the approval: it is over, and a new question is open
	ok, changed, err := w.ch.Destructive(w.ctx, destroy(dev.ID, "5"))
	if err != nil || ok || changed == id {
		t.Fatalf("revision 5 = %v, %d, %v", ok, changed, err)
	}
	if got := w.request(id); got.State != chain.Invalidated {
		t.Fatalf("the approval of revision 4 is %s, want invalidated", got.State)
	}
	// approved again for the state it is in, spent once
	w.approveAsOwner(changed)
	if ok, used, err := w.ch.Destructive(w.ctx, destroy(dev.ID, "5")); err != nil || !ok || used != changed {
		t.Fatalf("with the approval = %v, %d, %v", ok, used, err)
	}
	if ok, next, _ := w.ch.Destructive(w.ctx, destroy(dev.ID, "5")); ok || next == changed {
		t.Fatalf("the approval was spent twice: %v, %d", ok, next)
	}
}

func TestAnApprovedDestructiveActionNotUsedInTimeIsNotSpent(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	_, id, _ := w.ch.Destructive(w.ctx, destroy(dev.ID, "4"))
	w.approveAsOwner(id)
	w.clk.Advance(24*time.Hour + time.Second)
	if ok, _, err := w.ch.Destructive(w.ctx, destroy(dev.ID, "4")); err != nil || ok {
		t.Fatalf("a late use = %v, %v", ok, err)
	}
}

func TestADeniedDestructiveActionIsNeverGrantedAndAskingAgainOpensAnotherQuestion(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	dev := w.recruit("Dev")
	_, id, _ := w.ch.Destructive(w.ctx, destroy(dev.ID, "4"))
	if _, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: "u1", Level: string(users.Owner), Answer: chain.AnswerDeny}); err != nil {
		t.Fatal(err)
	}
	if ok, again, _ := w.ch.Destructive(w.ctx, destroy(dev.ID, "4")); ok || again == id {
		t.Fatalf("after a denial = %v, %d", ok, again)
	}
	if _, _, err := w.ch.Destructive(w.ctx, chain.DestructiveAsk{Command: "note delete"}); err == nil {
		t.Error("an ask with no requester was taken")
	}
}

func (w *world) cmd(c string, by string, args ...string) localchan.Response {
	w.t.Helper()
	return w.fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{Cmd: c, Args: localchan.TextArgs(args...)}, Caller: localchan.Caller{Identified: true, AgentID: by}})
}

// CA-CLI-CONV-06
// Part of CA-APROV-POL-08 (the answer of the Grand Master is the CLI of the chain): with the chain of approval in place, deleting
// a scroll by a character opens a request of type acao_destrutiva and deletes
// nothing; after the people approve, the same command by the same character
// deletes it, once.
func TestANoteDeleteByACharacterWaitsForTheApprovalOfThePeopleAndThenRuns(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{destructive: true})
	dev := w.recruit("Dev")
	if r := w.cmd("note create", dev.ID, "# Plano\nfazer"); !r.OK {
		t.Fatalf("create: %+v", r.Error)
	}
	r := w.cmd("note delete", dev.ID, "Plano")
	if r.OK || r.Error == nil || r.Error.Code != "approval_required" || r.ExitCode() != localchan.ExitPending {
		t.Fatalf("the first delete: %+v", r)
	}
	id, _ := r.Error.Details["request"].(int64)
	if id == 0 {
		t.Fatalf("the answer names no request: %+v", r.Error.Details)
	}
	if q := w.request(id); q.Type != "acao_destrutiva" || q.Requester != dev.ID {
		t.Fatalf("the request = %+v", q)
	}
	if r := w.cmd("note read", dev.ID, "Plano"); !r.OK {
		t.Fatal("the scroll was deleted before the approval")
	}
	w.approveAsOwner(id)
	if r := w.cmd("note delete", dev.ID, "Plano"); !r.OK {
		t.Fatalf("the delete after the approval: %+v", r.Error)
	}
	if r := w.cmd("note read", dev.ID, "Plano"); r.OK {
		t.Fatal("the scroll is still there")
	}
}

// CA-APROV-FLUXO-10 (the real scroll: its revision moves after the approval)
func TestAScrollThatChangesAfterTheApprovalOfItsDeletionIsInvalidatedAndNothingIsDeleted(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{destructive: true})
	dev := w.recruit("Dev")
	if r := w.cmd("note create", dev.ID, "# Plano\nfazer"); !r.OK {
		t.Fatalf("create: %+v", r.Error)
	}
	first := w.cmd("note delete", dev.ID, "Plano")
	id, _ := first.Error.Details["request"].(int64)
	if first.OK || id == 0 {
		t.Fatalf("the first delete: %+v", first)
	}
	w.approveAsOwner(id)
	// the scroll moves to another revision before the deletion is applied
	if r := w.cmd("note write", dev.ID, "Plano", "# Plano\nfazer mais"); !r.OK {
		t.Fatalf("write: %+v", r.Error)
	}
	again := w.cmd("note delete", dev.ID, "Plano")
	next, _ := again.Error.Details["request"].(int64)
	if again.OK || again.Error == nil || again.Error.Code != "approval_required" || next == 0 || next == id {
		t.Fatalf("delete of the changed scroll = %+v, want approval_required with a new request (was #%d)", again, id)
	}
	if got := w.request(id); got.State != chain.Invalidated {
		t.Fatalf("the approval of the old revision is %s, want invalidated", got.State)
	}
	if r := w.cmd("note read", dev.ID, "Plano"); !r.OK {
		t.Fatalf("the scroll was deleted: %+v", r.Error)
	}
}

// A ritual is deleted the same way: the character is told it waits, and the
// approved deletion runs once.
func TestARitualDeleteByTheMasterWaitsForTheApprovalOfThePeopleAndThenRuns(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{destructive: true})
	dev := w.recruit("Dev")
	create := w.fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{Cmd: "ritual create", Args: localchan.TextArgs("Diario"),
		Flags: map[string]any{"target": dev.Name, "prompt": "oi", "daily": "09:00"}}, Caller: localchan.Caller{Identified: true, AgentID: w.master.ID}})
	if !create.OK {
		t.Fatalf("create: %+v", create.Error)
	}
	r := w.cmd("ritual delete", w.master.ID, "Diario")
	if r.OK || r.Error == nil || r.Error.Code != "approval_required" {
		t.Fatalf("the first delete: %+v", r)
	}
	id, _ := r.Error.Details["request"].(int64)
	if id == 0 {
		t.Fatalf("no request in %+v", r.Error.Details)
	}
	if r := w.cmd("ritual show", w.master.ID, "Diario"); !r.OK {
		t.Fatalf("the ritual was deleted before the approval: %+v", r.Error)
	}
	w.approveAsOwner(id)
	if r := w.cmd("ritual delete", w.master.ID, "Diario"); !r.OK {
		t.Fatalf("the delete after the approval: %+v", r.Error)
	}
	if r := w.cmd("ritual show", w.master.ID, "Diario"); r.OK {
		t.Fatal("the ritual is still there")
	}
}

// The table, as in CA-CLI-CONV-06: the cancellation waits for the people, and the
// approval is for the head of the branch it was asked about.
func TestTheCancellationOfATableWaitsForTheApprovalOfThePeopleAndThenRuns(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{destructive: true})
	cancel := func() localchan.Response {
		return w.fwd.Forward(w.ctx, &localchan.Call{Request: localchan.Request{Cmd: "table close", Args: localchan.TextArgs(w.table.ID),
			Flags: map[string]any{"cancel": true, "reason": "mudou o plano"}}, Caller: localchan.Caller{Identified: true, AgentID: w.gm.ID}})
	}
	r := cancel()
	if r.OK || r.Error == nil || r.ExitCode() != localchan.ExitPending {
		t.Fatalf("the first cancel: %+v %+v", r, r.Error)
	}
	if got, err := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID); err != nil || got.State == "cancelled" || got.State == "cancelling" {
		t.Fatalf("the table was cancelled before the approval: %+v, %v (response %+v)", got.State, err, r.Error)
	}
	var id int64
	for _, q := range mustOpen(t, w) {
		id = q
	}
	if id == 0 {
		t.Fatal("no request was opened for the cancellation")
	}
	w.approveAsOwner(id)
	cancel() // accepted: the table ends at the next safe point ("scheduled")
	if got, err := w.orc.Tables.Get(w.ctx, w.slug, w.table.ID); err != nil || (got.State != "cancelling" && got.State != "cancelled") {
		t.Fatalf("the table after the approval: %v, %v", got.State, err)
	}
}

// mustOpen lists the open requests of the destructive type (the Grand Master has
// no table, so they are found by number).
func mustOpen(t *testing.T, w *world) []int64 {
	t.Helper()
	var out []int64
	for id := int64(1); id < 50; id++ {
		if r, err := w.ch.Get(w.ctx, id); err == nil && r.Type == "acao_destrutiva" && r.State == chain.Open {
			out = append(out, id)
		}
	}
	return out
}

// CA-APROV-POL-08: a destructive action without an exception opens a request of
// type acao_destrutiva with the command and the object as its target, asked only
// of the Administrators and the Owner (an Approver is not asked), and the answer
// of the Grand Master to it is human_required.
func TestADestructiveRequestHasTheCommandAsTargetAsksOnlyAdministratorsAndIsNotTheGrandMasters(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	ana, err := w.usvc.Create(w.ctx, w.owner, users.NewUser{Name: "Ana", Level: users.Approver, Workspaces: []string{w.slug}})
	if err != nil {
		t.Fatal(err)
	}
	bia, err := w.usvc.Create(w.ctx, w.owner, users.NewUser{Name: "Bia", Level: users.Administrator, Workspaces: []string{w.slug}})
	if err != nil {
		t.Fatal(err)
	}
	dev := w.recruit("Dev")
	ok, id, err := w.ch.Destructive(w.ctx, destroy(dev.ID, "4"))
	if err != nil || ok || id == 0 {
		t.Fatalf("ask = %v, %d, %v", ok, id, err)
	}
	r := w.request(id)
	if r.Type != "acao_destrutiva" {
		t.Fatalf("the request is of type %q, want acao_destrutiva", r.Type)
	}
	var tg chain.DestructiveTarget
	if err := json.Unmarshal(r.Target, &tg); err != nil || tg.Command != "note delete" || tg.Object != "loja/42-login/p0003" {
		t.Fatalf("target = %s (%v); want the command and the scroll", r.Target, err)
	}
	// it goes to the people at once, and the Grand Master has no say
	got := w.cli(w.gm, "approval decide", map[string]any{"reason": "ok"}, itoa(id), "approve")
	if got.OK || got.Code != "human_required" || got.Exit != 3 {
		t.Fatalf("decide by the Grand Master = %+v, want exit 3 human_required", got)
	}
	if w.request(id).State != chain.Open {
		t.Fatalf("the request is %s after the refusal", w.request(id).State)
	}
	if _, _, asked := w.chn.To(ana.ID); asked {
		t.Error("an Approver was asked about a destructive action")
	}
	if _, _, asked := w.chn.To(bia.ID); !asked {
		t.Error("the Administrator was not asked")
	}
	if _, _, asked := w.chn.To(w.owner.ID); !asked {
		t.Error("the Owner was not asked")
	}
}
