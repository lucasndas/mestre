package telegram

import (
	"context"
	"errors"
	"strconv"
	"sync"

	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// Replies is the inbox.Channels of the Telegram: the answer of the Grand Master
// to a message of a user (`mestre reply`) goes back to the chat it came from.
// The bot is made after the orchestration, so the Replies is made first and bound
// later; until then, and while the Citadel may not act, the channel is "away" and
// the answer stays kept.
type Replies struct {
	mu  sync.Mutex
	bot *Bot
}

var (
	_ inbox.Channels      = (*Replies)(nil)
	_ inbox.SourceDeleter = (*Replies)(nil)
)

// Bind ties the channel to the bot.
func (r *Replies) Bind(bot *Bot) {
	r.mu.Lock()
	r.bot = bot
	r.mu.Unlock()
}

func (r *Replies) current() *Bot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.bot
}

// ErrAttachmentsNotSent is an answer with files: the send of files to the chat is
// not ready, and the answer stays kept instead of going without them.
var ErrAttachmentsNotSent = errors.New("telegram: sending files to a chat is not ready")

// Send implements inbox.Channels.
func (r *Replies) Send(ctx context.Context, o inbox.Outbound) error {
	bot := r.current()
	if o.Channel != inbox.ChannelTelegram || bot == nil || !bot.allowed(ctx) {
		return inbox.ErrChannelDown
	}
	if len(o.Attachments) > 0 {
		return ErrAttachmentsNotSent
	}
	chat, err := strconv.ParseInt(o.Conversation, 10, 64)
	if err != nil {
		return err
	}
	_, err = bot.Send(ctx, Out{ChatID: chat, UserID: o.User, Text: o.Text, Workspace: o.Workspace, Origin: "grao_mestre"})
	return err
}
