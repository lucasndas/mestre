package telegram_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/approvals"
	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/users"
)

var allAnswers = []chain.Answer{chain.AnswerApprove, chain.AnswerApproveFor, chain.AnswerApproveAlways, chain.AnswerDeny, chain.AnswerDenyInstruction, chain.AnswerDenyAlways, chain.AnswerAskBack}

// ask puts a request of the type in front of the people.
func (w *world) ask(id int64, typ classes.RequestType, min classes.Level, pin bool) chain.Request {
	w.t.Helper()
	r := chain.Request{ID: id, Workspace: "loja", Type: typ, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople}}
	if err := w.chain.Add(w.ctx, r); err != nil {
		w.t.Fatal(err)
	}
	q := chain.PeopleQuestion{Request: r, Text: "Posso rodar npm install lodash?", Approvals: 1, MinLevel: min, PIN: pin, Answers: allAnswers}
	if err := w.flow.Ask(w.ctx, q); err != nil {
		w.t.Fatal(err)
	}
	w.flush()
	return r
}

// buttons are the callback keys of the last message with buttons the user got,
// by the text of the button.
func (w *world) buttons(u faketelegram.User) (msg int64, keys map[string]string) {
	w.t.Helper()
	keys = map[string]string{}
	for _, s := range w.sent("sendMessage") {
		kb, ok := s.Params["reply_markup"].(map[string]any)
		if s.ChatID != u.ID || !ok || kb["inline_keyboard"] == nil {
			continue
		}
		msg, keys = s.MessageID, map[string]string{}
		for _, row := range kb["inline_keyboard"].([]any) {
			for _, b := range row.([]any) {
				bt := b.(map[string]any)
				keys[bt["text"].(string)] = bt["callback_data"].(string)
			}
		}
	}
	return msg, keys
}

func (w *world) click(u faketelegram.User, msg int64, key string) {
	w.t.Helper()
	if _, err := w.fake.UserClicks(u, msg, key); err != nil {
		w.t.Fatal(err)
	}
	w.poll()
	w.drain()
}

// drain sends what is queued; the bot sends one message per second to a chat.
func (w *world) drain() {
	w.t.Helper()
	for range 3 {
		w.clk.Advance(1100 * time.Millisecond)
		w.flush()
	}
}

// lastEdit is the last text the bot put in a message of the user.
func (w *world) lastEdit(u faketelegram.User) string {
	text := ""
	for _, s := range w.sent("editMessageText") {
		if s.ChatID == u.ID {
			text = s.Text
			if head, rest, ok := strings.Cut(text, "\n"); ok && strings.HasPrefix(head, "<b>") {
				text = rest // the header is checked on its own (CA-ROTA-06)
			}
		}
	}
	return text
}

func TestTheQuestionReachesEachUserWithTheButtonsOfTheirLevel(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	_, ana := w.user("Ana", users.Approver, "loja")
	_, adm := w.user("Adm", users.Administrator, "loja")
	_, otto := w.user("Otto", users.Observer, "loja")
	_, ivo := w.user("Ivo", users.Approver, "blog")
	w.ask(1, classes.RequestExtraCost, classes.LevelApprover, false)

	for _, m := range w.sent("sendMessage") {
		if m.ChatID == ana.ID && !strings.Contains(m.Text, "npm install lodash") {
			t.Errorf("the question = %q", m.Text)
		}
	}
	_, a := w.buttons(ana)
	want := []string{"Aprovar agora", "Negar agora", "Negar com instrução", "Perguntar de volta"}
	if len(a) != len(want) {
		t.Errorf("Ana's buttons = %v, want %v", a, want)
	}
	for _, b := range want {
		if a[b] == "" {
			t.Errorf("Ana has no %q", b)
		}
	}
	_, d := w.buttons(adm)
	for _, b := range []string{"Aprovar por um tempo", "Aprovar sempre", "Negar sempre"} {
		if d[b] == "" {
			t.Errorf("the Administrator has no %q", b)
		}
	}
	if _, o := w.buttons(otto); len(o) != 0 {
		t.Errorf("the Observer got buttons: %v", o)
	}
	if _, i := w.buttons(ivo); len(i) != 0 {
		t.Errorf("a user of another workspace got buttons: %v", i)
	}
}

