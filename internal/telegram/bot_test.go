package telegram_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/internal/users"
)

// CA-BOT-01
func TestAnUpdateRecordedBeforeARestartIsProcessedOnceAndARepeatedIdIsIgnored(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, tg := w.user("Ana", users.Approver)
	// the update is recorded and the process stops before it is processed
	body, _ := json.Marshal(telegram.Update{UpdateID: 500, Message: &telegram.Message{MessageID: 9, From: &telegram.User{ID: tg.ID}, Chat: telegram.Chat{ID: tg.ID, Type: "private"}, Text: "oi"}})
	if fresh, err := w.bot.Store().Record(w.ctx, 500, body); err != nil || !fresh {
		t.Fatalf("record = %v, %v", fresh, err)
	}
	// the new process reads the pending update once
	restarted, err := telegram.New(w.opts)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.PollOnce(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.router.count() != 1 {
		t.Fatalf("delivered %d times, want 1", w.router.count())
	}
	// the same update_id arrives again
	if fresh, err := w.bot.Store().Record(w.ctx, 500, body); err != nil || fresh {
		t.Fatalf("a repeated id was recorded again: %v, %v", fresh, err)
	}
	if err := restarted.PollOnce(w.ctx); err != nil {
		t.Fatal(err)
	}
	if w.router.count() != 1 {
		t.Fatalf("delivered %d times after the repetition, want 1", w.router.count())
	}
	if off, _ := w.bot.Store().Offset(w.ctx); off != 501 {
		t.Fatalf("offset = %d, want 501", off)
	}
}

// CA-BOT-02
func TestAQuickCommandIsAnsweredByTheEngineWithoutTheInbox(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{commands: []telegram.Command{{Name: "status", Description: "estado", Capability: users.Status,
		Run: func(context.Context, telegram.Call) (string, error) { return "Cidadela online", nil }}}})
	_, tg := w.user("Ana", users.Approver)
	// the Grand Master is busy: an ordinary message is in his inbox and he has not
	// taken it (the fake router only records what is delivered, never consumes it)
	w.say(tg, "faça a revisão do módulo")
	if w.router.count() != 1 {
		t.Fatalf("the ordinary message was delivered %d times, want 1 (queued for the busy Grand Master)", w.router.count())
	}
	w.say(tg, "/status")
	w.flush()
	got := w.sent("sendMessage")
	if len(got) != 1 || !strings.Contains(got[0].Text, "Cidadela online") || w.router.count() != 1 {
		t.Fatalf("sent %+v, delivered %d; want the engine's answer and nothing more in the inbox", got, w.router.count())
	}
}

// CA-BOT-03
func TestALongAnswerGoesAsASummaryAndTheWholeTextAsAFile(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	u, tg := w.user("Ana", users.Approver)
	long := strings.Repeat("linha de texto do Grão-Mestre\n\n", 700) // ~22.000 characters
	if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: long}); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		w.flush()
		w.clk.Advance(time.Second)
	}
	msgs, docs := w.sent("sendMessage"), w.sent("sendDocument")
	if len(msgs) != 1 || len(docs) != 1 || len([]rune(msgs[0].Text)) > telegram.MaxMessage {
		t.Fatalf("messages %d, documents %d; want a summary and a file", len(msgs), len(docs))
	}
	if sum := msgs[0].Text; sum == "" || !strings.Contains(sum, "linha de texto do Grão-Mestre") || len([]rune(sum)) >= len([]rune(long)) {
		t.Fatalf("summary = %q, want a non-empty text derived from the answer and shorter than it", sum)
	}
	f, ok := docs[0].Params["document"].(faketelegram.File)
	if !ok || !strings.HasSuffix(f.Name, ".md") || string(f.Content) != "Sistema\n\n"+long {
		t.Fatalf("document = %+v, want the whole text in a .md file", docs[0].Params["document"])
	}
}

