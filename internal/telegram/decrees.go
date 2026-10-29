package telegram

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/tablerules"
	"github.com/mestre-ia/mestre/internal/users"
)

// The actions and the PIN prefixes of the decrees over the bot.
const (
	// DecreeAction is the button of a proposal; its argument is "c:<proposal>"
	// (Confirmar) or "x:<proposal>" (Cancelar).
	DecreeAction = "decree"
	// DecreeRevokeAction is the Revogar of /decretos; its argument is the number.
	DecreeRevokeAction = "decree_revoke"
	// TableRuleRevokeAction is the Revogar of a temporary table rule.
	TableRuleRevokeAction = "table_rule_revoke"
)

// maxListed is how many decrees /decretos puts buttons on in one message.
const maxListed = 20

// TableRuleSource is the temporary table rules /decretos lists and revokes.
type TableRuleSource interface {
	Live(ctx context.Context) ([]tablerules.Rule, error)
	Revoke(ctx context.Context, id int64, by tablerules.Revoker) error
}

// TableRulesOf makes the source of the service of the temporary table rules.
func TableRulesOf(s *tablerules.Service) TableRuleSource { return tableRules{s} }

type tableRules struct{ s *tablerules.Service }

func (t tableRules) Live(ctx context.Context) ([]tablerules.Rule, error) {
	return t.s.Store().Live(ctx, "")
}

func (t tableRules) Revoke(ctx context.Context, id int64, by tablerules.Revoker) error {
	return t.s.Revoke(ctx, id, by)
}

// DecreeChannel is the decrees over the bot (docs/telegram/decretos.md): the
// confirmation of a proposal with Confirmar and Cancelar, the PIN when the
// effect loosens the security, /decretos and the Revogar of each decree and of
// each temporary table rule. The bot is made with its Commands, Actions and
// then bound.
type DecreeChannel struct {
	Svc   *decrees.Service
	Users *users.Service
	// PINs routes the answers of the PIN of the confirmation and of the
	// revocation; Rules are the temporary table rules /decretos also lists
	// (nil: none).
	PINs  *PINRouter
	Rules TableRuleSource

	bot *Bot
}

// Bind ties the channel to the bot.
func (c *DecreeChannel) Bind(bot *Bot) {
	c.bot = bot
	c.PINs.On("decree:", c.pinProposal)
	c.PINs.On("decree_revoke:", c.pinRevoke)
}

// Commands is /decretos, for Options.Commands. It only reads, so it is answered
// while the Mestre is locked.
func (c *DecreeChannel) Commands() []Command {
	return []Command{
		{Name: "decretos", Description: i18n.M("bot.cmd_decrees"), Query: true, Capability: users.Status, Run: c.list},
		{Name: "revogar_tudo", Description: i18n.M("bot.cmd_revoke_all"), Capability: users.DelegateHigh, Query: true, Run: c.revokeAllAsk},
	}
}

// Actions are the buttons, for Options.Actions.
func (c *DecreeChannel) Actions() map[string]Action {
	return map[string]Action{DecreeAction: c.click, DecreeRevokeAction: c.revoke, TableRuleRevokeAction: c.revokeRule,
		DelegationRenewAction: c.renew, DelegationLapseAction: c.lapse, RevokeAllAction: c.revokeAll}
}

// Offer puts a proposal in front of its author with the buttons Confirmar and
// Cancelar: the Grand Master proposed, and only his click makes the decree.
func (c *DecreeChannel) Offer(ctx context.Context, p decrees.Pending) error {
	if c.bot == nil {
		return errors.New("telegram: the bot is not running")
	}
	u, err := c.Users.Get(ctx, p.Author)
	if err != nil {
		return err
	}
	if u.TelegramID == 0 {
		return errors.New("telegram: the author has no Telegram")
	}
	parts := []i18n.Msg{p.Msg}
	if p.Msg.IsZero() {
		parts = []i18n.Msg{i18n.Text(p.Text)}
	}
	if p.Order != "" {
		var order any = p.Order // what the user wrote stays text; what the engine worded is translated
		if !p.OrderMsg.IsZero() {
			order = p.OrderMsg
		}
		parts = append(parts, i18n.M("bot.decree_your_order", "order", order))
	}
	if p.PIN {
		why := p.WhyMsg
		if why.IsZero() {
			why = i18n.Text(p.Why)
		}
		parts = append(parts, i18n.M("bot.decree_ask_pin", "why", why))
	}
	_, err = c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: i18n.Concat(parts...), Workspace: p.Workspace, Mesa: p.Mesa, Origin: "motor",
		Buttons: [][]Button{{{Msg: i18n.M("bot.confirm"), Action: DecreeAction, Arg: "c:" + p.ID}, {Msg: i18n.M("bot.cancel"), Action: DecreeAction, Arg: "x:" + p.ID}}}})
	return err
}