// Part of CA-APROV-FLUXO-02: through the Telegram, the first click decides and
// the other recipients read who answered.
func TestAClickDecidesAndTheOtherMessagesAreEdited(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	_, ana := w.user("Ana", users.Approver, "loja")
	_, adm := w.user("Adm", users.Administrator, "loja")
	r := w.ask(2, classes.RequestExtraCost, classes.LevelApprover, false)
	msg, keys := w.buttons(ana)
	w.click(ana, msg, keys["Aprovar agora"])
	if w.chain.State(r.ID) != chain.Approved {
		t.Fatalf("state = %s", w.chain.State(r.ID))
	}
	if got := w.lastEdit(adm); !strings.HasPrefix(got, "Já respondido por Ana: Aprovado às 12:00") {
		t.Errorf("the message of the Administrator = %q", got)
	}
	if got := w.lastEdit(ana); !strings.HasPrefix(got, "Você respondeu: Aprovado") {
		t.Errorf("the message of Ana = %q", got)
	}
	// the Administrator's button of the same request answers "already answered"
	_, dk := w.buttons(adm)
	w.click(adm, 1, dk["Negar agora"])
	if w.chain.State(r.ID) != chain.Approved || len(w.chain.Decisions()) != 1 {
		t.Error("a click after the decision changed it")
	}
}

// Part of CA-APROV-POL-11: the PIN is asked after the button, and nothing is
// applied without it.
func TestAnApprovalThatAsksThePINWaitsForItInTheChat(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	if err := w.users.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	c, err := w.users.NewCode(w.ctx, w.owner, w.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	owner := faketelegram.User{ID: 77, FirstName: "Lucas", Username: "lucas"}
	if _, err := w.users.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: owner.ID, Private: true}); err != nil {
		t.Fatal(err)
	}
	r := w.ask(3, classes.RequestGuildEntry, classes.LevelOwner, true)
	msg, keys := w.buttons(owner)
	w.click(owner, msg, keys["Aprovar agora"])
	if w.chain.State(r.ID) != chain.Open {
		t.Fatal("the button alone approved")
	}
	prompts := len(w.sent("sendMessage"))
	w.say(owner, "000000") // wrong
	if w.chain.State(r.ID) != chain.Open {
		t.Fatal("a wrong PIN approved")
	}
	w.drain()
	if got := w.sent("sendMessage"); len(got) <= prompts || !strings.Contains(got[len(got)-1].Text, "PIN incorreto") {
		t.Fatalf("the user was not told the PIN is wrong: %+v", got[len(got)-1:])
	}
	w.say(owner, "123456")
	w.drain()
	if w.chain.State(r.ID) != chain.Approved {
		t.Fatalf("with the PIN: %s", w.chain.State(r.ID))
	}
	for _, s := range w.sent("sendMessage") {
		if strings.Contains(s.Text, "Não apliquei") {
			t.Errorf("a wrong PIN tried to apply the approval: %q", s.Text)
		}
	}
	// the PIN never stays in the chat
	if len(w.fake.Deleted()) < 2 {
		t.Errorf("the messages with the PIN were not deleted: %v", w.fake.Deleted())
	}
	for _, s := range w.fake.Sent() {
		if strings.Contains(s.Text, "123456") || strings.Contains(s.Text, "000000") {
			t.Errorf("a PIN went out in a message: %q", s.Text)
		}
	}
}