// CA-BOT-04
func TestAButtonOfAnotherUserIsRefusedAndNothingRuns(t *testing.T) {
	t.Parallel()
	ran := 0
	w := newWorld(t, rigOpts{actions: map[string]telegram.Action{"approve": func(context.Context, telegram.Click) (string, string, error) {
		ran++
		return "feito", "Aprovado.", nil
	}}})
	a, ta := w.user("Ana", users.Approver)
	_, tb := w.user("Bia", users.Approver)
	if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: ta.ID, UserID: a.ID, Text: "Aprovar o PR 12?", Buttons: [][]telegram.Button{{{Text: "Aprovar", Action: "approve", Arg: "12", Capability: users.DecideApprovals}}}}); err != nil {
		t.Fatal(err)
	}
	w.flush()
	sent := w.sent("sendMessage")[0]
	kb := sent.Params["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)
	key := kb["callback_data"].(string)
	if len(key) > 64 {
		t.Fatalf("callback_data has %d bytes", len(key))
	}
	// Bia clicks Ana's button
	if _, err := w.fake.UserClicks(tb, sent.MessageID, key); err != nil {
		t.Fatal(err)
	}
	w.poll()
	if ran != 0 {
		t.Fatal("the action ran for another user")
	}
	if ans := w.sent("answerCallbackQuery"); len(ans) != 1 || !strings.Contains(ans[0].Text, "não é seu") {
		t.Fatalf("answers = %+v", ans)
	}
	// Ana clicks: it runs once; a second click finds it answered
	for range 2 {
		if _, err := w.fake.UserClicks(ta, sent.MessageID, key); err != nil {
			t.Fatal(err)
		}
		w.poll()
	}
	if ran != 1 {
		t.Fatalf("the action ran %d times, want 1", ran)
	}
	if ans := w.sent("answerCallbackQuery"); len(ans) != 3 || !strings.Contains(ans[2].Text, "Já respondido por Ana") {
		t.Fatalf("answers = %+v", ans)
	}
	if edits := w.sent("editMessageText"); len(edits) != 1 || !strings.Contains(edits[0].Text, "Aprovado") {
		t.Fatalf("the message was not edited with the result: %+v", edits)
	}
}

// CA-BOT-05
func TestA429HoldsTheQueueForItsRetryAfter(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	u, tg := w.user("Ana", users.Approver)
	if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: "oi"}); err != nil {
		t.Fatal(err)
	}
	w.fake.FailNext("sendMessage", faketelegram.Failure{Status: 429, RetryAfter: 7})
	start := w.clk.Now()
	if next := w.flush(); !next.Equal(start.Add(7 * time.Second)) {
		t.Fatalf("next = %v, want %v", next, start.Add(7*time.Second))
	}
	w.clk.Advance(6 * time.Second)
	w.flush()
	if len(w.sent("sendMessage")) != 0 {
		t.Fatal("it sent before the retry_after")
	}
	w.clk.Advance(time.Second)
	w.flush()
	if len(w.sent("sendMessage")) != 1 {
		t.Fatal("it did not send after the retry_after")
	}
}

// CA-BOT-06
func TestATermIsExplainedOnlyTheFirstTime(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{terms: true})
	u, tg := w.user("Ana", users.Approver)
	for _, text := range []string{"A Party da mesa está pronta.", "Outra Party chegou."} {
		if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: text}); err != nil {
			t.Fatal(err)
		}
		w.flush()
		w.clk.Advance(time.Second)
	}
	got := w.sent("sendMessage")
	if len(got) != 2 || !strings.Contains(got[0].Text, "Party (O time de personagens") || strings.Contains(got[1].Text, "Party (") {
		t.Fatalf("messages = %+v", got)
	}
}

