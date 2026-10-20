package telegram_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/users"
)

func routingWorld(t *testing.T) *world {
	t.Helper()
	return newWorld(t, rigOpts{routing: true})
}

// botSays makes the bot send a message about a workspace and a mesa to the user
// and returns its id in the chat.
func (w *world) botSays(u users.User, tg faketelegram.User, workspace, mesa, text string) int64 {
	w.t.Helper()
	if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: text, Workspace: workspace, Mesa: mesa, Origin: "motor"}); err != nil {
		w.t.Fatal(err)
	}
	w.drain()
	var id int64
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == tg.ID && strings.Contains(s.Text, text) {
			id = s.MessageID
		}
	}
	if id == 0 {
		w.t.Fatalf("the message %q was not sent", text)
	}
	return id
}

func (w *world) reply(tg faketelegram.User, text string, to int64) {
	w.t.Helper()
	if _, err := w.fake.UserRepliesText(tg, faketelegram.Private(tg), text, to); err != nil {
		w.t.Fatal(err)
	}
	w.poll()
	w.drain()
}

// activate makes a workspace the active one of the user, through /workspace.
func (w *world) activate(u users.User, tg faketelegram.User, ws string) {
	w.t.Helper()
	w.say(tg, "/workspace")
	w.drain()
	msg, keys := w.buttons(tg)
	for label, key := range keys {
		if strings.TrimPrefix(label, "● ") == ws {
			w.click(tg, msg, key)
			return
		}
	}
	w.t.Fatalf("no button for %s: %v", ws, keys)
}

// CA-ROTA-01
func TestAReplyToAMessageOfAnotherWorkspaceGoesThereAndKeepsTheActiveOne(t *testing.T) {
	t.Parallel()
	w := routingWorld(t)
	u, tg := w.user("Ana", users.Administrator, "loja", "blog")
	w.activate(u, tg, "loja")
	id := w.botSays(u, tg, "blog", "", "O build do blog passou")
	w.reply(tg, "ótimo, siga", id)
	got := w.delivered.all()
	if len(got) != 1 || got[0].Workspace != "blog" || got[0].Text != "ótimo, siga" || got[0].ReplyToMessage != id {
		t.Fatalf("delivered %+v", got)
	}
	if a, _ := w.bot.Store().Active(w.ctx, u.ID); a != "loja" {
		t.Fatalf("the active workspace became %q", a)
	}
	// and the next free text still goes to the active one
	w.say(tg, "outra coisa")
	w.drain()
	if got := w.delivered.all(); len(got) != 2 || got[1].Workspace != "loja" {
		t.Fatalf("delivered %+v", got)
	}
}

// CA-ROTA-02
func TestTheShortcutGoesToAnotherWorkspaceWithoutTheNameAndKeepsTheActiveOne(t *testing.T) {
	t.Parallel()
	w := routingWorld(t)
	u, tg := w.user("Ana", users.Administrator, "loja", "blog")
	w.activate(u, tg, "loja")
	w.say(tg, "#blog corrige o build")
	w.drain()
	got := w.delivered.all()
	if len(got) != 1 || got[0].Workspace != "blog" || got[0].Text != "corrige o build" || !got[0].ShortcutUsed {
		t.Fatalf("delivered %+v", got)
	}
	if a, _ := w.bot.Store().Active(w.ctx, u.ID); a != "loja" {
		t.Fatalf("the active workspace became %q", a)
	}
	// the name has to be followed by a message
	w.say(tg, "#blog")
	w.drain()
	if len(w.delivered.all()) != 1 || !strings.Contains(lastText(w, tg), "Escreva a mensagem") {
		t.Fatalf("%q", lastText(w, tg))
	}
}

// CA-ROTA-03
func TestAWorkspaceTheUserDoesNotActInIsRefusedWithTheAllowedOnes(t *testing.T) {
	t.Parallel()
	w := routingWorld(t)
	_, tg := w.user("Bia", users.Approver, "loja", "blog")
	for _, text := range []string{"#shop oi", "#naoexiste oi"} {
		w.say(tg, text)
		w.drain()
		got := lastText(w, tg)
		if !strings.Contains(got, "Você não atua em #") || !strings.Contains(got, "#blog, #loja") {
			t.Fatalf("%q: %q", text, got)
		}
	}
	if n := len(w.delivered.all()); n != 0 {
		t.Fatalf("%d messages were queued", n)
	}
	// nor does a reply to a message about a workspace he does not act in
	id := w.botSays(mustUser(t, w, "Bia"), tg, "shop", "", "aviso do shop")
	w.reply(tg, "e aí?", id)
	if !strings.Contains(lastText(w, tg), "Você não atua em #shop") || len(w.delivered.all()) != 0 {
		t.Fatalf("%q", lastText(w, tg))
	}
}

