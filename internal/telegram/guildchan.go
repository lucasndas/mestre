package telegram

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/i18n"

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
	return []Command{{Name: "guilda", Description: i18n.M("bot.cmd_guild"), Query: true, Capability: users.Status, Run: c.list}}
}

// Actions are the buttons, for Options.Actions.
func (c *GuildChannel) Actions() map[string]Action {
	return map[string]Action{GuildExcludeAction: c.click}
}

func (c *GuildChannel) list(ctx context.Context, call Call) (i18n.Msg, error) {
	if c.Machines == nil {
		return i18n.M("bot.guild_only_citadel"), nil
	}
	ms, epoch, err := c.Machines(ctx)
	if err != nil {
		return i18n.M("bot.guild_not_listed", "reason", i18n.OfError(err)), nil
	}
	owner := users.Can(call.User.Level, users.ManageMachines)
	lines := []i18n.Msg{i18n.M("bot.guild_head", "epoch", epoch)}
	var buttons [][]Button
	for _, m := range ms {
		role := i18n.M("bot.guild_tower")
		if m.Citadel {
			role = i18n.M("bot.guild_citadel")
		}
		state := i18n.M("bot.guild_offline")
		if m.Online {
			state = i18n.M("bot.guild_online")
		}
		line := []i18n.Msg{i18n.M("bot.guild_machine", "name", plainGuildText(m.Name), "role", role)}
		if m.Excluded {
			line = append(line, i18n.M("bot.guild_excluded"))
		}
		line = append(line, i18n.M("bot.guild_state", "state", state))
		lines = append(lines, i18n.Concat(line...))
		switch {
		case !owner || m.Citadel || len(buttons) >= maxListed:
		case m.Excluded:
			buttons = append(buttons, []Button{{Msg: i18n.M("bot.guild_undo_button", "name", m.Name), Action: GuildExcludeAction, Arg: "u:" + m.ID, Capability: users.ManageMachines}})
		default:
			buttons = append(buttons, []Button{{Msg: i18n.M("bot.guild_exclude_button", "name", m.Name), Action: GuildExcludeAction, Arg: "x:" + m.ID, Capability: users.ManageMachines}})
		}
	}
	if owner && c.Pending != nil {
		if ps, perr := c.Pending(ctx); perr == nil {
			lines = append(lines, c.pendingLines(ps)...)
		}
	}
	_, err = c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor", Msg: i18n.Join(i18n.Text("\n"), lines...), Buttons: buttons})
	return i18n.Msg{}, err
}

// pendingLines is the text of what waits for the Owner, oldest first as given.
func (c *GuildChannel) pendingLines(ps []GuildPending) []i18n.Msg {
	var out []i18n.Msg
	for _, p := range ps {
		switch p.Kind {
		case PendingEntry:
			line := i18n.M("bot.guild_entry", "name", plainGuildText(p.Name), "since", p.At)
			if p.Code != "" {
				line = i18n.Concat(line, i18n.M("bot.guild_entry_code", "code", plainGuildText(p.Code)))
			}
			out = append(out, line)
		case PendingPromotion:
			out = append(out, i18n.M("bot.guild_promotion", "name", plainGuildText(p.Name), "since", p.At))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return append([]i18n.Msg{i18n.Text(""), i18n.M("bot.guild_waiting")}, out...)
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
func (c *GuildChannel) click(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	verb, id, ok := strings.Cut(k.Arg, ":")
	if !ok || id == "" || (verb != "x" && verb != "u") {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	req, err := c.Users.StartPIN(ctx, k.User.ID, guildExcludePrefix+k.Arg)
	switch {
	case errors.Is(err, users.ErrNoPIN):
		return i18n.Msg{}, i18n.Msg{}, errNoPIN
	case errors.Is(err, users.ErrPINLocked):
		return i18n.Msg{}, i18n.Msg{}, errPINLocked
	case err != nil:
		return i18n.Msg{}, i18n.Msg{}, err
	}
	prompt := i18n.M("bot.pin_prompt_exclude")
	if verb == "u" {
		prompt = i18n.M("bot.pin_prompt_undo_exclude")
	}
	if err := c.bot.Ask(ctx, k.User.ID, PINKind, req.ID, prompt, promptTTL); err != nil {
		if errors.Is(err, ErrCaptureBusy) {
			return i18n.Msg{}, i18n.Msg{}, errFinishFirst
		}
		return i18n.Msg{}, i18n.Msg{}, err
	}
	return i18n.M("bot.confirm_with_pin"), i18n.Msg{}, nil
}

// pinDone is told whether the PIN of an exclusion was right.
func (c *GuildChannel) pinDone(ctx context.Context, u users.User, requestID string, err error) {
	if err == users.ErrBadPIN { //nolint:errorlint // only the bare one has tries left
		return
	}
	if err != nil {
		c.tell(ctx, u, i18n.M("bot.guild_pin_wrong"))
		return
	}
	action, aerr := c.Users.PINAction(ctx, requestID)
	if aerr != nil {
		return
	}
	verb, id, _ := strings.Cut(strings.TrimPrefix(action, guildExcludePrefix), ":")
	cur, gerr := c.Users.Get(ctx, u.ID)
	if gerr != nil || users.Require(cur.Level, users.ManageMachines) != nil {
		c.tell(ctx, u, i18n.M("bot.guild_level"))
		return
	}
	if cerr := c.Users.Consume(ctx, requestID, u.ID, action); cerr != nil {
		return
	}
	if c.SetExcluded == nil {
		c.tell(ctx, u, i18n.M("bot.guild_only_citadel_changes"))
		return
	}
	excluded := verb == "x"
	if serr := c.SetExcluded(ctx, id, excluded); serr != nil {
		c.tell(ctx, u, i18n.M("bot.guild_not_changed", "reason", i18n.OfError(serr)))
		return
	}
	if excluded {
		c.tell(ctx, u, i18n.M("bot.guild_excluded_done"))
		return
	}
	c.tell(ctx, u, i18n.M("bot.guild_exclusion_undone"))
}

func (c *GuildChannel) tell(ctx context.Context, u users.User, m i18n.Msg) {
	if u.TelegramID == 0 {
		return
	}
	if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: m, Origin: "motor"}); err != nil {
		c.bot.log.Warn("telegram: the user was not told the result of an exclusion", "err", err)
	}
}