// CA-BOT-07
func TestAMessageStaysInTheQueueWhileTheTelegramIsDown(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	u, tg := w.user("Ana", users.Approver)
	if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: "resposta"}); err != nil {
		t.Fatal(err)
	}
	w.fake.FailNext("sendMessage", faketelegram.Failure{Status: 502})
	if _, err := w.bot.Flush(w.ctx); err == nil {
		t.Fatal("a 502 was not reported")
	}
	if q, _, _, _ := w.bot.OutboxState(w.ctx); q != 1 || len(w.sent("sendMessage")) != 0 {
		t.Fatalf("queued %d, sent %d; want the message kept", q, len(w.sent("sendMessage")))
	}
	w.clk.Advance(time.Minute)
	w.flush()
	if q, s, _, _ := w.bot.OutboxState(w.ctx); q != 0 || s != 1 || len(w.sent("sendMessage")) != 1 {
		t.Fatalf("queued %d, sent %d after the Telegram came back", q, s)
	}
}

// CA-BOT-08
func TestAStickerIsRefusedWithTheAcceptedTypesAndNothingIsQueued(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, tg := w.user("Ana", users.Approver)
	if _, err := w.fake.UserSendsSticker(tg); err != nil {
		t.Fatal(err)
	}
	w.poll()
	w.flush()
	got := w.sent("sendMessage")
	if len(got) != 1 || !strings.Contains(got[0].Text, "texto, voz, áudio, foto e documento") || w.router.count() != 0 {
		t.Fatalf("sent %+v, delivered %d", got, w.router.count())
	}
}

// CA-BOT-09
func TestTheAnswerToASecondFactorIsTakenByTheEngineAndOnlyBythatUser(t *testing.T) {
	t.Parallel()
	var used []string
	w := newWorld(t, rigOpts{captures: map[string]telegram.CaptureFunc{"factor": func(_ context.Context, _ users.User, ref, text string) (string, bool, error) {
		used = append(used, ref+":"+text)
		return "", false, nil
	}}})
	ana, ta := w.user("Ana", users.Approver)
	_, tb := w.user("Bia", users.Approver)
	if err := w.bot.Ask(w.ctx, ana.ID, "factor", "login-github", "Código de github.com", time.Hour); err != nil {
		t.Fatal(err)
	}
	w.say(ta, "uma conversa antes de o pedido chegar") // the prompt is still in the queue
	if w.router.count() != 1 {
		t.Fatal("an ordinary message was swallowed before the user saw the prompt")
	}
	w.flush()
	w.say(tb, "uma conversa da Bia")
	w.say(ta, "482913")
	if len(used) != 1 || used[0] != "login-github:482913" {
		t.Fatalf("the engine used %v", used)
	}
	if w.router.count() != 2 || w.router.got[1].User.Name != "Bia" {
		t.Fatalf("delivered %+v, want only the ordinary messages", w.router.got)
	}
	if len(w.fake.Deleted()) != 2 {
		t.Fatalf("deleted %+v, want the answer and the prompt", w.fake.Deleted())
	}
}

// CA-BOT-14
func TestOnlyTheLongestTermIsExplainedAndACommonWordNeedsACapital(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{terms: true})
	u, tg := w.user("Ana", users.Approver)
	if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: "o Mestre da mesa vigia a cidadela suspensa"}); err != nil {
		t.Fatal(err)
	}
	w.flush()
	text := w.sent("sendMessage")[0].Text
	if !strings.Contains(text, "Mestre da mesa (") || !strings.Contains(text, "cidadela suspensa (") || strings.Contains(text, "vigia (") ||
		strings.Contains(text, "Mestre (") || strings.Contains(text, "cidadela (") {
		t.Fatalf("text = %q", text)
	}
	// only those two terms are explained: with them removed, no other term
	// (nor the shorter ones inside them) is followed by an explanation
	rest := strings.ReplaceAll(strings.ReplaceAll(text, "Mestre da mesa (", ""), "cidadela suspensa (", "")
	if strings.Contains(rest, " (") || strings.Contains(rest, "mesa (") || strings.Contains(rest, "suspensa (") {
		t.Fatalf("another term is explained in %q", text)
	}
	if n := strings.Count(text, "("); n != 2 {
		t.Fatalf("text has %d explanations, want exactly 2: %q", n, text)
	}
}

