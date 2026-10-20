package app

import (
	"context"
	"fmt"
	"strconv"

	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/users"
)

// inboxUsers is inbox.Users over the bot of the Telegram: a notice "Sistema" that
// found no Grand Master alive goes to the Owner, and a notice for one user goes to
// him. While the bot is not up the Owner's notice waits for it (OwnerAlerts).
type inboxUsers struct{ r *stdRun }

var _ inbox.Users = inboxUsers{}

// NotifyOwner implements inbox.Users.
func (u inboxUsers) NotifyOwner(ctx context.Context, _, text string) error {
	u.r.alert.OwnerAlert(ctx, text)
	return nil
}

// NotifyUser implements inbox.Users: user is the id of the user.
func (u inboxUsers) NotifyUser(ctx context.Context, _, user, text string) error {
	u.r.alert.Tell(ctx, user, text)
	return nil
}

// inboxChannel is inbox.Channels over the bot: the answer of a character (`mestre
// reply`) goes to the chat the message came from. A channel that is not the Telegram
// (the panel, from the M6) and a bot that is not up are "channel down": the answer is
// kept and goes when the bot is up (inbox.Service.ChannelUp).
type inboxChannel struct{ r *stdRun }

var _ inbox.Channels = inboxChannel{}

// Send implements inbox.Channels.
func (c inboxChannel) Send(ctx context.Context, o inbox.Outbound) error {
	if o.Channel != inbox.ChannelTelegram {
		return inbox.ErrChannelDown
	}
	bot := c.r.currentBot()
	if bot == nil || c.r.closed.Load() {
		return inbox.ErrChannelDown
	}
	chat, err := strconv.ParseInt(o.Conversation, 10, 64)
	if err != nil {
		return fmt.Errorf("app: the answer %d has no chat of the Telegram: %w", o.ID, err)
	}
	// the user must still be one who may be answered, in the chat the message came from
	u, err := c.r.users.Get(ctx, o.User)
	if err != nil {
		return fmt.Errorf("app: the user of the answer %d: %w", o.ID, err)
	}
	if u.State != users.Active || u.TelegramID != chat {
		return fmt.Errorf("app: the user of the answer %d is not active in that chat", o.ID)
	}
	if len(o.Attachments) > 0 {
		c.r.env.Log.Warn("app: the files of an answer are not sent yet; only its text goes", "answer", o.ID, "files", len(o.Attachments))
	}
	_, err = bot.Send(ctx, telegram.Out{ChatID: chat, UserID: u.ID, Text: o.Text, Workspace: o.Workspace, Origin: "grao-mestre"})
	return err
}
