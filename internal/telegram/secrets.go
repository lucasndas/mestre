package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"

	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/secretscreen"
	"github.com/mestre-ia/mestre/internal/users"
)

// What the Grand Master receives in the place of a secret
// (docs/plataforma/cofre.md#credenciais-de-site).
const (
	// RemovedSecret replaces a piece of a known format.
	RemovedSecret = secretscreen.Removed
	// HeldNotice is all the Grand Master receives of a message with a password
	// phrase: the notice, never the content. It tells him what to do.
	// mestre:allow pt-text: the notice the Grand Master (an agent) receives instead of a secret
	HeldNotice = "[mensagem retida: o usuário mandou uma senha pelo Telegram; o motor a apagou e não a entregou. " +
		// mestre:allow pt-text: the notice the Grand Master (an agent) receives instead of a secret
		"Recuse, peça o cadastro pelo painel e recomende trocar a senha.]"
)

// secretKind says how the engine saw the secret (the filter is shared with the panel:
// internal/secretscreen).
type secretKind = secretscreen.Kind

const (
	secretNone       = secretscreen.None
	secretPassphrase = secretscreen.Passphrase
	secretFormat     = secretscreen.Format
	secretFollowUp   = secretscreen.FollowUp
)

// followUpLife is how long after a held password phrase the next message of the
// same author is held too: "minha senha:" and then the value, in two messages.
const followUpLife = 5 * time.Minute

// screen looks at the text a user sent (secretscreen.Screen over the masker of the bot).
func (b *Bot) screen(text string) (secretKind, string) {
	return secretscreen.Screen(b.o.Mask, text)
}

// bodyOf is the text of a message: the text, or the caption of a file.
func bodyOf(m *Message) string {
	if m.Text != "" {
		return m.Text
	}
	return m.Caption
}

// holdFollowUp makes the next message of the user be held too.
func (b *Bot) holdFollowUp(userID string) {
	b.mu.Lock()
	b.followUp[userID] = b.clk.Now().Add(followUpLife)
	b.mu.Unlock()
}

// followUpOpen says whether the next message of the user is still to be held;
// take also ends the wait (one message).
func (b *Bot) followUpOpen(userID string, take bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	until, ok := b.followUp[userID]
	if !ok {
		return false
	}
	if !b.clk.Now().Before(until) {
		delete(b.followUp, userID)
		return false
	}
	if take {
		delete(b.followUp, userID)
	}
	return true
}

// screenMessage is the filter of a message of a user: the kind of the secret in
// it, and the text without the piece (for a known format). A command is not the
// follow-up of a held phrase.
func (b *Bot) screenMessage(u users.User, m *Message, take bool) (secretKind, string) {
	body := bodyOf(m)
	if k, clean := b.screen(body); k != secretNone {
		return k, clean
	}
	if body != "" && !strings.HasPrefix(strings.TrimSpace(body), "/") && b.followUpOpen(u.ID, take) {
		return secretFollowUp, ""
	}
	return secretNone, body
}

// blocked is what the engine does when a secret arrives: the message is deleted
// from the chat, the attempt goes to the audit (who, where, when: nothing of the
// text) and the author is told to change the secret.
func (b *Bot) blocked(ctx context.Context, u users.User, m *Message, kind secretKind) {
	b.deleteMessage(ctx, m.Chat.ID, m.MessageID)
	if kind == secretPassphrase {
		b.holdFollowUp(u.ID)
	}
	// the id comes from the message, so a message handled again (the process died
	// in between) is audited once
	id := fmt.Sprintf("secret-tg-%d-%d", m.Chat.ID, m.MessageID)
	if err := inbox.AuditSecretAttempt(ctx, b.o.DB, id, b.clk.Now(), b.o.Machine, u.ID, inbox.ChannelTelegram, string(kind)); err != nil {
		b.log.Warn("telegram: the attempt to send a secret was not audited", "err", err)
	}
	advice := i18n.M("bot.secret_masked_advice")
	if kind.Held() {
		advice = i18n.M("bot.secret_held_advice")
	}
	b.say(ctx, m.Chat.ID, u.ID, advice)
}

// blockedStranger is the same for a sender who is not a user: the message is
// deleted and audited (by his Telegram id); nothing is said to him.
func (b *Bot) blockedStranger(ctx context.Context, m *Message, kind secretKind) {
	b.deleteMessage(ctx, m.Chat.ID, m.MessageID)
	id := fmt.Sprintf("secret-tg-%d-%d", m.Chat.ID, m.MessageID)
	who := "telegram:" + strconv.FormatInt(m.From.ID, 10)
	if err := inbox.AuditSecretAttempt(ctx, b.o.DB, id, b.clk.Now(), b.o.Machine, who, inbox.ChannelTelegram, string(kind)); err != nil {
		b.log.Warn("telegram: the attempt to send a secret was not audited", "err", err)
	}
}

// blockedPending is the blocked path of an update that was recorded with a secret
// and never handled (the Citadel could not act, or the process died): the message
// is deleted, audited and the author told. Nothing of the text is left to deliver.
func (b *Bot) blockedPending(ctx context.Context, up Update) {
	m := up.Message
	if m == nil || m.From == nil {
		return
	}
	kind := secretKind(up.Secret)
	u, err := b.o.Users.ByTelegram(ctx, m.From.ID)
	switch {
	case errors.Is(err, users.ErrNoUser):
		b.blockedStranger(ctx, m, kind)
	case err == nil && u.State == users.Active:
		b.blocked(ctx, u, m, kind)
	case err == nil:
		b.deleteMessage(ctx, m.Chat.ID, m.MessageID)
	}
}

// DeleteSource implements inbox.SourceDeleter: `mestre reply --redact-source`
// takes the message of the user off his chat.
func (r *Replies) DeleteSource(ctx context.Context, o inbox.Origin) error {
	if o.Channel != inbox.ChannelTelegram {
		return fmt.Errorf("telegram: the channel %q cannot delete a message of the Telegram", o.Channel)
	}
	bot := r.current()
	if bot == nil || !bot.allowed(ctx) {
		return inbox.ErrChannelDown
	}
	chat, err := strconv.ParseInt(o.Conversation, 10, 64)
	if err != nil {
		return err
	}
	msg, err := strconv.ParseInt(o.Message, 10, 64)
	if err != nil {
		return err
	}
	if r.Users != nil { // only the chat of the user the message came from
		u, uerr := r.Users.Get(ctx, o.User)
		if uerr != nil {
			return fmt.Errorf("telegram: the user of the message to delete: %w", uerr)
		}
		if u.TelegramID != chat {
			return errors.New("telegram: the message to delete is not in the chat of its user")
		}
	}
	_, err = bot.o.Client.Call(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": msg})
	return err
}
