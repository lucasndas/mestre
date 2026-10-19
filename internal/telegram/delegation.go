package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
	"github.com/lucasndas/mestre/internal/users"
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
// before the end, that the delegation is about to expire, with Renovar (30 more
// days, with the PIN) and Deixar vencer.
func (c *DecreeChannel) DelegationEnding(ctx context.Context, d decrees.Decree) {
	u, err := c.Users.Get(ctx, d.Author)
	if err != nil || u.State != users.Active || u.TelegramID == 0 || c.bot == nil {
		return
	}
	n := strconv.FormatInt(d.Number, 10)
	_, err = c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Origin: "motor", Workspace: d.Workspace, Mesa: d.Mesa,
		Text:    fmt.Sprintf("A delegação %d (%s) acaba em %s. Renovar vale por mais 30 dias, com o seu PIN.", d.Number, d.Summary(), d.Until.UTC().Format("02/01/2006 15:04 UTC")),
		Buttons: [][]Button{{{Text: "Renovar", Action: DelegationRenewAction, Arg: n}, {Text: "Deixar vencer", Action: DelegationLapseAction, Arg: n}}}})
	if err != nil {
		c.bot.log.Warn("telegram: the reminder of a delegation was not queued", "decree", d.Number, "err", err)
	}
}

// renew is the Renovar: it proposes the same delegation for 30 more days, and
// the author confirms it like any decree.
func (c *DecreeChannel) renew(ctx context.Context, k Click) (string, string, error) {
	n, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil {
		return "", "", errors.New("botão inválido")
	}
	pend, err := c.Svc.Renew(ctx, n, k.User.ID)
	if err != nil {
		return "", "", c.say(err)
	}
	if err := c.Offer(ctx, pend); err != nil {
		return "", "", err
	}
	return "Renovação proposta.", fmt.Sprintf("Renovação da delegação %d proposta: confirme na mensagem seguinte.", n), nil
}

func (c *DecreeChannel) lapse(_ context.Context, k Click) (string, string, error) {
	return "Combinado.", "A delegação " + k.Arg + " vence no prazo.", nil
}

// revokeAllAsk is /revogar_tudo: only the Owner (the bot checks the level), with
// one confirmation by button, no PIN: it only restricts.
func (c *DecreeChannel) revokeAllAsk(ctx context.Context, call Call) (string, error) {
	_, err := c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor",
		Text:    "Revogar todas as delegações (de decisão, no GitHub, do Operador e subdelegações) e todas as regras temporárias de mesa, em todos os workspaces? Não para os agentes: para isso há o /travar.",
		Buttons: [][]Button{{{Text: "Revogar todas as delegações", Action: RevokeAllAction, Capability: users.DelegateHigh}}}})
	return "", err
}

// RevokeAllClick is the confirmation of the emergency button; the panel calls it
// too.
func (c *DecreeChannel) revokeAll(ctx context.Context, k Click) (string, string, error) {
	rep, err := c.Svc.RevokeAllDelegations(ctx, k.User.ID)
	switch {
	case errors.Is(err, decrees.ErrNotAllowed):
		return "", "", errors.New("só o Dono revoga todas as delegações")
	case err != nil:
		return "", "", err
	}
	rules := 0
	for _, n := range rep.Rules {
		rules += n
	}
	return "Revogadas.", fmt.Sprintf("%s revogou todas as delegações: %d delegações, %d subdelegações e %d regras temporárias de mesa.", k.User.Name, len(rep.Decrees), rep.Subdelegations, rules), nil
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
	GrandMaster func(ctx context.Context, workspace, text string)
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
func (h RevokeAllHooks) Told(ctx context.Context, workspace, text string) {
	if h.GrandMaster != nil {
		h.GrandMaster(ctx, workspace, text)
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
		if _, err := h.Channel.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: strings.TrimSpace(text), Workspace: workspace, Origin: "motor"}); err != nil {
			h.Channel.bot.log.Warn("telegram: the notice of the revocation was not queued", "err", err)
		}
	}
}