func (c *DecreeChannel) click(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	verb, id, ok := strings.Cut(k.Arg, ":")
	if !ok || id == "" {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	if verb == "x" {
		if err := c.Svc.Cancel(ctx, id, k.User.ID); err != nil {
			return i18n.Msg{}, i18n.Msg{}, c.say(err)
		}
		return i18n.M("bot.cancelled"), i18n.M("bot.proposal_cancelled"), nil
	}
	d, err := c.Svc.Confirm(ctx, id, k.User.ID, "")
	switch {
	case errors.Is(err, decrees.ErrPINRequired):
		return c.askPIN(ctx, k.User, id)
	case err != nil:
		return i18n.Msg{}, i18n.Msg{}, c.say(err)
	}
	return i18n.M("bot.decree_toast", "number", d.Number), c.confirmed(ctx, d), nil
}

// askPIN opens the request of PIN of the proposal and asks it.
func (c *DecreeChannel) askPIN(ctx context.Context, u users.User, proposal string) (i18n.Msg, i18n.Msg, error) {
	req, err := c.Svc.BeginPIN(ctx, proposal, u.ID)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, c.pinError(err)
	}
	if err := c.bot.Ask(ctx, u.ID, PINKind, req.ID, i18n.M("bot.pin_prompt_decree"), promptTTL); err != nil {
		if errors.Is(err, ErrCaptureBusy) {
			return i18n.Msg{}, i18n.Msg{}, errFinishFirst
		}
		return i18n.Msg{}, i18n.Msg{}, err
	}
	return i18n.M("bot.confirm_with_pin"), i18n.Msg{}, nil
}

func (c *DecreeChannel) pinError(err error) error {
	switch {
	case errors.Is(err, users.ErrNoPIN):
		return errNoPIN
	case errors.Is(err, users.ErrPINLocked):
		return errPINLocked
	}
	return c.say(err)
}

// say is the error a user reads: the engine's own words for a refusal, said by whoever
// shows it in the language of the user.
func (c *DecreeChannel) say(err error) error {
	var ref *decrees.Refusal
	switch {
	case errors.As(err, &ref):
		return &i18n.Error{M: ref.Msg}
	case errors.Is(err, decrees.ErrNotAuthor):
		return i18n.Err("bot.decree_not_author")
	case errors.Is(err, decrees.ErrNoProposal):
		return i18n.Err("bot.decree_no_proposal")
	case errors.Is(err, decrees.ErrNotAllowed):
		return i18n.Err("bot.decree_not_allowed")
	case errors.Is(err, decrees.ErrEnded):
		return i18n.Err("bot.decree_ended")
	case errors.Is(err, decrees.ErrNoDecree):
		return i18n.Err("bot.decree_none")
	case errors.Is(err, decrees.ErrLocked):
		return i18n.Err("bot.decree_locked")
	case errors.Is(err, decrees.ErrPINFailed):
		return i18n.Err("bot.decree_pin_failed")
	}
	return err
}

func (c *DecreeChannel) confirmed(ctx context.Context, d decrees.Decree) i18n.Msg {
	parts := []i18n.Msg{i18n.M("bot.decree_active", "number", d.Number, "summary", d.SummaryMsg())}
	if !d.Until.IsZero() {
		parts = append(parts, i18n.M("bot.decree_until", "moment", c.Svc.InGuildZone(ctx, d.Until)))
	}
	if d.State == decrees.Suspended {
		parts = append(parts, i18n.M("bot.decree_suspended"))
	}
	return i18n.Concat(parts...)
}

