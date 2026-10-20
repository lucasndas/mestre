package telegram

import (
	"context"
	"regexp"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/users"
)

// What the Grand Master receives in the place of a secret
// (docs/plataforma/cofre.md#credenciais-de-site).
const (
	// RemovedSecret replaces a piece of a known format.
	RemovedSecret = "[segredo removido]"
	// HeldNotice is all the Grand Master receives of a message with a password
	// phrase: the notice, never the content. It tells him what to do.
	HeldNotice = "[mensagem retida: o usuário mandou uma senha pelo Telegram; o motor a apagou e não a entregou. " +
		"Recuse, peça o cadastro pelo painel e recomende trocar a senha.]"
)

// What the author is told.
const (
	heldAdvice = "Apaguei a sua mensagem: ela parecia trazer uma senha, e o Telegram não tem criptografia ponta a ponta. " +
		"Não mandei nada dela ao Grão-Mestre. Cadastre credenciais pelo painel e troque essa senha, que já passou por aqui."
	maskedAdvice = "Apaguei a sua mensagem: ela trazia um segredo (um token, uma chave), e o Telegram não tem criptografia ponta a ponta. " +
		"O Grão-Mestre recebeu a mensagem sem o trecho. Troque esse segredo: ele já passou por aqui."
)

// passphraseRe finds the phrases that give a password in free text ("a senha é",
// "minha senha", "password:").
var passphraseRe = regexp.MustCompile(`(?i)\b(?:a\s+senha\s+(?:é|eh\b)|minha\s+senha\b|senha\s*:|password\s*:|passwd\s*:)`)

// secretKind says how the engine saw the secret.
type secretKind string

const (
	secretNone       secretKind = ""
	secretPassphrase secretKind = "passphrase"
	secretFormat     secretKind = "format"
)

// screen looks at the text a user sent. A passphrase holds the whole message (the
// text comes back empty: nothing of it is kept); a known format is replaced in
// place by RemovedSecret and the rest goes on. The masker is the one of the logs,
// with every secret of the vault in it.
func (b *Bot) screen(text string) (secretKind, string) {
	if text == "" {
		return secretNone, text
	}
	if passphraseRe.MatchString(text) {
		return secretPassphrase, ""
	}
	masked := b.o.Mask(text)
	if masked == text {
		return secretNone, text
	}
	return secretFormat, strings.ReplaceAll(masked, logging.Redacted, RemovedSecret)
}

// blocked is what the engine does when a secret arrives: the message is deleted
// from the chat, the attempt goes to the audit (who, where, when: nothing of the
// text) and the author is told to change the secret.
func (b *Bot) blocked(ctx context.Context, u users.User, m *Message, kind secretKind) {
	b.deleteMessage(ctx, m.Chat.ID, m.MessageID)
	if err := inbox.AuditSecretAttempt(ctx, b.o.DB, protocol.NewIDGen(b.clk).NewString(), b.clk.Now(), b.o.Machine, u.ID, inbox.ChannelTelegram, string(kind)); err != nil {
		b.log.Warn("telegram: the attempt to send a secret was not audited", "err", err)
	}
	advice := maskedAdvice
	if kind == secretPassphrase {
		advice = heldAdvice
	}
	b.say(ctx, m.Chat.ID, u.ID, advice)
}

// DeleteSource implements inbox.SourceDeleter: `mestre reply --redact-source`
// takes the message of the user off his chat.
func (r *Replies) DeleteSource(ctx context.Context, o inbox.Origin) error {
	chat, err := strconv.ParseInt(o.Conversation, 10, 64)
	if err != nil {
		return err
	}
	msg, err := strconv.ParseInt(o.Message, 10, 64)
	if err != nil {
		return err
	}
	bot := r.current()
	if bot == nil {
		return inbox.ErrChannelDown
	}
	_, err = bot.o.Client.Call(ctx, "deleteMessage", map[string]any{"chat_id": chat, "message_id": msg})
	return err
}
