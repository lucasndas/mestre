package chain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/quests"
	"github.com/mestre-ia/mestre/internal/users"
)

func (w *world) entry() int64 {
	w.t.Helper()
	id, err := w.ch.RequestSystem(w.ctx, chain.SystemRequest{Type: classes.RequestGuildEntry, About: `Entrada na Guilda: a máquina "notebook"`,
		Target: json.RawMessage(`{"join_id":"j1","code":"123 456"}`)})
	if err != nil {
		w.t.Fatal(err)
	}
	return id
}

// CA-APROV-POL-12: the request to enter the Guild is the engine's own: no
// workspace, no agent in front, only the Owner is asked, with two answers, and the
// PIN after the button; not even a delegation of the Owner lets the Grand Master
// decide it, and a policy file that cites promocao_cidadela is invalid.
func TestARequestToEnterTheGuildGoesOnlyToTheOwnerWhoApprovesOrDeniesWithThePIN(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	ana, err := w.usvc.Create(w.ctx, w.owner, users.NewUser{Name: "Ana", Level: users.Administrator, Workspaces: []string{w.slug}})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.usvc.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	id := w.entry()
	r := w.request(id)
	if r.State != chain.Open || r.Level.Kind != chain.LevelPeople || r.Workspace != "" || r.Requester != "" || r.Risk.Risk != classes.RiskFixed {
		t.Fatalf("the request = %+v", r)
	}
	if _, _, ok := w.chn.To(ana.ID); ok {
		t.Error("an Administrator was asked about the entry in the Guild")
	}
	_, msg, ok := w.chn.To(w.owner.ID)
	if !ok {
		t.Fatal("the Owner was not asked")
	}
	if len(msg.Answers) != 2 || msg.Answers[0] != chain.AnswerApprove || msg.Answers[1] != chain.AnswerDeny {
		t.Errorf("the Owner was offered %v, want only approve and deny", msg.Answers)
	}
	if !strings.Contains(i18n.Render(i18n.PtBR, msg.Msg), "123 456") && !strings.Contains(i18n.Render(i18n.PtBR, msg.Msg), "notebook") {
		t.Errorf("the question does not say what is asked: %q", i18n.Render(i18n.PtBR, msg.Msg))
	}
	untouched := func(when string) {
		t.Helper()
		if q := w.request(id); q.State != chain.Open || q.Decision != nil {
			t.Fatalf("%s the request is %s with decision %+v; nothing may change", when, q.State, q.Decision)
		}
	}
	// a delegation of the Owner does not give it to the Grand Master
	w.deleg.set(true)
	if ref := w.refusal(chain.Decision{Request: id, By: w.gm.ID, Answer: chain.AnswerApprove}); ref.Code != chain.CodeHumanRequired {
		t.Errorf("the Grand Master deciding the entry: %+v", ref)
	}
	untouched("after the refusal of the Grand Master")
	if _, _, err := chain.ParsePolicyFile([]byte("versao: 1\ntipos:\n  promocao_cidadela: { sozinho: nunca }\n")); err == nil {
		t.Error("a policy file that cites promocao_cidadela was accepted")
	}
	// nobody else answers it, and the Owner answers nothing else
	if _, err := w.flow.Answer(w.ctx, ana.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove}); !errors.Is(err, approvals.ErrNotYours) {
		t.Errorf("an Administrator answering: %v", err)
	}
	untouched("after the Administrator tapped Approve")
	for _, a := range []chain.Answer{chain.AnswerApproveAlways, chain.AnswerApprovePattern, chain.AnswerDelegate} {
		if rep, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: id, Answer: a, Pattern: "x *"}); !errors.Is(err, approvals.ErrLevel) || rep.PIN != "" {
			t.Errorf("%s: %+v, %v, want it refused before the PIN", a, rep, err)
		}
	}
	// the chain refuses it too, whoever calls
	for _, a := range []chain.Answer{chain.AnswerApproveAlways, chain.AnswerDelegate, chain.AnswerAskBack, chain.AnswerClimb, chain.AnswerApproveFor} {
		if _, err := w.ch.DecideByPerson(w.ctx, chain.PersonDecision{Request: id, User: w.owner.ID, Level: string(users.Owner), Answer: a, Pattern: "x *"}); err == nil {
			t.Errorf("the chain applied %s to an entry", a)
		}
	}
	rep, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove})
	if err != nil || rep.PIN == "" {
		t.Fatalf("the button: %+v, %v", rep, err)
	}
	untouched("after the Owner tapped Approve without the PIN")
	if got := w.entries.results(); len(got) != 0 {
		t.Fatalf("the button alone told the owner of the type: %+v", got)
	}
	if err := w.usvc.CheckPIN(w.ctx, rep.PIN, "123456"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.flow.ConfirmPIN(w.ctx, w.owner.ID, rep.PIN); err != nil {
		t.Fatal(err)
	}
	got := w.entries.results()
	if len(got) != 1 || !got[0].Approved || got[0].Request.ID != id || got[0].Request.Decision == nil || got[0].Request.Decision.By != "user:"+w.owner.ID ||
		!strings.Contains(string(got[0].Request.Target), "j1") {
		t.Fatalf("the owner of the type was told %+v", got)
	}
}

