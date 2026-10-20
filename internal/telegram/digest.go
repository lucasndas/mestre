package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/lucasndas/mestre/internal/orchestration/digest"
	"github.com/lucasndas/mestre/internal/users"
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
			btn := Button{Text: b.Text, Action: b.Action, Arg: b.Arg}
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
	_, err = c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: m.Text, Workspace: m.Workspace, Origin: "motor", Buttons: rows})
	return err
}

func (c *DigestChannel) say(err error) error {
	switch {
	case errors.Is(err, digest.ErrNotOwner):
		return errors.New("só o Dono responde a uma sugestão")
	case errors.Is(err, digest.ErrNotOpen):
		return errors.New("essa sugestão já foi respondida ou saiu")
	}
	return err
}

func (c *DigestChannel) id(k Click) (int64, error) {
	n, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil {
		return 0, errors.New("botão inválido")
	}
	return n, nil
}

func (c *DigestChannel) mistake(ctx context.Context, k Click) (string, string, error) {
	n, err := c.id(k)
	if err != nil {
		return "", "", err
	}
	revoked, err := c.Svc.Mark(ctx, n, k.User.ID)
	if err != nil {
		return "", "", c.say(err)
	}
	text := fmt.Sprintf("Decisão #%d marcada como erro por %s.", n, k.User.Name)
	if revoked > 0 {
		text += fmt.Sprintf(" %d regra(s) temporária(s) revogada(s).", revoked)
	}
	text += " Para proibir, peça um decreto ao Grão-Mestre."
	return "Marcado.", "", c.reply(ctx, k, text)
}

// reply tells the user, in a message of its own, what a click did (the buttons
// of the sample stay for the other actions).
func (c *DigestChannel) reply(ctx context.Context, k Click, text string) error {
	if k.User.TelegramID == 0 || c.bot == nil {
		return nil
	}
	_, err := c.bot.Send(ctx, Out{ChatID: k.User.TelegramID, UserID: k.User.ID, Text: text, Origin: "motor"})
	return err
}

func (c *DigestChannel) accept(ctx context.Context, k Click) (string, string, error) {
	n, err := c.id(k)
	if err != nil {
		return "", "", err
	}
	pend, err := c.Svc.Accept(ctx, n, k.User.ID)
	if err != nil {
		return "", "", c.say(err)
	}
	if err := c.Decrees.Offer(ctx, pend); err != nil {
		return "", "", err
	}
	return "Proposta feita.", "Sugestão aceita: confirme o decreto na mensagem seguinte.", nil
}

func (c *DigestChannel) decline(ctx context.Context, k Click) (string, string, error) {
	n, err := c.id(k)
	if err != nil {
		return "", "", err
	}
	if err := c.Svc.Decline(ctx, n, k.User.ID); err != nil {
		return "", "", c.say(err)
	}
	return "Recusada.", "Sugestão recusada: o mesmo padrão não volta por 30 dias.", nil
}

func (c *DigestChannel) examples(ctx context.Context, k Click) (string, string, error) {
	n, err := c.id(k)
	if err != nil {
		return "", "", err
	}
	text, err := c.Svc.ExamplesText(ctx, n)
	if err != nil {
		return "", "", err
	}
	return "Exemplos.", "", c.reply(ctx, k, text)
}