// CA-BOT-15
func TestTheMarkdownBecomesSafeHTMLAPlainFallbackAndOnePerSecond(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	u, tg := w.user("Ana", users.Approver)
	for _, text := range []string{"**negrito** e o literal <b>", "```\nsem fechar"} {
		if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: text}); err != nil {
			t.Fatal(err)
		}
		w.flush()
		w.clk.Advance(time.Second)
	}
	got := w.sent("sendMessage")
	if got[0].Text != "<b>Sistema</b>\n<b>negrito</b> e o literal &lt;b&gt;" || got[0].Params["parse_mode"] != "HTML" {
		t.Fatalf("html = %q (%v)", got[0].Text, got[0].Params["parse_mode"])
	}
	if got[1].Text != "Sistema\n```\nsem fechar" || got[1].Params["parse_mode"] != nil {
		t.Fatalf("fallback = %q (%v), want plain text", got[1].Text, got[1].Params["parse_mode"])
	}
	// 31 messages in the same second leave one per second
	for i := range 31 {
		if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: fmt.Sprintf("m%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	before := len(w.sent("sendMessage"))
	for i := range 31 {
		w.flush()
		if n := len(w.sent("sendMessage")) - before; n != i+1 {
			t.Fatalf("after %d s, %d messages left; want %d", i, n, i+1)
		}
		w.clk.Advance(time.Second)
	}
}

// CA-USR-01
func TestAnObserverTalkingFreelyGetsTheAllowedCommandsAndNothingGoesToTheInbox(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{commands: []telegram.Command{
		{Name: "status", Description: "estado", Capability: users.Status, Run: func(context.Context, telegram.Call) (string, error) { return "ok", nil }},
		{Name: "travar", Description: "emergência", Capability: users.Lock, Run: func(context.Context, telegram.Call) (string, error) { return "travado", nil }},
	}})
	_, tg := w.user("Otto", users.Observer)
	w.say(tg, "faça o deploy agora")
	w.flush()
	got := w.sent("sendMessage")
	if w.router.count() != 0 || len(got) != 1 || !strings.Contains(got[0].Text, "/status") || strings.Contains(got[0].Text, "/travar") {
		t.Fatalf("delivered %d, sent %+v; want the allowed commands only", w.router.count(), got)
	}
	// a command above the level is refused with the level it needs
	w.clk.Advance(time.Second)
	w.say(tg, "/travar")
	w.flush()
	if got := w.sent("sendMessage"); len(got) != 2 || !strings.Contains(got[1].Text, "Administrador") {
		t.Fatalf("sent %+v", got)
	}
}

