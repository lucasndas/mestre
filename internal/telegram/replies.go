package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/users"
)

// Replies is the inbox.Channels of the Telegram: the answer of the Grand Master
// to a message of a user (`mestre reply`) goes back to the chat it came from.
// The bot is made after the orchestration, so the Replies is made first and bound
// later; until then, and while the Citadel may not act, the channel is "away" and
// the answer stays kept.
type Replies struct {
	// Users, when set, is asked whether the user of an answer is still active in the chat it
	// goes to.
	Users *users.Service

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
	if r.Users != nil { // the user must still be one who may be answered, in the chat the message came from
		u, uerr := r.Users.Get(ctx, o.User)
		if uerr != nil {
			return fmt.Errorf("telegram: the user of the answer %d: %w", o.ID, uerr)
		}
		if u.State != users.Active || u.TelegramID != chat {
			return fmt.Errorf("telegram: the user of the answer %d is not active in that chat", o.ID)
		}
	}
	_, err = bot.Send(ctx, Out{ChatID: chat, UserID: o.User, Text: o.Text, Workspace: o.Workspace, Origin: "grao_mestre"})
	return err
}
