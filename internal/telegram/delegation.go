package telegram

import (
	"context"
	"errors"
	"strconv"

	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/tablerules"
	"github.com/mestre-ia/mestre/internal/users"
)

// The buttons of the delegation (docs/aprovacoes/delegacao.md).
const (
	// DelegationRenewAction is the Renovar of the reminder; its argument is the
	// number of the decree.
	DelegationRenewAction = "delegation_renew"
	// DelegationLapseAction is the Deixar vencer of the reminder.
	DelegationLapseAction = "delegation_lapse"
	// RevokeAllAction is the confirmation of /revogar_tudo.
	RevokeAllAction = "revoke_all"
)

// DelegationEnding implements decrees.Reminder: the author is told, 3 days
// before the end, that the delegation is about to expire, with Renovar (the default
// validity of the workspace, up to 30 days, with the PIN) and Deixar vencer.
func (c *DecreeChannel) DelegationEnding(ctx context.Context, d decrees.Decree) {
	u, err := c.Users.Get(ctx, d.Author)
	if err != nil || u.State != users.Active || u.TelegramID == 0 || c.bot == nil {
		return
	}
	n := strconv.FormatInt(d.Number, 10)
	_, err = c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Origin: "motor", Workspace: d.Workspace, Mesa: d.Mesa,
		Msg:     i18n.M("bot.delegation_ending", "number", d.Number, "summary", d.SummaryMsg(), "moment", c.Svc.InGuildZone(ctx, d.Until)),
		Buttons: [][]Button{{{Msg: i18n.M("bot.renew"), Action: DelegationRenewAction, Arg: n}, {Msg: i18n.M("bot.let_lapse"), Action: DelegationLapseAction, Arg: n}}}})
	if err != nil {
		c.bot.log.Warn("telegram: the reminder of a delegation was not queued", "decree", d.Number, "err", err)
	}
}

// renew is the Renovar: it proposes the same delegation for the default validity of the workspace (up to 30 days), and
// the author confirms it like any decree.
func (c *DecreeChannel) renew(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	n, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	pend, err := c.Svc.Renew(ctx, n, k.User.ID)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, c.say(err)
	}
	if err := c.Offer(ctx, pend); err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	return i18n.M("bot.renewal_proposed"), i18n.M("bot.renewal_proposed_result", "number", n), nil
}

func (c *DecreeChannel) lapse(_ context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	return i18n.M("bot.agreed"), i18n.M("bot.delegation_lapses", "number", k.Arg), nil
}

// revokeAllAsk is /revogar_tudo: only the Owner (the bot checks the level), with
// one confirmation by button, no PIN: it only restricts.
func (c *DecreeChannel) revokeAllAsk(ctx context.Context, call Call) (i18n.Msg, error) {
	_, err := c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor",
		Msg:     i18n.M("bot.revoke_all_ask"),
		Buttons: [][]Button{{{Msg: i18n.M("bot.revoke_all_button"), Action: RevokeAllAction, Capability: users.DelegateHigh}}}})
	return i18n.Msg{}, err
}

// RevokeAllClick is the confirmation of the emergency button; the panel calls it
// too.
func (c *DecreeChannel) revokeAll(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	rep, err := c.Svc.RevokeAllDelegations(ctx, k.User.ID)
	switch {
	case errors.Is(err, decrees.ErrNotAllowed):
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.only_owner_revokes_all")
	case err != nil:
		return i18n.Msg{}, i18n.Msg{}, err
	}
	rules := 0
	for _, n := range rep.Rules {
		rules += n
	}
	return i18n.M("bot.revoked_all_toast"), i18n.M("bot.revoked_all_result", "name", k.User.Name, "delegations", len(rep.Decrees), "subdelegations", rep.Subdelegations, "rules", rules), nil
}

// RevokeAllHooks is what the emergency button reaches beyond the decrees: the
// temporary table rules, the open requests that leaned on a delegation, and the
// Administrators and the Grão-Mestre of each workspace, who are told.
type RevokeAllHooks struct {
	Rules *tablerules.Service
	Users *users.Service
	// Reroute takes the open requests of a workspace that leaned on a
	// delegation up a level (nil: none).
	RerouteFn func(ctx context.Context, workspace string) error
	// GrandMaster puts the notice in the inbox of the Grão-Mestre of the
	// workspace (nil: only the people are told).
	GrandMaster func(ctx context.Context, workspace string, m i18n.Msg)
	// Channel is the channel that has the bot.
	Channel *DecreeChannel
}

var _ decrees.RevokeHooks = RevokeAllHooks{}

// RevokeRules implements decrees.RevokeHooks.
func (h RevokeAllHooks) RevokeRules(ctx context.Context, workspace, by string) (int, error) {
	if h.Rules == nil {
		return 0, nil
	}
	return h.Rules.RevokeAll(ctx, workspace, by)
}

// Reroute implements decrees.RevokeHooks.
func (h RevokeAllHooks) Reroute(ctx context.Context, workspace string) error {
	if h.RerouteFn == nil {
		return nil
	}
	return h.RerouteFn(ctx, workspace)
}

// Told implements decrees.RevokeHooks: the Administrators of the workspace and
// its Grão-Mestre.
func (h RevokeAllHooks) Told(ctx context.Context, workspace string, m i18n.Msg) {
	if h.GrandMaster != nil {
		h.GrandMaster(ctx, workspace, m)
	}
	if h.Users == nil || h.Channel == nil || h.Channel.bot == nil {
		return
	}
	all, err := h.Users.List(ctx)
	if err != nil {
		return
	}
	for _, u := range all {
		if u.State != users.Active || u.TelegramID == 0 || !u.Acts(workspace) || !users.Can(u.Level, users.RevokeTemporaryRules) || u.Level == users.Owner {
			continue // the Owner pressed the button
		}
		if _, err := h.Channel.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: m, Workspace: workspace, Origin: "motor"}); err != nil {
			h.Channel.bot.log.Warn("telegram: the notice of the revocation was not queued", "err", err)
		}
	}
}
