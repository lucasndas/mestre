package telegram

import (
	"context"
	"errors"
	"strconv"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/digest"
	"github.com/mestre-ia/mestre/internal/users"
)

// DigestChannel is the summary over the bot (docs/aprovacoes/resumo-e-sugestoes.md):
// the suggestions of rule with Aceitar, Recusar and Ver exemplos, and the
// sample for review with Foi um erro. It is the digest.Sender; the service is
// set after it is made (the service needs the sender first).
type DigestChannel struct {
	Svc     *digest.Service
	Users   *users.Service
	Decrees *DecreeChannel

	bot *Bot
}

var _ digest.Sender = (*DigestChannel)(nil)

// Bind ties the channel to the bot.
func (c *DigestChannel) Bind(bot *Bot) { c.bot = bot }

// Actions are the buttons, for Options.Actions.
func (c *DigestChannel) Actions() map[string]Action {
	return map[string]Action{digest.MistakeAction: c.mistake, digest.AcceptAction: c.accept, digest.DeclineAction: c.decline, digest.ExamplesAction: c.examples}
}

// Send implements digest.Sender.
func (c *DigestChannel) Send(ctx context.Context, m digest.Message) error {
	if c.bot == nil {
		return errors.New("telegram: the bot is not up")
	}
	u, err := c.Users.Get(ctx, m.UserID)
	if err != nil {
		return err
	}
	if u.State != users.Active || u.TelegramID == 0 {
		return nil
	}
	var rows [][]Button
	for _, row := range m.Buttons {
		var r []Button
		for _, b := range row {
			btn := Button{Msg: b.Msg, Action: b.Action, Arg: b.Arg}
			switch b.Action {
			case digest.MistakeAction:
				btn.Capability = users.RevokeTemporaryRules
			case digest.PauseAutonomyAction:
				btn.Capability = users.PauseButtons
			default:
				btn.Capability = users.DelegateHigh // the suggestions are the Owner's
			}
			r = append(r, btn)
		}
		rows = append(rows, r)
	}
	_, err = c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: m.Msg, Workspace: m.Workspace, Origin: "motor", Buttons: rows})
	return err
}

func (c *DigestChannel) say(err error) error {
	switch {
	case errors.Is(err, digest.ErrNotOwner):
		return i18n.Err("bot.digest_owner_only")
	case errors.Is(err, digest.ErrNotOpen):
		return i18n.Err("bot.digest_not_open")
	}
	return err
}

func (c *DigestChannel) id(k Click) (int64, error) {
	n, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil {
		return 0, errInvalidButton
	}
	return n, nil
}

func (c *DigestChannel) mistake(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	n, err := c.id(k)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	revoked, err := c.Svc.Mark(ctx, n, k.User.ID)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, c.say(err)
	}
	parts := []i18n.Msg{i18n.M("bot.marked_mistake", "id", n, "name", k.User.Name)}
	if revoked > 0 {
		parts = append(parts, i18n.M("bot.marked_revoked", "count", revoked))
	}
	parts = append(parts, i18n.M("bot.marked_to_forbid"))
	return i18n.M("bot.marked"), i18n.Msg{}, c.reply(ctx, k, i18n.Concat(parts...))
}

// reply tells the user, in a message of its own, what a click did (the buttons
// of the sample stay for the other actions).
func (c *DigestChannel) reply(ctx context.Context, k Click, m i18n.Msg) error {
	if k.User.TelegramID == 0 || c.bot == nil {
		return nil
	}
	_, err := c.bot.Send(ctx, Out{ChatID: k.User.TelegramID, UserID: k.User.ID, Msg: m, Origin: "motor"})
	return err
}

func (c *DigestChannel) accept(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	n, err := c.id(k)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	pend, err := c.Svc.Accept(ctx, n, k.User.ID)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, c.say(err)
	}
	if err := c.Decrees.Offer(ctx, pend); err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	return i18n.M("bot.proposal_made"), i18n.M("bot.suggestion_accepted"), nil
}

func (c *DigestChannel) decline(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	n, err := c.id(k)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	if err := c.Svc.Decline(ctx, n, k.User.ID); err != nil {
		return i18n.Msg{}, i18n.Msg{}, c.say(err)
	}
	return i18n.M("bot.declined"), i18n.M("bot.suggestion_declined"), nil
}

func (c *DigestChannel) examples(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	n, err := c.id(k)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	msg, err := c.Svc.ExamplesMsg(ctx, n)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	return i18n.M("bot.examples"), i18n.Msg{}, c.reply(ctx, k, msg)
}
