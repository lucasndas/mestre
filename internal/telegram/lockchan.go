package telegram

import (
	"context"
	"errors"

	"github.com/mestre-ia/mestre/internal/i18n"

	"github.com/mestre-ia/mestre/internal/lockdown"
	"github.com/mestre-ia/mestre/internal/users"
)

// LockAction is the button that confirms /travar.
const LockAction = "lock_confirm"

// LockChannel is /travar and /destravar over the bot
// (docs/telegram/seguranca-do-canal.md#travar-e-destravar): /travar asks for a
// confirmation with a button, /destravar asks for the PIN.
type LockChannel struct {
	Svc   *lockdown.Service
	Users *users.Service
	// PINs routes the answer of the PIN of the unlock.
	PINs *PINRouter

	bot *Bot
}

// Bind ties the channel to the bot.
func (c *LockChannel) Bind(bot *Bot) {
	c.bot = bot
	c.PINs.On(lockdown.UnlockAction, c.pinDone)
}

// Commands are the two commands, for Options.Commands. /destravar is answered
// while locked, since it is how it ends.
func (c *LockChannel) Commands() []Command {
	return []Command{
		{Name: "travar", Description: i18n.M("bot.cmd_lock"), Capability: users.Lock, Run: c.lock},
		{Name: "destravar", Description: i18n.M("bot.cmd_unlock"), Capability: users.Unlock, Query: true, Run: c.unlock},
	}
}

// Actions are the buttons, for Options.Actions.
func (c *LockChannel) Actions() map[string]Action { return map[string]Action{LockAction: c.confirm} }

func (c *LockChannel) lock(ctx context.Context, call Call) (i18n.Msg, error) {
	if c.Svc.Locked(ctx) {
		return i18n.M("bot.lock_already"), nil
	}
	_, err := c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor",
		Msg:     i18n.M("bot.lock_ask"),
		Buttons: [][]Button{{{Msg: i18n.M("bot.lock_confirm"), Action: LockAction, Capability: users.Lock}}}})
	return i18n.Msg{}, err
}

func (c *LockChannel) confirm(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	if err := c.Svc.Lock(ctx, k.User); err != nil {
		if errors.Is(err, lockdown.ErrLevel) {
			return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.lock_level")
		}
		return i18n.Msg{}, i18n.Msg{}, err
	}
	return i18n.M("bot.locked_toast"), i18n.M("bot.locked_result", "name", k.User.Name), nil
}

func (c *LockChannel) unlock(ctx context.Context, call Call) (i18n.Msg, error) {
	if !c.Svc.Locked(ctx) {
		return i18n.M("bot.not_locked"), nil
	}
	req, err := c.Users.StartPIN(ctx, call.User.ID, lockdown.UnlockAction)
	switch {
	case errors.Is(err, users.ErrNoPIN):
		return i18n.M("bot.register_pin_first"), nil
	case errors.Is(err, users.ErrPINLocked):
		return i18n.M("bot.your_pin_locked"), nil
	case err != nil:
		return i18n.Msg{}, err
	}
	if err := c.bot.Ask(ctx, call.User.ID, PINKind, req.ID, i18n.M("bot.pin_prompt_unlock"), promptTTL); err != nil {
		return i18n.Msg{}, err
	}
	return i18n.Msg{}, nil
}

// pinDone is told whether the PIN of the unlock was right.
func (c *LockChannel) pinDone(ctx context.Context, u users.User, requestID string, err error) {
	if err != nil {
		return // wrong: it may try again, or ask /destravar again
	}
	if uerr := c.Svc.Unlock(ctx, u, requestID); uerr != nil {
		if u.TelegramID != 0 {
			_, _ = c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: i18n.M("bot.unlock_failed", "reason", i18n.OfError(uerr)), Origin: "motor"})
		}
	}
}
