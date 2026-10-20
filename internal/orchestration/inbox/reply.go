package inbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/protocol"
)

// Delivery says what happened to an answer to a user.
type Delivery string

// The outcomes of Reply.
const (
	// DeliverySent: the channel of the origin took the answer.
	DeliverySent Delivery = "sent"
	// DeliveryQueued: the channel is away; the answer is kept, shown in the
	// panel and sent when the channel is back.
	DeliveryQueued Delivery = "queued"
)

// ReplyRequest is `mestre reply`.
type ReplyRequest struct {
	// From is the Grand Master.
	From        characters.Character
	Text        string
	Attachments []string
	// ToMessage is the id (in the inbox) of the message to answer; empty:
	// the current one.
	ToMessage string
	// RedactSource says the message answered carried a secret the engine did
	// not recognise: it is deleted from the channel it came from and from the
	// inbox, and the attempt goes to the audit (`mestre reply --redact-source`).
	RedactSource bool
}

// ReplyResult is what Reply did.
type ReplyResult struct {
	// Message is the message that was answered, Outbound the kept answer.
	Message  string
	Channel  string
	Outbound int64
	Delivery Delivery
	// SourceDeleted says the message answered was taken off its channel
	// (RedactSource); false with RedactSource means the channel could not do it
	// now, and the message is gone from the inbox all the same.
	SourceDeleted bool
}

// Reply answers a message of a user through the channel it came from
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#resposta-pelo-mesmo-canal).
// The current message is the last one of a user delivered to the Grand
// Master; ToMessage names another. The agent knows nothing of the channel: the
// origin of the message is what decides. An answer is always kept first, so a
// channel that is away loses nothing.
func (s *Service) Reply(ctx context.Context, r ReplyRequest) (ReplyResult, error) {
	if r.From.Role != characters.RoleGrandMaster {
		return ReplyResult{}, ErrNotGrandMaster
	}
	var msg Item
	if r.ToMessage != "" {
		it, err := s.st.Get(ctx, r.ToMessage)
		if err != nil {
			return ReplyResult{}, err
		}
		if it.Target != r.From.ID || it.Kind != KindUser {
			return ReplyResult{}, fmt.Errorf("%w: %s is not a message of a user to you", ErrNothingToReply, r.ToMessage)
		}
		msg = it
	} else {
		it, ok, err := s.st.LastUserDelivered(ctx, r.From.ID)
		if err != nil {
			return ReplyResult{}, err
		}
		if !ok {
			return ReplyResult{}, ErrNothingToReply
		}
		msg = it
	}
	if msg.Origin == nil {
		return ReplyResult{}, fmt.Errorf("%w: %s has no origin", ErrNothingToReply, msg.ID)
	}
	out := Outbound{
		Workspace: msg.Workspace, Message: msg.ID, Channel: msg.Origin.Channel, User: msg.Origin.User,
		Conversation: msg.Origin.Conversation, ReplyTo: msg.Origin.Message, Text: r.Text, Attachments: r.Attachments, CreatedAt: s.clk.Now(),
	}
	deleted := false
	if r.RedactSource {
		// the answer does not cite the message that is going away
		out.ReplyTo = ""
		var err error
		if deleted, err = s.redactSource(ctx, r.From, msg); err != nil {
			return ReplyResult{}, err
		}
	}
	o, err := s.st.AddOutbound(ctx, out)
	if err != nil {
		return ReplyResult{}, err
	}
	res := ReplyResult{Message: msg.ID, Channel: o.Channel, Outbound: o.ID, Delivery: DeliveryQueued, SourceDeleted: deleted}
	if s.send(ctx, o) {
		res.Delivery = DeliverySent
	}
	return res, nil
}

// send gives an answer to its channel; it reports whether the channel took it.
func (s *Service) send(ctx context.Context, o Outbound) bool {
	if s.o.Channels == nil {
		return false
	}
	err := s.o.Channels.Send(ctx, o)
	switch {
	case err == nil:
		if err := s.st.MarkSent(ctx, o.ID, s.clk.Now()); err != nil {
			s.log.Warn("inbox: an answer sent was not recorded", "id", o.ID, "err", err)
		}
		return true
	case !errors.Is(err, ErrChannelDown):
		s.log.Warn("inbox: the channel did not take an answer, it stays kept", "id", o.ID, "channel", o.Channel, "err", err)
	}
	return false
}

// ChannelUp says a channel is back: the answers kept for it are sent, in the
// order they were made, until one is not taken.
func (s *Service) ChannelUp(ctx context.Context, channel string) (int, error) {
	pending, err := s.st.PendingOutbound(ctx, "", channel)
	if err != nil {
		return 0, err
	}
	sent := 0
	for _, o := range pending {
		if !s.send(ctx, o) {
			break
		}
		sent++
	}
	return sent, nil
}

// Outbox lists the answers kept for channels that were away, for the panel.
func (s *Service) Outbox(ctx context.Context, workspace string) ([]Outbound, error) {
	return s.st.PendingOutbound(ctx, workspace, "")
}

// SourceDeleter is a channel that can take a message of a user off the place it
// came from (deleteMessage on the Telegram). A Channels that is not one cannot
// redact the source: the message still leaves the inbox.
type SourceDeleter interface {
	DeleteSource(ctx context.Context, o Origin) error
}

// redactSource makes a message of a user leave: it is deleted from its channel,
// removed from the inbox (and so from every list of it) and the attempt goes to
// the audit without the text. The removal comes first: what the engine keeps is
// the first to go, and a channel that is away does not hold it back.
func (s *Service) redactSource(ctx context.Context, gm characters.Character, msg Item) (bool, error) {
	if err := s.st.Delete(ctx, msg.ID); err != nil {
		return false, err
	}
	if err := AuditSecretAttempt(ctx, s.st.DB, protocol.NewIDGen(s.clk).NewString(), s.clk.Now(), gm.Machine, msg.Origin.User, msg.Origin.Channel, "reply --redact-source"); err != nil {
		return false, err
	}
	d, ok := s.o.Channels.(SourceDeleter)
	if !ok {
		return false, nil
	}
	if err := d.DeleteSource(ctx, *msg.Origin); err != nil {
		s.log.Warn("inbox: the message with a secret was not deleted from its channel", "id", msg.ID, "channel", msg.Origin.Channel, "err", err)
		return false, nil
	}
	return true, nil
}
