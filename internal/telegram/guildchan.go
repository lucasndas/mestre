package telegram

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/users"
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
	// Online says the machine is connected to the Citadel now (the Citadel
	// itself is online).
	Online bool
}

// The kinds of a GuildPending.
const (
	// PendingEntry is a machine that asks to enter the Guild.
	PendingEntry = "entrada"
	// PendingPromotion is a request to promote a Tower to Citadel.
	PendingPromotion = "promocao"
)

// GuildPending is something that waits for the Owner in the Guild. Name is text
// of another machine: the source strips the marks of the Telegram from it, and
// the channel strips them again.
type GuildPending struct {
	Kind string
	Name string
	// Code is the code of an entry to compare with the installer screen.
	Code string
	At   time.Time
}

// GuildChannel is /guilda over the bot (docs/telegram/bot.md): the machines of
// the Guild with their role and whether they are excluded and, to the Owner,
// the online or offline of each, and, to the Owner, the pending requests of
// entry and promotion (text only: they are answered in their own approval) and
// the Excluir and Desfazer exclusão of each Tower, with the PIN. Passar a
// Cidadela is not here yet (M7).
type GuildChannel struct {
	Users *users.Service
	// PINs routes the answer of the PIN of an exclusion.
	PINs *PINRouter
	// Machines lists the active machines and the epoch; SetExcluded excludes a
	// machine or undoes it (guild.Citadel.SetExcluded, which refuses the
	// Citadel). Either may fail while this machine is not the acting Citadel.
	Machines    func(ctx context.Context) ([]GuildMachine, uint64, error)
	SetExcluded func(ctx context.Context, machineID string, excluded bool) error
	// Pending lists the entry and promotion requests that wait; only the Owner
	// sees them. Nil, or an error, leaves them out.
	Pending func(ctx context.Context) ([]GuildPending, error)

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
		state := "offline"
		if m.Online {
			state = "online"
		}
		line := fmt.Sprintf("%s · %s", plainGuildText(m.Name), role)
		if m.Excluded {
			line += " · excluída"
		}
		line += " · " + state
		lines = append(lines, line)
		switch {
		case !owner || m.Citadel || len(buttons) >= maxListed:
		case m.Excluded:
			buttons = append(buttons, []Button{{Text: "Desfazer exclusão de " + m.Name, Action: GuildExcludeAction, Arg: "u:" + m.ID, Capability: users.ManageMachines}})
		default:
			buttons = append(buttons, []Button{{Text: "Excluir " + m.Name, Action: GuildExcludeAction, Arg: "x:" + m.ID, Capability: users.ManageMachines}})
		}
	}
	if owner && c.Pending != nil {
		if ps, perr := c.Pending(ctx); perr == nil {
			lines = append(lines, c.pendingLines(ps)...)
		}
	}
	_, err = c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor", Text: strings.Join(lines, "\n"), Buttons: buttons})
	return "", err
}

// pendingLines is the text of what waits for the Owner, oldest first as given.
func (c *GuildChannel) pendingLines(ps []GuildPending) []string {
	var out []string
	for _, p := range ps {
		at := p.At.Format("02/01 15:04")
		switch p.Kind {
		case PendingEntry:
			line := fmt.Sprintf("Pedido de entrada: %s, desde %s", plainGuildText(p.Name), at)
			if p.Code != "" {
				line += ", código " + plainGuildText(p.Code)
			}
			out = append(out, line)
		case PendingPromotion:
			out = append(out, fmt.Sprintf("Pedido de promoção a Cidadela: %s, desde %s", plainGuildText(p.Name), at))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return append([]string{"", "Esperam o Dono (a resposta é no pedido de aprovação):"}, out...)
}

// plainGuildText drops the marks the Telegram turns into links, bold or code,
// so the name another machine chose cannot dress as text of the engine.
func plainGuildText(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune("[]()*_`<>~|\\#", r) {
			return -1
		}
		return r
	}, s)
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