// CA-USR-06
func TestThePINAnswerIsDeletedNeverKeptAndNeverInTheInbox(t *testing.T) {
	t.Parallel()
	var done []error
	w := newWorld(t)
	var logs bytes.Buffer
	w.opts.Logger = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w.opts.Captures = map[string]telegram.CaptureFunc{telegram.PINKind: nil}
	w.opts.Captures[telegram.PINKind] = telegram.PINCapture(w.users, func(_ context.Context, _ users.User, _ string, err error) { done = append(done, err) })
	var err error
	if w.bot, err = telegram.New(w.opts); err != nil {
		t.Fatal(err)
	}
	owner, err := w.users.Get(w.ctx, w.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.users.SetPIN(w.ctx, owner.ID, "482913"); err != nil {
		t.Fatal(err)
	}
	// the Owner pairs his Telegram
	c, err := w.users.NewCode(w.ctx, w.owner, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	tg := faketelegram.User{ID: 77, FirstName: "Lucas", Username: "lucas"}
	if _, err := w.users.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: tg.ID, Private: true}); err != nil {
		t.Fatal(err)
	}
	req, err := w.users.StartPIN(w.ctx, owner.ID, "unlock")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.bot.Ask(w.ctx, owner.ID, telegram.PINKind, req.ID, "Digite o seu PIN.", 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	w.flush()
	prompts := w.sent("sendMessage")
	if len(prompts) == 0 {
		t.Fatal("the PIN prompt was not sent")
	}
	promptID := prompts[len(prompts)-1].MessageID
	if _, err := w.fake.UserSendsText(tg, faketelegram.Private(tg), "482913"); err != nil {
		t.Fatal(err)
	}
	// the fake numbers the messages of the bot and of the users in one sequence,
	// so the message with the PIN is the one right after the prompt
	pinMsgID := promptID + 1
	// the update is recorded without the PIN
	raw, err := w.bot.Store().DB.Reader().QueryContext(w.ctx, `SELECT body FROM telegram_updates`)
	if err != nil {
		t.Fatal(err)
	}
	raw.Close()
	w.poll()
	if len(done) != 1 || done[0] != nil {
		t.Fatalf("PIN check = %v, want right", done)
	}
	wantDeleted := []faketelegram.Deleted{{ChatID: tg.ID, MessageID: pinMsgID}, {ChatID: tg.ID, MessageID: promptID}}
	deleted := w.fake.Deleted()
	if w.router.count() != 0 || len(deleted) != 2 || !slices.Contains(deleted, wantDeleted[0]) || !slices.Contains(deleted, wantDeleted[1]) {
		t.Fatalf("delivered %d, deleted %+v; want nothing delivered and exactly %+v (the PIN and the prompt) deleted", w.router.count(), deleted, wantDeleted)
	}
	var bodies int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM telegram_updates WHERE body LIKE '%482913%'`).Scan(&bodies); err != nil || bodies != 0 {
		t.Fatalf("the PIN is kept in %d updates (%v)", bodies, err)
	}
	if strings.Contains(logs.String(), "482913") {
		t.Fatalf("the PIN is in the log: %s", logs.String())
	}
	for _, s := range w.fake.Sent() {
		if strings.Contains(s.Text, "482913") {
			t.Fatalf("the PIN went back to the Telegram: %+v", s)
		}
	}
}

func TestAStrangerIsIgnoredAGroupIsLeftAndAPairingCodeWorks(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	stranger := faketelegram.User{ID: 999, FirstName: "Intruso", Username: "intruso"}
	w.say(stranger, "oi")
	w.flush()
	if len(w.fake.Sent()) != 0 || len(w.alerts.unauthorized) != 1 || w.router.count() != 0 {
		t.Fatalf("sent %+v, alerts %v", w.fake.Sent(), w.alerts.unauthorized)
	}
	// a group: the bot leaves and the Owner is told
	ana, ta := w.user("Ana", users.Approver)
	_ = ana
	group := faketelegram.Chat{ID: -100, Type: "group", Title: "g"}
	if _, err := w.fake.UserSendsText(ta, group, "oi pessoal"); err != nil {
		t.Fatal(err)
	}
	w.poll()
	if len(w.sent("leaveChat")) != 1 || len(w.alerts.owner) < 1 || !strings.Contains(w.alerts.owner[len(w.alerts.owner)-1], "não é privado") || w.router.count() != 0 {
		t.Fatalf("left %d, alerts %v", len(w.sent("leaveChat")), w.alerts.owner)
	}
	// a pairing code from an unknown id pairs; a wrong one is told
	nu, err := w.users.Create(w.ctx, w.owner, users.NewUser{Name: "Caio", Level: users.Approver})
	if err != nil {
		t.Fatal(err)
	}
	code, err := w.users.NewCode(w.ctx, w.owner, nu.ID)
	if err != nil {
		t.Fatal(err)
	}
	caio := faketelegram.User{ID: 4242, FirstName: "Caio", Username: "caio"}
	w.say(caio, "/start "+code.Code)
	w.flush()
	if got := w.sent("sendMessage"); len(got) != 1 || !strings.Contains(got[0].Text, "Pareado, Caio") {
		t.Fatalf("sent %+v", got)
	}
	if _, err := w.users.ByTelegram(w.ctx, caio.ID); err != nil {
		t.Fatal(err)
	}
}

// CA-USR-09
func TestTheSixthWrongPairingCodeInAnHourGetsNoAnswerAtAllNotEvenWithTheRightCode(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	stranger := faketelegram.User{ID: 999, FirstName: "Intruso", Username: "intruso"}
	for i := range users.PairingFailures {
		w.say(stranger, "AAAAAAA"+string(users.CodeAlphabet[i]))
		w.flush()
		w.clk.Advance(time.Second)
		if got := w.sent("sendMessage"); len(got) != i+1 || !strings.Contains(got[i].Text, "inválido") {
			t.Fatalf("after wrong code %d the bot sent %+v, want one refusal each", i+1, got)
		}
	}
	// the sixth, even a right one: silence, no message to the sender
	nu, err := w.users.Create(w.ctx, w.owner, users.NewUser{Name: "Caio", Level: users.Approver})
	if err != nil {
		t.Fatal(err)
	}
	code, err := w.users.NewCode(w.ctx, w.owner, nu.ID)
	if err != nil {
		t.Fatal(err)
	}
	sentBefore := len(w.fake.Sent())
	w.say(stranger, "/start "+code.Code)
	w.flush()
	w.clk.Advance(time.Minute)
	w.flush()
	if got := w.fake.Sent(); len(got) != sentBefore {
		t.Fatalf("the sixth attempt got an answer: %+v", got[sentBefore:])
	}
	if _, err := w.users.ByTelegram(w.ctx, stranger.ID); !errors.Is(err, users.ErrNoUser) {
		t.Errorf("the silenced sender was paired: %v", err)
	}
}

// The PIN prompt stays open for another try after a wrong PIN: the second
// answer is captured too, never delivered, and a second prompt is refused.
func TestAWrongPINKeepsThePromptOpenAndTheRetryIsCapturedToo(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.opts.Captures = map[string]telegram.CaptureFunc{telegram.PINKind: telegram.PINCapture(w.users, nil)}
	var err error
	if w.bot, err = telegram.New(w.opts); err != nil {
		t.Fatal(err)
	}
	if err := w.users.SetPIN(w.ctx, w.owner.ID, "482913"); err != nil {
		t.Fatal(err)
	}
	c, _ := w.users.NewCode(w.ctx, w.owner, w.owner.ID)
	tg := faketelegram.User{ID: 77, FirstName: "Lucas", Username: "lucas"}
	if _, err := w.users.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: tg.ID, Private: true}); err != nil {
		t.Fatal(err)
	}
	req, _ := w.users.StartPIN(w.ctx, w.owner.ID, "unlock")
	if err := w.bot.Ask(w.ctx, w.owner.ID, telegram.PINKind, req.ID, "Digite o PIN.", 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := w.bot.Ask(w.ctx, w.owner.ID, telegram.PINKind, "outro", "Outro PIN.", 5*time.Minute); !errors.Is(err, telegram.ErrCaptureBusy) {
		t.Fatalf("a second prompt = %v, want busy", err)
	}
	w.flush()
	w.say(tg, "000000")
	w.say(tg, "482913")
	if w.router.count() != 0 {
		t.Fatalf("a PIN went to the inbox: %+v", w.router.got)
	}
	var kept int
	_ = w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM telegram_updates WHERE body LIKE '%482913%' OR body LIKE '%000000%'`).Scan(&kept)
	if kept != 0 {
		t.Fatalf("a PIN is kept in %d updates", kept)
	}
	for range 3 {
		w.flush()
		w.clk.Advance(time.Second)
	}
	got := w.sent("sendMessage")
	if len(got) < 3 || !strings.Contains(got[1].Text, "incorreto") || !strings.Contains(got[2].Text, "confirmado") {
		t.Fatalf("sent %+v", got)
	}
	// the wait is over: an ordinary message goes through again
	w.clk.Advance(time.Second)
	w.say(tg, "oi")
	if w.router.count() != 1 {
		t.Fatal("the message after the PIN did not go to the Grão-Mestre")
	}
}

