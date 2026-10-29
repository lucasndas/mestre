package app

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
)

// chatWorld is a Citadel whose standard integration is attached (the channels of the inbox, the
// chooser and the panel conversation), with a user who also has a Telegram: what a message of the
// panel must never reach.
type chatWorld struct {
	*ghCompose
	fwd   *localchan.LocalForwarder
	owner users.User
	bia   users.User
	gm    characters.Character
}

const biaChat = 88

func newChatWorld(t *testing.T) *chatWorld {
	t.Helper()
	c := newGHComposeCfg(t, false, true)
	ctx := c.ctx
	w := &chatWorld{ghCompose: c, fwd: localchan.NewLocalForwarder()}
	var err error
	if w.owner, err = c.run.users.CreateOwner(ctx, "Lucas"); err != nil {
		t.Fatal(err)
	}
	if w.bia, err = c.run.users.Create(ctx, w.owner, users.NewUser{Name: "Bia", Level: users.Administrator, Workspaces: []string{c.ws.Slug}}); err != nil {
		t.Fatal(err)
	}
	code, err := c.run.users.NewCode(ctx, w.owner, w.bia.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.run.users.Pair(ctx, users.PairRequest{Text: code.Code, TelegramID: biaChat, Private: true}); err != nil {
		t.Fatal(err)
	}
	if w.bia, err = c.run.users.Get(ctx, w.bia.ID); err != nil {
		t.Fatal(err)
	}
	// the bot is made, not run: what it queues is what the Telegram would get
	if err := c.vault.Put(ctx, TelegramTokenSecret, vault.TypeTelegramBotToken, []byte("123456:fake-bot-token"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run.startBot(ctx); err != nil {
		t.Fatal(err)
	}
	c.orc.Register(w.fwd, nil)
	c.run.Attach(c.orc, w.fwd)
	gm, ok, err := c.orc.Registry.GrandMaster(ctx, c.ws.Slug)
	if err != nil || !ok {
		t.Fatalf("grand master: %v, %v", ok, err)
	}
	w.gm = gm
	return w
}

// forward is a command of the Grand Master through the local channel.
func (w *chatWorld) forward(cmd string, flags map[string]any, args ...string) localchan.Response {
	w.t.Helper()
	if flags == nil {
		flags = map[string]any{}
	}
	return w.fwd.Forward(w.ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: cmd + "-1", AgentID: w.gm.ID, Cmd: cmd, Args: localchan.TextArgs(args...), Flags: flags},
		Caller:  localchan.Caller{Identified: true, AgentID: w.gm.ID},
	})
}

// say sends a message from the panel the way the route does.
func (w *chatWorld) say(u users.User, id, text string, files ...[2]string) panel.ChatSent {
	w.t.Helper()
	d, err := w.run.chat.Begin(w.ctx, u, panel.ChatDraftInput{Workspace: w.ws.Slug, Text: text, ClientID: id, Session: "sessao-1"})
	if err != nil {
		w.t.Fatal(err)
	}
	for _, f := range files {
		if err := d.AddFile(w.ctx, f[0], "application/pdf", strings.NewReader(f[1])); err != nil {
			w.t.Fatal(err)
		}
	}
	sent, err := d.Send(w.ctx)
	if err != nil {
		w.t.Fatal(err)
	}
	return sent
}

func (w *chatWorld) conversation(u users.User) panel.ChatConversation {
	w.t.Helper()
	c, err := w.run.chat.Conversation(w.ctx, u, w.ws.Slug)
	if err != nil {
		w.t.Fatal(err)
	}
	return c
}

// CA-CONV-09 (through the real `mestre choose`): the Grão-Mestre asks the user who wrote through the
// panel; the question is shown in his conversation, with a button per option and not on the
// Telegram; the click delivers the choice once, and a second click is refused.
func TestAChooseQuestionFollowsTheChannelOfTheMessageAndIsAnsweredOnce(t *testing.T) {
	t.Parallel()
	w := newChatWorld(t)
	w.say(w.bia, "msg-choose-1", "qual banco usamos?")
	before := len(w.chatTexts(biaChat))
	r := w.forward("choose", map[string]any{"option": []any{"sqlite", "postgres", "mysql"}, "recommend": "2"}, "Qual banco?")
	if r.Error == nil || r.Error.Code != "pending" {
		t.Fatalf("mestre choose: %+v", r)
	}
	if got := w.chatTexts(biaChat); len(got) != before {
		t.Fatalf("the Telegram got the question of the panel: %q", got[before:])
	}
	c := w.conversation(w.bia)
	var q *panel.ChatEntry
	for i := range c.Entries {
		if c.Entries[i].Question != nil {
			q = &c.Entries[i]
		}
	}
	if q == nil || q.Channel != "panel" || q.Text != "Qual banco?" || strings.Join(q.Question.Options, ",") != "postgres,sqlite,mysql" || q.Question.State != "open" {
		t.Fatalf("the conversation: %+v", c.Entries)
	}
	// only the user asked is asked: the Owner, who also acts here, sees nothing
	if o := w.conversation(w.owner); len(o.Entries) != 0 {
		t.Fatalf("another user sees %+v", o.Entries)
	}
	if err := w.run.chat.Choose(w.ctx, w.bia, q.Question.ID, 2, ""); err != nil {
		t.Fatal(err)
	}
	err := w.run.chat.Choose(w.ctx, w.bia, q.Question.ID, 0, "")
	if api, ok := err.(*panel.APIError); !ok || api.Code != panel.CodeChatChoiceTaken {
		t.Fatalf("a second click: %v", err)
	}
	items, err := (&inbox.Store{DB: w.db}).All(w.ctx, w.gm.ID)
	if err != nil {
		t.Fatal(err)
	}
	var answers []inbox.Item
	for _, it := range items {
		if it.Origin != nil && it.Origin.Choice == q.Question.ID {
			answers = append(answers, it)
		}
	}
	if len(answers) != 1 || answers[0].Origin.Channel != inbox.ChannelPanel || answers[0].Origin.User != w.bia.ID || !strings.Contains(answers[0].Text, `"mysql"`) {
		t.Fatalf("the answers in the inbox of the Grand Master: %+v", answers)
	}

	// a message that came from the Telegram has its question there, never in the panel
	if _, err := w.orc.Inbox.Enqueue(w.ctx, telegram.NewItem(telegram.Delivery{Workspace: w.ws.Slug,
		Inbound: telegram.Inbound{User: w.bia, Kind: telegram.KindText, Text: "agora pelo telegram", ChatID: biaChat, MessageID: 9}}, w.gm.ID)); err != nil {
		t.Fatal(err)
	}
	sent := len(w.chatTexts(biaChat))
	if r := w.forward("choose", map[string]any{"option": []any{"a", "b"}}, "E agora?"); r.Error == nil || r.Error.Code != "pending" {
		t.Fatalf("mestre choose after a message of the Telegram: %+v", r)
	}
	if got := w.chatTexts(biaChat); len(got) != sent+1 || !strings.Contains(got[sent], "E agora?") {
		t.Fatalf("the Telegram got %q; want the question", got[sent:])
	}
	var tg panel.ChatEntry
	for _, e := range w.conversation(w.bia).Entries {
		if e.Question != nil && e.Question.ID != q.Question.ID {
			tg = e
		}
	}
	if tg.Question == nil || tg.Channel != "telegram" {
		t.Fatalf("the question of the Telegram in the history: %+v", w.conversation(w.bia).Entries)
	}
	if err := w.run.chat.Choose(w.ctx, w.bia, tg.Question.ID, 0, ""); err == nil {
		t.Fatal("a question of the Telegram was answered through the panel")
	}
}