// CA-ROTA-04
func TestAUserOfTwoWorkspacesWithoutAnActiveOneIsAskedAndTheChoiceBecomesTheActiveOne(t *testing.T) {
	t.Parallel()
	w := routingWorld(t)
	u, tg := w.user("Ana", users.Administrator, "loja", "blog")
	w.say(tg, "faz o deploy")
	w.drain()
	if n := len(w.delivered.all()); n != 0 {
		t.Fatalf("delivered before the choice: %d", n)
	}
	msg, keys := w.buttons(tg)
	if keys["loja"] == "" || keys["blog"] == "" {
		t.Fatalf("buttons %v", keys)
	}
	w.click(tg, msg, keys["blog"])
	got := w.delivered.all()
	if len(got) != 1 || got[0].Workspace != "blog" || got[0].Text != "faz o deploy" {
		t.Fatalf("delivered %+v", got)
	}
	if a, _ := w.bot.Store().Active(w.ctx, u.ID); a != "blog" {
		t.Fatalf("active %q", a)
	}
	// from now on it goes there, with no question
	w.say(tg, "e agora?")
	w.drain()
	if got := w.delivered.all(); len(got) != 2 || got[1].Workspace != "blog" {
		t.Fatalf("%+v", got)
	}
	// who acts in one workspace only never chooses
	_, one := w.user("Caio", users.Approver, "loja")
	w.say(one, "oi")
	w.drain()
	if got := w.delivered.all(); len(got) != 3 || got[2].Workspace != "loja" || got[2].User.Name != "Caio" {
		t.Fatalf("%+v", got)
	}
	// /workspace marks the current one
	w.say(tg, "/workspace")
	w.drain()
	if _, keys := w.buttons(tg); keys["● blog"] == "" || keys["loja"] == "" {
		t.Fatalf("buttons %v", keys)
	}
}

// A button of the choice is checked again against the workspaces the user has now.
func TestTheChoiceIsCheckedAgainstTheWorkspacesOfNow(t *testing.T) {
	t.Parallel()
	w := routingWorld(t)
	u, tg := w.user("Ana", users.Administrator, "loja", "blog")
	w.say(tg, "faz o deploy")
	w.drain()
	msg, keys := w.buttons(tg)
	short := []string{"loja"}
	if _, err := w.users.Edit(w.ctx, w.owner, u.ID, users.Edit{Workspaces: &short}); err != nil {
		t.Fatal(err)
	}
	w.click(tg, msg, keys["blog"])
	if n := len(w.delivered.all()); n != 0 {
		t.Fatalf("delivered to a workspace the user lost: %d", n)
	}
	if a, _ := w.bot.Store().Active(w.ctx, u.ID); a != "" {
		t.Fatalf("active %q", a)
	}
}

// CA-ROTA-05
func TestAReplyAboutAMesaCarriesTheMesaInTheHeaderAndInTheOrigin(t *testing.T) {
	t.Parallel()
	w := routingWorld(t)
	u, tg := w.user("Ana", users.Administrator, "loja")
	id := w.botSays(u, tg, "loja", "42-checkout", "O PR da mesa abriu")
	w.reply(tg, "pode mesclar quando passar", id)
	got := w.delivered.all()
	if len(got) != 1 || got[0].Mesa != "42-checkout" {
		t.Fatalf("delivered %+v", got)
	}
	n := telegram.NewItem(got[0], "gm")
	if n.Origin.Table != "42-checkout" || n.Origin.Channel != inbox.ChannelTelegram || n.Origin.User != u.ID || n.Origin.Message == "" || n.Origin.Conversation == "" || n.Origin.RepliedTo == "" {
		t.Fatalf("origin %+v", n.Origin)
	}
	item := inbox.Item{ID: "m7", Workspace: n.Workspace, Kind: n.Kind, Sender: n.Sender, Text: n.Text, Origin: n.Origin}
	want := "[Mestre · user · de Ana (Administrador) · via Telegram · mesa 42-checkout · m7]"
	if item.Header() != want {
		t.Fatalf("header %q, want %q", item.Header(), want)
	}
	if n.Text != "pode mesclar quando passar" {
		t.Fatalf("the text was changed: %q", n.Text)
	}
}