func TestADenialWithInstructionTakesTheTextOfTheUser(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	_, ana := w.user("Ana", users.Approver, "loja")
	r := w.ask(4, classes.RequestExtraCost, classes.LevelApprover, false)
	msg, keys := w.buttons(ana)
	w.click(ana, msg, keys["Negar com instrução"])
	if w.chain.State(r.ID) != chain.Open {
		t.Fatal("the button decided before the text")
	}
	w.say(ana, "use pnpm")
	w.drain()
	if w.chain.State(r.ID) != chain.Denied {
		t.Fatalf("state = %s", w.chain.State(r.ID))
	}
	d := w.chain.Decisions()
	if len(d) != 1 || d[0].Instruction != "use pnpm" || d[0].Answer != chain.AnswerDenyInstruction {
		t.Errorf("decisions = %+v", d)
	}
	for _, in := range w.router.got {
		t.Errorf("the text went to the Grand Master: %+v", in)
	}
}

func TestABadTimeIsAskedAgainAndAGoodOneDecides(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	admU, adm := w.user("Adm", users.Administrator, "loja")
	if err := w.users.SetPIN(w.ctx, admU.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	r := w.ask(5, classes.RequestExtraCost, classes.LevelApprover, false)
	msg, keys := w.buttons(adm)
	w.click(adm, msg, keys["Aprovar por um tempo"])
	w.say(adm, "uma eternidade") // not a time
	w.drain()
	if w.chain.State(r.ID) != chain.Open || len(w.chain.Decisions()) != 0 {
		t.Fatal("a bad time decided")
	}
	w.say(adm, "3h")
	w.drain()
	if len(w.chain.Decisions()) != 0 {
		t.Fatal("a rule that frees was applied before the PIN")
	}
	w.say(adm, "123456")
	w.drain()
	d := w.chain.Decisions()
	if len(d) != 1 || d[0].For != 3*time.Hour || d[0].Answer != chain.AnswerApproveFor {
		t.Errorf("decisions = %+v", d)
	}
}

func TestParseValidity(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]time.Duration{"90m": 90 * time.Minute, "3h": 3 * time.Hour, "2d": 48 * time.Hour, " 1D ": 24 * time.Hour} {
		if got, err := telegram.ParseValidity(in); err != nil || got != want {
			t.Errorf("%q = %v, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "0", "-3h", "0d", "999d", "tres horas", "1e9h"} {
		if _, err := telegram.ParseValidity(in); err == nil {
			t.Errorf("%q was accepted", in)
		}
	}
}

func TestAQueuedQuestionIsEditedBeforeItGoesOut(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	_, ana := w.user("Ana", users.Approver, "loja")
	r := chain.Request{ID: 6, Workspace: "loja", Type: classes.RequestExtraCost, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople}}
	if err := w.chain.Add(w.ctx, r); err != nil {
		t.Fatal(err)
	}
	q := chain.PeopleQuestion{Request: r, Text: "Primeira versão", Approvals: 1, MinLevel: classes.LevelApprover, Answers: allAnswers}
	if err := w.flow.Ask(w.ctx, q); err != nil {
		t.Fatal(err)
	}
	q.Text = "Segunda versão"
	if err := w.flow.Ask(w.ctx, q); err != nil {
		t.Fatal(err)
	}
	w.flush()
	var texts []string
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == ana.ID {
			texts = append(texts, s.Text)
		}
	}
	if len(texts) != 1 || !strings.Contains(texts[0], "Segunda versão") {
		t.Errorf("the user got %q", texts)
	}
	if _, keys := w.buttons(ana); keys["Aprovar agora"] == "" {
		t.Error("the edited question lost its buttons")
	}
}

