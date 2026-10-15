package inbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
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
}

// ReplyResult is what Reply did.
type ReplyResult struct {
	// Message is the message that was answered, Outbound the kept answer.
	Message  string
	Channel  string
	Outbound int64
	Delivery Delivery
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
	o, err := s.st.AddOutbound(ctx, Outbound{
		Workspace: msg.Workspace, Message: msg.ID, Channel: msg.Origin.Channel, User: msg.Origin.User,
		Conversation: msg.Origin.Conversation, ReplyTo: msg.Origin.Message, Text: r.Text, Attachments: r.Attachments, CreatedAt: s.clk.Now(),
	})
	if err != nil {
		return ReplyResult{}, err
	}
	res := ReplyResult{Message: msg.ID, Channel: o.Channel, Outbound: o.ID, Delivery: DeliveryQueued}
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
