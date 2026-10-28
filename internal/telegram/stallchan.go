package telegram

import (
	"context"
	"errors"
	"strings"

	"github.com/mestre-ia/mestre/internal/i18n"

	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/digest"
	"github.com/mestre-ia/mestre/internal/users"
)

// PauseTableAction is the button **Pausar mesa** of the warning of a table that
// spins; its argument is "<workspace>:<table>".
const PauseTableAction = "pause_table"

// TablePauser pauses a table on the word of a person (*chain.Supervisor).
type TablePauser interface {
	PauseStalled(ctx context.Context, r chain.PauseRequest) (bool, error)
}

// PauseAutonomyAction is the button **Pausar autonomia** of the warning of a
// queue that accumulates; its argument is "<workspace>:<table>".
const PauseAutonomyAction = digest.PauseAutonomyAction

// AutonomyPauser pauses the autonomy of a table on the word of a person
// (*chain.Supervisor).
type AutonomyPauser interface {
	PauseAutonomy(ctx context.Context, r chain.PauseRequest) (bool, error)
}

// StallChannel sends the warning of a table that works without delivering
// (docs/orquestracao/supervisao-da-cadeia.md#entrega-útil-e-mesa-que-gira) to
// the people who may pause it, with the button **Pausar mesa**. It is the
// chain.StallPeople of the bot.
type StallChannel struct {
	Users *users.Service
	// Pauser is who the button calls; the audit keeps who touched it and why.
	Pauser TablePauser
	// Autonomy is who the button Pausar autonomia calls.
	Autonomy AutonomyPauser

	bot *Bot
}

var (
	_ chain.StallPeople = (*StallChannel)(nil)
	_ chain.QueuePeople = (*StallChannel)(nil)
)

// Bind ties the channel to the bot.
func (c *StallChannel) Bind(bot *Bot) { c.bot = bot }

// Actions are the buttons, for Options.Actions.
func (c *StallChannel) Actions() map[string]Action {
	return map[string]Action{PauseTableAction: c.pause, PauseAutonomyAction: c.pauseAutonomy}
}

// StalledTable implements chain.StallPeople: the message goes to each active
// user who acts in the workspace and has the level of the button (an Approver or
// above). It fails only when nobody could be told.
func (c *StallChannel) StalledTable(ctx context.Context, n chain.StallNotice) error {
	if c.bot == nil {
		return errors.New("the bot of the Telegram is not running")
	}
	all, err := c.Users.List(ctx)
	if err != nil {
		return err
	}
	sent := 0
	for _, u := range all {
		if u.State != users.Active || u.TelegramID == 0 || !u.Acts(n.Workspace) || !users.Can(u.Level, users.PauseButtons) {
			continue
		}
		rows := [][]Button{{{Msg: i18n.M("bot.pause_table"), Action: PauseTableAction, Arg: n.Workspace + ":" + n.Table, Capability: users.PauseButtons}}}
		if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: i18n.Text(n.Text), Workspace: n.Workspace, Mesa: n.Table, Origin: "mesa_parada", Buttons: rows}); err != nil {
			c.bot.log.Warn("telegram: the warning of a stalled table was not queued", "user", u.ID, "err", err)
			continue
		}
		sent++
	}
	if sent == 0 {
		return errors.New("nobody with the level to pause a table could be told")
	}
	return nil
}

// pause is the button: it pauses the table of the warning in the name of the
// user who touched it, if the user still acts in the workspace.
func (c *StallChannel) pause(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	ws, table, ok := strings.Cut(k.Arg, ":")
	if !ok || ws == "" || table == "" {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	if c.Pauser == nil {
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.pause_unavailable")
	}
	u, err := c.Users.Get(ctx, k.User.ID) // the level and the workspaces as stored now
	if err != nil || !u.Acts(ws) {
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.table_not_yours")
	}
	changed, err := c.Pauser.PauseStalled(ctx, chain.PauseRequest{Workspace: ws, Table: table,
		// mestre:allow pt-text: the reason or the subject kept in Portuguese in the audit and the log of the request, not a sentence shown by a channel
		Reason: "a mesa trabalhava há horas sem entregar nada; pausada pelo botão do aviso",
		By:     chain.Actor{User: u.ID, UserLevel: characters.UserLevel(u.Level)}})
	switch {
	case errors.Is(err, chain.ErrTableOver):
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.table_over")
	case err != nil:
		return i18n.Msg{}, i18n.Msg{}, err
	case !changed:
		return i18n.M("bot.table_already_paused"), i18n.Msg{}, nil
	}
	return i18n.M("bot.table_paused", "table", table), i18n.Msg{}, nil
}

// QueueAccumulating implements chain.QueuePeople: the warning of a queue that
// accumulates goes, with the button **Pausar autonomia**, to each active user
// who acts in the workspace and has the level of the button. It fails only when
// nobody could be told.
func (c *StallChannel) QueueAccumulating(ctx context.Context, n chain.QueueNotice) error {
	if c.bot == nil {
		return errors.New("the bot of the Telegram is not running")
	}
	all, err := c.Users.List(ctx)
	if err != nil {
		return err
	}
	sent := 0
	for _, u := range all {
		if u.State != users.Active || u.TelegramID == 0 || !u.Acts(n.Workspace) || !users.Can(u.Level, users.PauseButtons) {
			continue
		}
		rows := [][]Button{{{Msg: i18n.M("bot.pause_autonomy"), Action: PauseAutonomyAction, Arg: n.Workspace + ":" + n.Table, Capability: users.PauseButtons}}}
		if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: i18n.Text(n.Text), Workspace: n.Workspace, Mesa: n.Table, Origin: "fila", Buttons: rows}); err != nil {
			c.bot.log.Warn("telegram: the warning of a queue was not queued", "user", u.ID, "err", err)
			continue
		}
		sent++
	}
	if sent == 0 {
		return errors.New("nobody with the level to pause the autonomy of a table could be told")
	}
	return nil
}

// pauseAutonomy is the button: it pauses the autonomy of the table of the
// warning in the name of the user who touched it, if he still acts in the
// workspace. It only restricts, so it asks no PIN.
func (c *StallChannel) pauseAutonomy(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	ws, table, ok := strings.Cut(k.Arg, ":")
	if !ok || ws == "" || table == "" {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	if c.Autonomy == nil {
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.pause_autonomy_unavailable")
	}
	u, err := c.Users.Get(ctx, k.User.ID) // the level and the workspaces as stored now
	if err != nil || !u.Acts(ws) {
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.table_not_yours")
	}
	changed, err := c.Autonomy.PauseAutonomy(ctx, chain.PauseRequest{Workspace: ws, Table: table,
		// mestre:allow pt-text: the reason or the subject kept in Portuguese in the audit and the log of the request, not a sentence shown by a channel
		Reason: "a fila de aprovações da mesa acumulou; autonomia pausada pelo botão do aviso",
		By:     chain.Actor{User: u.ID, UserLevel: characters.UserLevel(u.Level)}})
	switch {
	case errors.Is(err, chain.ErrTableOver):
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.table_over")
	case errors.Is(err, chain.ErrLevelDenied):
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.pause_autonomy_level")
	case err != nil:
		return i18n.Msg{}, i18n.Msg{}, err
	case !changed:
		return i18n.M("bot.autonomy_already_paused"), i18n.Msg{}, nil
	}
	return i18n.M("bot.autonomy_paused", "table", table), i18n.Msg{}, nil
}
