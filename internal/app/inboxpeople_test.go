package app

import (
	"errors"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/internal/vault"
)

// chatTexts are the messages the bot queued for a chat, in order.
func (c *ghCompose) chatTexts(chat int64) []string {
	c.t.Helper()
	rows, err := c.db.Reader().QueryContext(c.ctx, `SELECT params FROM telegram_outbox WHERE chat_id = ? ORDER BY id`, chat)
	if err != nil {
		c.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			c.t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// The standard integration gives the orchestration the people and the channel of the
// inbox: the notice "Sistema" that finds no Grand Master and the answer of `mestre reply`
// reach the user over the bot of the Telegram.
func TestTheNoticeSystemWithNoGrandMasterAndTheAnswerOfReplyReachTheUserChannel(t *testing.T) {
	t.Parallel()
	c := newGHComposeCfg(t, false, true)
	ctx := c.ctx
	var o OrchestrationOptions
	c.run.Extend(&o)
	if o.Users == nil || o.Channels == nil {
		t.Fatalf("Extend left the people (%v) or the channel (%v) of the inbox unset", o.Users, o.Channels)
	}
	owner, err := c.run.users.CreateOwner(ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	code, err := c.run.users.NewCode(ctx, owner, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.run.users.Pair(ctx, users.PairRequest{Text: code.Code, TelegramID: 77, Private: true}); err != nil {
		t.Fatal(err)
	}
	if owner, err = c.run.users.Get(ctx, owner.ID); err != nil {
		t.Fatal(err)
	}
	// the bot is made, not run: what it queues is what the Telegram would get
	if err := c.vault.Put(ctx, TelegramTokenSecret, vault.TypeTelegramBotToken, []byte("123456:fake-bot-token"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := c.run.startBot(ctx); err != nil {
		t.Fatal(err)
	}
	// the Citadel acts: the Telegram is allowed by its fence
	c.run.BindFence(guild.NewFence(guild.FenceConfig{Clock: c.clk, Epoch: func() uint64 { return 1 }}))
	fwd := localchan.NewLocalForwarder()
	c.orc.Register(fwd, nil)
	gm, ok, err := c.orc.Registry.GrandMaster(ctx, c.ws.Slug)
	if err != nil || !ok {
		t.Fatalf("grand master: %v, %v", ok, err)
	}

	// a message of the user reaches the Grand Master; the answer of `mestre reply` goes back to his chat
	err = inboxDeliverer{c.orc}.Deliver(ctx, telegram.Delivery{Workspace: c.ws.Slug, Inbound: telegram.Inbound{User: owner, Kind: telegram.KindText, Text: "como está a mesa?", ChatID: 77, MessageID: 5}})
	if err != nil {
		t.Fatal(err)
	}
	reply := fwd.Forward(ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: "reply-1", AgentID: gm.ID, Cmd: "reply", Args: localchan.TextArgs("a mesa 1 está em revisão"), Flags: map[string]any{}},
		Caller:  localchan.Caller{Identified: true, AgentID: gm.ID},
	})
	if !reply.OK {
		t.Fatalf("mestre reply: %+v", reply.Error)
	}
	if got := c.chatTexts(77); len(got) == 0 || !strings.Contains(got[len(got)-1], "a mesa 1 está em revisão") {
		t.Fatalf("the chat of the user got %q; want the answer of the Grand Master", got)
	}

	// the Grand Master is not there: the notice "Sistema" for it goes to the Owner
	if _, err := c.db.Writer().ExecContext(ctx, `UPDATE characters SET state = ? WHERE id = ?`, string(characters.Stopped), gm.ID); err != nil {
		t.Fatal(err)
	}
	before := len(c.chatTexts(77))
	if err := c.orc.Inbox.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToGrandMaster}, Workspace: c.ws.Slug, Text: "o Grão-Mestre caiu"}); err != nil {
		t.Fatal(err)
	}
	got := c.chatTexts(77)
	if len(got) != before+1 || !strings.Contains(got[before], "Sistema") || !strings.Contains(got[before], "o Grão-Mestre caiu") {
		t.Fatalf("the Owner got %q after the notice; want one \"Sistema\" notice with its text", got[before:])
	}
	// and a notice for the Owner himself
	if err := c.orc.Inbox.Notify(ctx, characters.Notice{Kind: characters.NoticeSystem, To: characters.Recipient{Kind: characters.ToOwner}, Workspace: c.ws.Slug, Text: "aviso ao Dono"}); err != nil {
		t.Fatal(err)
	}
	if got := c.chatTexts(77); !strings.Contains(got[len(got)-1], "aviso ao Dono") {
		t.Fatalf("the Owner got %q; want the notice", got)
	}
	// an answer while the bot is not up is kept for it (ErrChannelDown), not lost
	c.run.Close()
	if err := c.run.replies.Send(ctx, inbox.Outbound{Channel: inbox.ChannelTelegram, User: owner.ID, Conversation: "77", Text: "x"}); !errors.Is(err, inbox.ErrChannelDown) {
		t.Errorf("the channel answered %v after the period ended; want it down, the answer kept", err)
	}
}