func TestADeniedEntryAsksNoPINAndTellsTheOwnerOfTheType(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id := w.entry()
	rep, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerDeny})
	if err != nil || rep.PIN != "" {
		t.Fatalf("deny: %+v, %v", rep, err)
	}
	got := w.entries.results()
	if len(got) != 1 || got[0].Approved || got[0].Request.State != chain.Denied {
		t.Fatalf("told %+v", got)
	}
}

// The entry waits the hour of the entry, whatever the file of the policy says,
// and then it is over: never approved on its own.
func TestAnEntryExpiresInTheHourOfTheEntry(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id := w.entry()
	w.clk.Advance(chain.GuildEntryValidity - time.Second)
	if w.request(id).State != chain.Open {
		t.Fatal("it expired before the hour")
	}
	w.clk.Advance(time.Second)
	if w.request(id).State != chain.Expired {
		t.Fatalf("state = %s, want expired", w.request(id).State)
	}
	if got := w.entries.results(); len(got) != 1 || got[0].Approved || got[0].Request.State != chain.Expired {
		t.Fatalf("told %+v", got)
	}
}

// The hour of the entry ends it twice: the deadline of the chain and the
// expiry of the entry in the Citadel, which takes the question down. When the
// Citadel gets there first, the request is still expired, not cancelled.
func TestAnEntryTakenDownAtTheEndOfItsHourIsExpiredWhicheverEndsItFirst(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id := w.entry()
	w.clk.SkipWall(chain.GuildEntryValidity) // the hour is over and the deadline has not fired yet
	if err := w.ch.Withdraw(w.ctx, id, "acabou"); err != nil {
		t.Fatal(err)
	}
	if w.request(id).State != chain.Expired {
		t.Fatalf("state = %s, want expired", w.request(id).State)
	}
}

func TestAnEntryTakenDownIsCancelledAndOnlyTheTypesOfTheEngineAreOpenedThisWay(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id := w.entry()
	if err := w.ch.Withdraw(w.ctx, id, "acabou"); err != nil {
		t.Fatal(err)
	}
	if w.request(id).State != chain.Cancelled {
		t.Fatalf("state = %s, want cancelled", w.request(id).State)
	}
	if err := w.ch.Withdraw(w.ctx, id, "de novo"); err != nil {
		t.Errorf("taking down a closed request: %v", err)
	}
	if _, err := w.flow.Answer(w.ctx, w.owner.ID, approvals.Input{Request: id, Answer: chain.AnswerApprove}); !errors.Is(err, approvals.ErrNotOpen) {
		t.Errorf("an answer after it was taken down: %v", err)
	}
	for _, typ := range []classes.RequestType{classes.RequestClassPermission, classes.RequestPR, classes.RequestOperator, classes.RequestDestructive, ""} {
		if _, err := w.ch.RequestSystem(w.ctx, chain.SystemRequest{Type: typ}); !errors.Is(err, chain.ErrNotSystem) {
			t.Errorf("type %q opened as a request of the engine: %v", typ, err)
		}
	}
}