func TestAnAnswerTheChainRefusesGivesTheButtonsBack(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	_, ana := w.user("Ana", users.Approver, "loja")
	r := w.ask(7, classes.RequestExtraCost, classes.LevelApprover, false)
	msg, keys := w.buttons(ana)
	w.click(ana, msg, keys["Negar com instrução"])
	w.chain.FailNext(errors.New("recusado pela cadeia"))
	w.say(ana, "use pnpm")
	w.drain()
	if w.chain.State(r.ID) != chain.Open {
		t.Fatal("the refused answer decided")
	}
	// the message was edited with new buttons, and the one that was used up works again
	var keys2 map[string]string
	for _, e := range w.sent("editMessageText") {
		if kb, ok := e.Params["reply_markup"].(map[string]any); ok && e.ChatID == ana.ID {
			if rows, _ := kb["inline_keyboard"].([]any); len(rows) > 0 {
				keys2 = map[string]string{}
				for _, row := range rows {
					for _, b := range row.([]any) {
						bt := b.(map[string]any)
						keys2[bt["text"].(string)] = bt["callback_data"].(string)
					}
				}
			}
		}
	}
	if keys2["Negar com instrução"] == "" || keys2["Negar com instrução"] == keys["Negar com instrução"] {
		t.Fatalf("the buttons did not come back: %v", keys2)
	}
	w.click(ana, msg, keys2["Negar com instrução"])
	w.say(ana, "use pnpm")
	w.drain()
	if w.chain.State(r.ID) != chain.Denied {
		t.Errorf("state = %s", w.chain.State(r.ID))
	}
}

func TestAButtonAboveTheLevelOfTheUserIsRefusedByTheBot(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	ana, tg := w.user("Ana", users.Approver, "loja")
	r := chain.Request{ID: 8, Workspace: "loja", Type: classes.RequestExtraCost, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople}}
	if err := w.chain.Add(w.ctx, r); err != nil {
		t.Fatal(err)
	}
	// a question that wrongly offers a permanent answer to an Approver
	if _, err := w.ac.Send(w.ctx, approvals.Message{UserID: ana.ID, Request: r, Text: "?", Answers: []chain.Answer{chain.AnswerApproveAlways}}); err != nil {
		t.Fatal(err)
	}
	w.drain()
	msg, keys := w.buttons(tg)
	w.click(tg, msg, keys["Aprovar sempre"])
	if w.chain.State(r.ID) != chain.Open || len(w.chain.Decisions()) != 0 {
		t.Error("the bot let an Approver give a permanent answer")
	}
}

// the PIN wrong three times ends the request of PIN, and the buttons come back
func TestThreeWrongPINsBringTheButtonsBack(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	if err := w.users.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	owner := w.pairOwnerTelegram()
	r := w.ask(9, classes.RequestGuildEntry, classes.LevelOwner, true)
	msg, keys := w.buttons(owner)
	w.click(owner, msg, keys["Aprovar agora"])
	for range 3 {
		w.say(owner, "000000")
		w.drain()
	}
	if w.chain.State(r.ID) != chain.Open {
		t.Fatal("a wrong PIN approved")
	}
	var back map[string]string
	for _, e := range w.sent("editMessageText") {
		if kb, ok := e.Params["reply_markup"].(map[string]any); ok && e.ChatID == owner.ID {
			if rows, _ := kb["inline_keyboard"].([]any); len(rows) > 0 {
				back = map[string]string{}
				for _, row := range rows {
					for _, b := range row.([]any) {
						bt := b.(map[string]any)
						back[bt["text"].(string)] = bt["callback_data"].(string)
					}
				}
			}
		}
	}
	if back["Aprovar agora"] == "" || back["Aprovar agora"] == keys["Aprovar agora"] {
		t.Fatalf("the buttons did not come back: %v", back)
	}
	// the old button is void; the new one asks the PIN again and, with the right one, approves
	w.click(owner, msg, keys["Aprovar agora"])
	if w.chain.State(r.ID) != chain.Open {
		t.Fatal("the spent button worked")
	}
	w.click(owner, msg, back["Aprovar agora"])
	w.say(owner, "123456")
	w.drain()
	if w.chain.State(r.ID) != chain.Approved {
		t.Errorf("state = %s", w.chain.State(r.ID))
	}
}