// An answer after the prompt expired is not an ordinary message either.
func TestAnAnswerAfterThePromptExpiredIsDeletedAndNotDelivered(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{captures: map[string]telegram.CaptureFunc{"factor": func(context.Context, users.User, string, string) (string, bool, error) { return "", false, nil }}})
	a, ta := w.user("Ana", users.Approver)
	if err := w.bot.Ask(w.ctx, a.ID, "factor", "r", "Código", time.Minute); err != nil {
		t.Fatal(err)
	}
	w.flush()
	w.clk.Advance(2 * time.Minute)
	w.say(ta, "482913")
	w.flush()
	if w.router.count() != 0 || len(w.fake.Deleted()) < 1 {
		t.Fatalf("delivered %d, deleted %+v", w.router.count(), w.fake.Deleted())
	}
	if got := w.sent("sendMessage"); !strings.Contains(got[len(got)-1].Text, "expirou") {
		t.Fatalf("sent %+v", got)
	}
}

// A recorded answer is kept without its text, and a copy without text is not replayed.
func TestTheRecordedCopyOfACapturedAnswerHasNoTextAndIsNotReplayed(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{captures: map[string]telegram.CaptureFunc{"factor": func(context.Context, users.User, string, string) (string, bool, error) { return "", false, nil }}})
	a, ta := w.user("Ana", users.Approver)
	if err := w.bot.Ask(w.ctx, a.ID, "factor", "r", "Código", time.Hour); err != nil {
		t.Fatal(err)
	}
	w.flush()
	// the Gate closes right after the update is recorded: it stays pending
	g := &stopAfter{n: 1}
	w.opts.Gate = g
	var err error
	if w.bot, err = telegram.New(w.opts); err != nil {
		t.Fatal(err)
	}
	if _, err := w.fake.UserSendsText(ta, faketelegram.Private(ta), "482913"); err != nil {
		t.Fatal(err)
	}
	w.poll()
	var body string
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT body FROM telegram_updates WHERE state = 'new'`).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "482913") || !strings.Contains(body, `"redacted":true`) {
		t.Fatalf("stored copy = %s", body)
	}
	g.n = 100 // the Citadel acts again: the copy without text is dropped, not handled
	w.poll()
	if w.router.count() != 0 {
		t.Fatal("the stored copy was replayed")
	}
}

type stopAfter struct{ n int }

func (g *stopAfter) Allowed(context.Context) bool { g.n--; return g.n >= 0 }

// A 400 that says the HTML did not parse is sent once more as plain text; a 401
// holds the whole queue and tells the Owner; a quiet week drops the offset.
func TestTheQueueSurvivesAParseErrorARevokedTokenAndAQuietWeek(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	u, tg := w.user("Ana", users.Approver)
	if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: "**oi**"}); err != nil {
		t.Fatal(err)
	}
	w.fake.FailNext("sendMessage", faketelegram.Failure{Status: 400, Description: "Bad Request: can't parse entities"})
	w.flush()
	if got := w.sent("sendMessage"); len(got) != 1 || got[0].Text != "Sistema\n**oi**" || got[0].Params["parse_mode"] != nil {
		t.Fatalf("sent %+v, want the plain text", got)
	}
	w.clk.Advance(time.Second)
	if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: "segunda"}); err != nil {
		t.Fatal(err)
	}
	w.fake.FailNext("sendMessage", faketelegram.Failure{Status: 401})
	if _, err := w.bot.Flush(w.ctx); err == nil {
		t.Fatal("a 401 was not reported")
	}
	if q, _, f, _ := w.bot.OutboxState(w.ctx); q != 1 || f != 0 || len(w.alerts.owner) == 0 {
		t.Fatalf("queued %d, failed %d, alerts %v; want the message kept and the Owner told", q, f, w.alerts.owner)
	}
	// a quiet week: no offset
	body, _ := json.Marshal(telegram.Update{UpdateID: 900})
	if _, err := w.bot.Store().Record(w.ctx, 900, body); err != nil {
		t.Fatal(err)
	}
	if off, _ := w.bot.Store().Offset(w.ctx); off != 901 {
		t.Fatalf("offset = %d, want 901", off)
	}
	w.clk.Advance(6 * 24 * time.Hour)
	if off, _ := w.bot.Store().Offset(w.ctx); off != 0 {
		t.Fatalf("offset after a quiet week = %d, want none", off)
	}
}