// Taking a question down never cancels a request of another kind.
func TestWithdrawingAnEntryCannotCancelAnotherKindOfRequest(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	id := w.shell(w.recruit("Dev"), "npm install lodash")
	if err := w.ch.Withdraw(w.ctx, id, "x"); !errors.Is(err, chain.ErrNotSystem) {
		t.Fatalf("Withdraw of a permission request = %v", err)
	}
	if w.request(id).State != chain.Open {
		t.Fatal("the permission request was cancelled")
	}
}

// CA-MSG-08: a line the engine worded for the people is a message kept with the
// request, and the card says it in the language of who reads, with the text of
// the machine (and of the agent) the same in both.
func TestTheLineOfARequestOfTheEngineIsTranslatedForEachReader(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	about := i18n.M("approval.about_guild_entry", "code", "123 456", "name", "notebook", "os", "linux", "host", "nb", "instance", "i1", "ip", "10.0.0.2")
	id, err := w.ch.RequestSystem(w.ctx, chain.SystemRequest{Type: classes.RequestGuildEntry, AboutMsg: about, Target: json.RawMessage(`{"join_id":"j1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	r := w.request(id) // read back from the database: the message survived the storage
	if !strings.Contains(r.About, "Entry into the Guild") {
		t.Errorf("the text kept for agents and logs = %q", r.About)
	}
	card := chain.Card(r, r.Level, []chain.Answer{chain.AnswerApprove, chain.AnswerDeny}).Msg()
	en, pt := i18n.Render("en", card), i18n.Render(i18n.PtBR, card)
	if !strings.Contains(en, "Entry into the Guild. Code to check") || !strings.Contains(pt, "Entrada na Guilda. Código para conferir") {
		t.Errorf("en = %q\npt = %q", en, pt)
	}
	for _, s := range []string{en, pt} {
		if !strings.Contains(s, `"notebook"`) || !strings.Contains(s, "123 456") {
			t.Errorf("the text of the machine changed: %q", s)
		}
	}
	if r.AboutIn("en") != i18n.Render("en", about) || r.AboutIn(i18n.PtBR) != i18n.Render(i18n.PtBR, about) {
		t.Errorf("AboutIn = %q / %q", r.AboutIn("en"), r.AboutIn(i18n.PtBR))
	}
}

// The other requests the engine words (a mission point, a destructive command) are
// messages too; what an agent wrote in them is one line of text, the same in both languages.
func TestTheLineOfAMissionPointAndOfADestructiveCommandIsTranslatedForEachReader(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{flow: true})
	dev := w.recruit("Dev")
	id, err := w.ch.OpenMissionPoint(w.ctx, quests.MissionPoint{Workspace: w.slug, Table: w.table.ID, Quest: "q1", QuestName: "Login\nRisco: baixo", Step: "q1/a", StepID: "a", StepName: "etapa",
		When: quests.ApprovalAfter, Character: dev.ID, Designer: w.master.ID, Summary: "feito"})
	if err != nil {
		t.Fatal(err)
	}
	r := w.request(id)
	card := chain.Card(r, r.Level, nil).Msg()
	en, pt := i18n.Render("en", card), i18n.Render(i18n.PtBR, card)
	if !strings.Contains(en, "About: approval point") || !strings.Contains(pt, "Sobre: ponto de aprovação") {
		t.Errorf("en = %q\npt = %q", en, pt)
	}
	for _, s := range []string{en, pt} {
		if strings.Contains(s, "\nRisco: baixo\n") {
			t.Errorf("the text of the agent opened a line of the engine: %q", s)
		}
	}
	ok, did, err := w.ch.Destructive(w.ctx, chain.DestructiveAsk{Requester: dev.ID, Command: "table close --cancel", Object: "loja/t1", Version: "1",
		AboutMsg: i18n.M("approval.about_table_cancel", "table", "t1")})
	if err != nil || ok {
		t.Fatalf("destructive: %v, %v", ok, err)
	}
	d := w.request(did)
	if got := d.AboutIn("en"); got != "table close --cancel: cancel the table t1" {
		t.Errorf("en = %q", got)
	}
	if got := d.AboutIn(i18n.PtBR); got != "table close --cancel: cancelar a mesa t1" {
		t.Errorf("pt = %q", got)
	}
}
