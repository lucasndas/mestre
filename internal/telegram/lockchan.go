package telegram

import (
	"context"
	"errors"
	"fmt"

	"github.com/lucasndas/mestre/internal/lockdown"
	"github.com/lucasndas/mestre/internal/users"
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
		{Name: "travar", Description: "trava o Mestre: nada é executado até o Dono destravar", Capability: users.Lock, Run: c.lock},
		{Name: "destravar", Description: "destrava o Mestre (só o Dono, com o PIN)", Capability: users.Unlock, Query: true, Run: c.unlock},
	}
}

// Actions are the buttons, for Options.Actions.
func (c *LockChannel) Actions() map[string]Action { return map[string]Action{LockAction: c.confirm} }

func (c *LockChannel) lock(ctx context.Context, call Call) (string, error) {
	if c.Svc.Locked(ctx) {
		return "O Mestre já está travado.", nil
	}
	_, err := c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor",
		Text:    "Travar o Mestre? Os decretos ficam suspensos, toda ferramenta é negada, a caixa de entrada para de entregar, nada é feito no GitHub e missões, rituais e recrutamentos pausam. Os terminais continuam abertos. Só o Dono destrava, com o PIN.",
		Buttons: [][]Button{{{Text: "Confirmar travamento", Action: LockAction, Capability: users.Lock}}}})
	return "", err
}

func (c *LockChannel) confirm(ctx context.Context, k Click) (string, string, error) {
	if err := c.Svc.Lock(ctx, k.User); err != nil {
		if errors.Is(err, lockdown.ErrLevel) {
			return "", "", errors.New("o seu nível não trava o Mestre")
		}
		return "", "", err
	}
	return "Travado.", fmt.Sprintf("Mestre travado por %s.", k.User.Name), nil
}

func (c *LockChannel) unlock(ctx context.Context, call Call) (string, error) {
	if !c.Svc.Locked(ctx) {
		return "O Mestre não está travado.", nil
	}
	req, err := c.Users.StartPIN(ctx, call.User.ID, lockdown.UnlockAction)
	switch {
	case errors.Is(err, users.ErrNoPIN):
		return "Cadastre o seu PIN primeiro.", nil
	case errors.Is(err, users.ErrPINLocked):
		return "O seu PIN está bloqueado por um tempo.", nil
	case err != nil:
		return "", err
	}
	if err := c.bot.Ask(ctx, call.User.ID, PINKind, req.ID, "Digite o seu PIN para destravar o Mestre. A mensagem com o PIN é apagada.", promptTTL); err != nil {
		return "", err
	}
	return "", nil
}

// pinDone is told whether the PIN of the unlock was right.
func (c *LockChannel) pinDone(ctx context.Context, u users.User, requestID string, err error) {
	if err != nil {
		return // wrong: it may try again, or ask /destravar again
	}
	if uerr := c.Svc.Unlock(ctx, u, requestID); uerr != nil {
		if u.TelegramID != 0 {
			_, _ = c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: "Não destravei: " + uerr.Error(), Origin: "motor"})
		}
	}
}