// pinProposal is told whether the PIN of a confirmation was right.
func (c *DecreeChannel) pinProposal(ctx context.Context, u users.User, requestID string, err error) {
	switch err { //nolint:errorlint // the error that cancelled the request wraps ErrBadPIN: only the bare one has tries left
	case users.ErrBadPIN:
		return // still has tries: the prompt stays
	}
	action, aerr := c.Users.PINAction(ctx, requestID)
	if aerr != nil {
		return
	}
	proposal := strings.TrimPrefix(action, "decree:")
	if err != nil {
		// a wrong or expired PIN leaves the proposal cancelled
		_ = c.Svc.Cancel(ctx, proposal, u.ID)
		c.tell(ctx, u, i18n.M("bot.decree_pin_cancelled"))
		return
	}
	d, cerr := c.Svc.Confirm(ctx, proposal, u.ID, requestID)
	if cerr != nil {
		c.tell(ctx, u, i18n.M("bot.decree_not_saved", "reason", i18n.OfError(c.say(cerr))))
		return
	}
	c.tell(ctx, u, c.confirmed(ctx, d))
}

func (c *DecreeChannel) tell(ctx context.Context, u users.User, m i18n.Msg) {
	if u.TelegramID == 0 {
		return
	}
	if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: m, Origin: "motor"}); err != nil {
		c.bot.log.Warn("telegram: the user was not told the result of a decree", "err", err)
	}
}

// revoke is the Revogar of a decree.
func (c *DecreeChannel) revoke(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	n, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	err = c.Svc.Revoke(ctx, n, k.User.ID, "")
	switch {
	case errors.Is(err, decrees.ErrPINRequired):
		req, err := c.Svc.BeginRevokePIN(ctx, n, k.User.ID)
		if err != nil {
			return i18n.Msg{}, i18n.Msg{}, c.pinError(err)
		}
		if err := c.bot.Ask(ctx, k.User.ID, PINKind, req.ID, i18n.M("bot.pin_prompt_revoke", "number", n), promptTTL); err != nil {
			if errors.Is(err, ErrCaptureBusy) {
				return i18n.Msg{}, i18n.Msg{}, errFinishFirst
			}
			return i18n.Msg{}, i18n.Msg{}, err
		}
		return i18n.M("bot.confirm_with_pin"), i18n.Msg{}, nil
	case err != nil:
		return i18n.Msg{}, i18n.Msg{}, c.say(err)
	}
	return i18n.M("bot.revoked"), i18n.M("bot.decree_revoked_by", "number", n, "name", k.User.Name), nil
}

// pinRevoke is told whether the PIN of a revocation was right.
func (c *DecreeChannel) pinRevoke(ctx context.Context, u users.User, requestID string, err error) {
	if err == users.ErrBadPIN { //nolint:errorlint // only the bare one has tries left
		return
	}
	if err != nil {
		c.tell(ctx, u, i18n.M("bot.decree_pin_not_revoked"))
		return
	}
	action, aerr := c.Users.PINAction(ctx, requestID)
	if aerr != nil {
		return
	}
	n, perr := strconv.ParseInt(strings.TrimPrefix(action, "decree_revoke:"), 10, 64)
	if perr != nil {
		return
	}
	if rerr := c.Svc.Revoke(ctx, n, u.ID, requestID); rerr != nil {
		c.tell(ctx, u, i18n.M("bot.decree_not_revoked", "reason", i18n.OfError(c.say(rerr))))
		return
	}
	c.tell(ctx, u, i18n.M("bot.decree_revoked_by", "number", n, "name", u.Name))
}