// an answer that needs a text and the PIN: the text first, then the PIN
func TestATextAnswerThatAsksThePINAsksItAfterTheText(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	if err := w.users.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	owner := w.pairOwnerTelegram()
	r := w.ask(10, classes.RequestWorkspaceClass, classes.LevelOwner, true)
	msg, keys := w.buttons(owner)
	w.click(owner, msg, keys["Aprovar por um tempo"])
	w.say(owner, "3h")
	w.drain()
	if w.chain.State(r.ID) != chain.Open {
		t.Fatal("approved before the PIN")
	}
	w.say(owner, "123456")
	w.drain()
	d := w.chain.Decisions()
	if w.chain.State(r.ID) != chain.Approved || len(d) != 1 || d[0].For != 3*time.Hour {
		t.Errorf("state %s, decisions %+v", w.chain.State(r.ID), d)
	}
}

func TestAnEditedQuestionVoidsTheOldButtons(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	_, ana := w.user("Ana", users.Approver, "loja")
	r := w.ask(11, classes.RequestExtraCost, classes.LevelApprover, false)
	_, old := w.buttons(ana)
	q := chain.PeopleQuestion{Request: r, Text: "Texto novo", Approvals: 1, MinLevel: classes.LevelApprover, Answers: allAnswers}
	if err := w.flow.Ask(w.ctx, q); err != nil {
		t.Fatal(err)
	}
	w.drain()
	w.click(ana, 1, old["Aprovar agora"])
	if w.chain.State(r.ID) != chain.Open {
		t.Error("a button the edit replaced still worked")
	}
}

func TestALongQuestionIsEditedInTheMessageNotInTheFile(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	ana, tg := w.user("Ana", users.Approver, "loja")
	r := chain.Request{ID: 12, Workspace: "loja", Type: classes.RequestExtraCost, State: chain.Open, Level: chain.Level{Kind: chain.LevelPeople}}
	if err := w.chain.Add(w.ctx, r); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("linha de arquivo alterado\n", 600)
	ref, err := w.ac.Send(w.ctx, approvals.Message{UserID: ana.ID, Request: r, Text: long, Answers: []chain.Answer{chain.AnswerApprove}})
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	if err := w.ac.Edit(w.ctx, approvals.MessageEdit{UserID: ana.ID, Ref: ref, Text: "Encerrado.", Request: r.ID}); err != nil {
		t.Fatal(err)
	}
	var msgID int64
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == tg.ID {
			msgID = s.MessageID
		}
	}
	edits := w.sent("editMessageText")
	if len(edits) != 1 || edits[0].MessageID != msgID || edits[0].Text != "<b>loja</b>\nEncerrado." {
		t.Errorf("edits = %+v, want one on message %d", edits, msgID)
	}
}

func TestTheNameOfWhoAnsweredMakesNoLinkInTheMessageOfTheOthers(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	_, ana := w.user("Ana [clique](https://evil.example)", users.Approver, "loja")
	_, adm := w.user("Adm", users.Administrator, "loja")
	w.ask(13, classes.RequestExtraCost, classes.LevelApprover, false)
	msg, keys := w.buttons(ana)
	w.click(ana, msg, keys["Aprovar agora"])
	got := w.lastEdit(adm)
	if !strings.HasPrefix(got, "Já respondido por Ana") || strings.ContainsAny(got, "[]()") {
		t.Errorf("the message = %q", got)
	}
}

func (w *world) pairOwnerTelegram() faketelegram.User {
	w.t.Helper()
	c, err := w.users.NewCode(w.ctx, w.owner, w.owner.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	tg := faketelegram.User{ID: 77, FirstName: "Lucas", Username: "lucas"}
	if _, err := w.users.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: tg.ID, Private: true}); err != nil {
		w.t.Fatal(err)
	}
	return tg
}
