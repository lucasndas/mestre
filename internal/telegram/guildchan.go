package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lucasndas/mestre/internal/users"
)

// GuildExcludeAction is the Excluir and the Desfazer exclusão of /guilda; its
// argument is "x:<machine>" (exclude) or "u:<machine>" (undo).
const GuildExcludeAction = "guild_exclude"

// guildExcludePrefix is the prefix of the action of the request of PIN of an
// exclusion: "guild_exclude:<x|u>:<machine>".
const guildExcludePrefix = GuildExcludeAction + ":"

// GuildMachine is a machine of the Guild as /guilda shows it.
type GuildMachine struct {
	ID, Name string
	// Citadel is true for the machine that is the Citadel of the epoch: it is
	// never excluded.
	Citadel  bool
	Excluded bool
}

// GuildChannel is /guilda over the bot (docs/telegram/bot.md): the machines of
// the Guild with their role and whether they are excluded and, to the Owner,
// the Excluir and Desfazer exclusão of each Tower, with the PIN. The pending
// requests, online/offline and Passar a Cidadela are not here yet.
type GuildChannel struct {
	Users *users.Service
	// PINs routes the answer of the PIN of an exclusion.
	PINs *PINRouter
	// Machines lists the active machines and the epoch; SetExcluded excludes a
	// machine or undoes it (guild.Citadel.SetExcluded, which refuses the
	// Citadel). Either may fail while this machine is not the acting Citadel.
	Machines    func(ctx context.Context) ([]GuildMachine, uint64, error)
	SetExcluded func(ctx context.Context, machineID string, excluded bool) error

	bot *Bot
}

// Bind ties the channel to the bot.
func (c *GuildChannel) Bind(bot *Bot) {
	c.bot = bot
	c.PINs.On(guildExcludePrefix, c.pinDone)
}

// Commands is /guilda, for Options.Commands. It only reads, so it is answered
// while the Mestre is locked.
func (c *GuildChannel) Commands() []Command {
	return []Command{{Name: "guilda", Description: "máquinas da Guilda; o Dono exclui ou desfaz a exclusão de uma Torre, com o PIN", Query: true, Capability: users.Status, Run: c.list}}
}

// Actions are the buttons, for Options.Actions.
func (c *GuildChannel) Actions() map[string]Action {
	return map[string]Action{GuildExcludeAction: c.click}
}

func (c *GuildChannel) list(ctx context.Context, call Call) (string, error) {
	if c.Machines == nil {
		return "Só a Cidadela lista as máquinas da Guilda.", nil
	}
	ms, epoch, err := c.Machines(ctx)
	if err != nil {
		return "Não consegui listar as máquinas agora: " + err.Error(), nil
	}
	owner := users.Can(call.User.Level, users.ManageMachines)
	lines := []string{fmt.Sprintf("Guilda, época %d:", epoch)}
	var buttons [][]Button
	for _, m := range ms {
		role := "Torre"
		if m.Citadel {
			role = "Cidadela"
		}
		line := fmt.Sprintf("%s · %s", m.Name, role)
		if m.Excluded {
			line += " · excluída"
		}
		lines = append(lines, line)
		switch {
		case !owner || m.Citadel || len(buttons) >= maxListed:
		case m.Excluded:
			buttons = append(buttons, []Button{{Text: "Desfazer exclusão de " + m.Name, Action: GuildExcludeAction, Arg: "u:" + m.ID, Capability: users.ManageMachines}})
		default:
			buttons = append(buttons, []Button{{Text: "Excluir " + m.Name, Action: GuildExcludeAction, Arg: "x:" + m.ID, Capability: users.ManageMachines}})
		}
	}
	_, err = c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor", Text: strings.Join(lines, "\n"), Buttons: buttons})
	return "", err
}

// click asks the PIN: the exclusion is made when it is right.
func (c *GuildChannel) click(ctx context.Context, k Click) (string, string, error) {
	verb, id, ok := strings.Cut(k.Arg, ":")
	if !ok || id == "" || (verb != "x" && verb != "u") {
		return "", "", errors.New("botão inválido")
	}
	req, err := c.Users.StartPIN(ctx, k.User.ID, guildExcludePrefix+k.Arg)
	switch {
	case errors.Is(err, users.ErrNoPIN):
		return "", "", errors.New("cadastre o seu PIN primeiro")
	case errors.Is(err, users.ErrPINLocked):
		return "", "", errors.New("o seu PIN está bloqueado por um tempo")
	case err != nil:
		return "", "", err
	}
	what := "excluir a máquina do cofre"
	if verb == "u" {
		what = "desfazer a exclusão da máquina"
	}
	if err := c.bot.Ask(ctx, k.User.ID, PINKind, req.ID, "Digite o seu PIN para "+what+". A mensagem com o PIN é apagada.", promptTTL); err != nil {
		if errors.Is(err, ErrCaptureBusy) {
			return "", "", errors.New("termine a resposta anterior primeiro")
		}
		return "", "", err
	}
	return "Confirme com o PIN.", "", nil
}

// pinDone is told whether the PIN of an exclusion was right.
func (c *GuildChannel) pinDone(ctx context.Context, u users.User, requestID string, err error) {
	if err == users.ErrBadPIN { //nolint:errorlint // only the bare one has tries left
		return
	}
	if err != nil {
		c.tell(ctx, u, "PIN incorreto ou expirado: nada foi mudado na Guilda.")
		return
	}
	action, aerr := c.Users.PINAction(ctx, requestID)
	if aerr != nil {
		return
	}
	verb, id, _ := strings.Cut(strings.TrimPrefix(action, guildExcludePrefix), ":")
	cur, gerr := c.Users.Get(ctx, u.ID)
	if gerr != nil || users.Require(cur.Level, users.ManageMachines) != nil {
		c.tell(ctx, u, "O seu nível não exclui máquinas.")
		return
	}
	if cerr := c.Users.Consume(ctx, requestID, u.ID, action); cerr != nil {
		return
	}
	if c.SetExcluded == nil {
		c.tell(ctx, u, "Só a Cidadela muda a exclusão de uma máquina.")
		return
	}
	excluded := verb == "x"
	if serr := c.SetExcluded(ctx, id, excluded); serr != nil {
		c.tell(ctx, u, "Não mudei a exclusão: "+serr.Error())
		return
	}
	if excluded {
		c.tell(ctx, u, "Máquina excluída: ela apaga o cofre e não é promovida.")
		return
	}
	c.tell(ctx, u, "Exclusão desfeita: a máquina recebe o cofre de novo.")
}

func (c *GuildChannel) tell(ctx context.Context, u users.User, text string) {
	if u.TelegramID == 0 {
		return
	}
	if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: text, Origin: "motor"}); err != nil {
		c.bot.log.Warn("telegram: the user was not told the result of an exclusion", "err", err)
	}
}