// revokeRule is the Revogar of a temporary table rule: an Administrator or above.
func (c *DecreeChannel) revokeRule(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	id, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil || c.Rules == nil {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	live, err := c.Rules.Live(ctx)
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	for _, r := range live {
		if r.ID != id {
			continue
		}
		if !k.User.Acts(r.Workspace) {
			return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.rule_not_yours")
		}
	}
	err = c.Rules.Revoke(ctx, id, tablerules.Revoker{Administrator: users.Can(k.User.Level, users.RevokeTemporaryRules), ID: k.User.ID})
	switch {
	case errors.Is(err, tablerules.ErrNotAllowed):
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.rule_level")
	case errors.Is(err, tablerules.ErrEnded):
		return i18n.Msg{}, i18n.Msg{}, i18n.Err("bot.rule_ended")
	case err != nil:
		return i18n.Msg{}, i18n.Msg{}, err
	}
	return i18n.M("bot.rule_revoked_toast"), i18n.M("bot.rule_revoked_by", "id", id, "name", k.User.Name), nil
}

// list is /decretos: the active and suspended decrees of the workspaces of the
// user, with the table rules marked as such. The buttons Revogar are the ones
// the level of the user may use.
func (c *DecreeChannel) list(ctx context.Context, call Call) (i18n.Msg, error) {
	views, err := c.Svc.List(ctx, call.User.ID)
	if err != nil {
		return i18n.Msg{}, err
	}
	var lines []i18n.Msg
	var buttons [][]Button
	addButton := func(b Button) {
		if len(buttons) < maxListed {
			buttons = append(buttons, []Button{b})
		}
	}
	for _, v := range views {
		line := []i18n.Msg{i18n.M("bot.decrees_line", "number", v.Number, "author", v.AuthorName, "level", levelName(v), "summary", v.SummaryMsg())}
		switch {
		case v.Kind == decrees.KindException:
			line = append(line, i18n.M("bot.decrees_exception"))
		case v.Until.IsZero():
			line = append(line, i18n.M("bot.decrees_permanent"))
		default:
			line = append(line, i18n.M("bot.decrees_until", "moment", c.Svc.InGuildZone(ctx, v.Until)))
		}
		switch {
		case v.Frozen:
			line = append(line, i18n.M("bot.decrees_frozen"))
		case v.State == decrees.Suspended:
			line = append(line, i18n.M("bot.decrees_suspended"))
		case v.OverriddenBy != 0:
			line = append(line, i18n.M("bot.decrees_overridden", "number", v.OverriddenBy))
		}
		lines = append(lines, i18n.Concat(line...))
		if !v.Frozen && canRevoke(call.User, v) {
			addButton(Button{Msg: i18n.M("bot.decrees_revoke_button", "number", v.Number), Action: DecreeRevokeAction, Arg: strconv.FormatInt(v.Number, 10)})
		}
	}
	if c.Rules != nil {
		live, err := c.Rules.Live(ctx)
		if err != nil {
			return i18n.Msg{}, err
		}
		for _, r := range live {
			if !call.User.Acts(r.Workspace) {
				continue
			}
			what := r.Pattern
			if r.Action != nil {
				what = r.Action.String()
			}
			line := []i18n.Msg{i18n.M("bot.rule_line", "id", r.ID, "table", r.Table, "class", r.Class, "what", what, "moment", c.Svc.InGuildZone(ctx, r.Until))}
			if r.State == tablerules.Suspended {
				line = append(line, i18n.M("bot.rule_suspended"))
			}
			lines = append(lines, i18n.Concat(line...))
			if users.Can(call.User.Level, users.RevokeTemporaryRules) {
				addButton(Button{Msg: i18n.M("bot.rule_revoke_button", "id", r.ID), Action: TableRuleRevokeAction, Arg: strconv.FormatInt(r.ID, 10), Capability: users.RevokeTemporaryRules})
			}
		}
	}
	if len(lines) == 0 {
		return i18n.M("bot.decrees_none"), nil
	}
	_, err = c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor", Msg: i18n.M("bot.decrees_list", "lines", i18n.Join(i18n.Text("\n"), lines...)), Buttons: buttons})
	return i18n.Msg{}, err
}

func levelName(v decrees.View) i18n.Msg {
	switch v.Level {
	case classes.LevelOwner:
		return users.Owner.Msg()
	case classes.LevelAdmin:
		return users.Administrator.Msg()
	case classes.LevelApprover:
		return users.Approver.Msg()
	case "":
		return i18n.M("bot.decrees_no_level")
	}
	return i18n.Text(string(v.Level))
}

// canRevoke says the user may be offered the Revogar of a decree: the level
// decides (the engine decides again at the click).
func canRevoke(u users.User, v decrees.View) bool {
	switch users.RevokeDecrees(u.Level) {
	case users.RevokeAny:
		return true
	case users.RevokeAdminAndApprover:
		return v.Level != classes.LevelOwner
	case users.RevokeOwnUnused:
		return v.Author == u.ID && v.Kind == decrees.KindException && v.State == decrees.Active
	}
	return false
}