// CA-ROTA-06
func TestEveryMessageToAUserStartsWithItsHeader(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{approvals: true})
	u, tg := w.user("Ana", users.Approver, "loja")
	for _, c := range []struct {
		ws, mesa, want string
	}{
		{"loja", "", "<b>loja</b>\n"},
		{"loja", "42-checkout", "<b>loja · Mesa #42 checkout</b>\n"},
		{"", "", "<b>Sistema</b>\n"},
	} {
		if _, err := w.bot.Send(w.ctx, telegram.Out{ChatID: tg.ID, UserID: u.ID, Text: "corpo", Workspace: c.ws, Mesa: c.mesa, Origin: "motor"}); err != nil {
			t.Fatal(err)
		}
		w.drain()
		var last string
		for _, s := range w.sent("sendMessage") {
			last = s.Text
		}
		if !strings.HasPrefix(last, c.want+"corpo") {
			t.Errorf("%q: %q", c.want, last)
		}
	}
	// the question of an approval and its edit carry the header of the request
	w.ask(9, classes.RequestExtraCost, classes.LevelApprover, false)
	for _, s := range w.sent("sendMessage") {
		if strings.Contains(s.Text, "corpo") {
			continue
		}
		if !strings.HasPrefix(s.Text, "<b>loja</b>\n") {
			t.Errorf("the question of an approval starts with %q", s.Text)
		}
	}
	msg, keys := w.buttons(tg)
	w.click(tg, msg, keys["Aprovar agora"])
	var edited string
	for _, s := range w.sent("editMessageText") {
		edited = s.Text
	}
	if !strings.HasPrefix(edited, "<b>loja</b>\n") {
		t.Errorf("the edit starts with %q", edited)
	}
	// a reply to who is no user says nothing about the Mestre
	w.say(faketelegram.User{ID: 999, FirstName: "X"}, "oi")
	w.drain()
}

// CA-ROTA-07
func TestATextThatAnswersASystemMessageIsAnsweredByTheEngineNotByAnAgent(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{routing: true, lockdown: true})
	_, tg := w.user("Ana", users.Administrator, "loja")
	w.say(tg, "/travar")
	w.drain()
	var system int64
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == tg.ID && strings.Contains(s.Text, "Travar o Mestre?") {
			system = s.MessageID
		}
	}
	if system == 0 {
		t.Fatal("no Sistema message")
	}
	if !strings.HasPrefix(w.sent("sendMessage")[len(w.sent("sendMessage"))-1].Text, "<b>Sistema</b>") {
		t.Fatalf("the message is not a Sistema one")
	}
	w.reply(tg, "sim, trava", system)
	got := lastText(w, tg)
	if !strings.Contains(got, "Confirmar travamento") || !strings.Contains(got, "botões") {
		t.Fatalf("the engine answered %q", got)
	}
	if n := len(w.delivered.all()); n != 0 {
		t.Fatalf("%d messages reached the Grand Masters", n)
	}
	if w.lock.Locked(w.ctx) {
		t.Fatal("a text locked the Mestre")
	}
}

// A reply to a message older than 180 days is routed by the other rules.
func TestTheMemoryOfTheMessagesOfTheBotLasts180Days(t *testing.T) {
	t.Parallel()
	w := routingWorld(t)
	u, tg := w.user("Ana", users.Administrator, "loja", "blog")
	w.activate(u, tg, "loja")
	id := w.botSays(u, tg, "blog", "", "aviso antigo")
	w.clk.Advance(telegram.MessageLife + time.Hour)
	w.reply(tg, "e isso?", id)
	if got := w.delivered.all(); len(got) != 1 || got[0].Workspace != "loja" {
		t.Fatalf("an old reply went to %+v; want the active workspace", got)
	}
}
