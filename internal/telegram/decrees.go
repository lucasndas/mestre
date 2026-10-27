package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
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
		{Name: "decretos", Description: "lista os decretos ativos e suspensos e as regras temporárias de mesa", Query: true, Capability: users.Status, Run: c.list},
		{Name: "revogar_tudo", Description: "revoga todas as delegações e regras temporárias de mesa (só o Senhor da Guilda)", Capability: users.DelegateHigh, Query: true, Run: c.revokeAllAsk},
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
	text := p.Text
	if p.Order != "" {
		text += "\nSua ordem: " + p.Order
	}
	if p.PIN {
		text += "\nPara confirmar vou pedir o seu PIN (" + p.Why + ")."
	}
	_, err = c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: text, Workspace: p.Workspace, Mesa: p.Mesa, Origin: "motor",
		Buttons: [][]Button{{{Text: "Confirmar", Action: DecreeAction, Arg: "c:" + p.ID}, {Text: "Cancelar", Action: DecreeAction, Arg: "x:" + p.ID}}}})
	return err
}

func (c *DecreeChannel) click(ctx context.Context, k Click) (string, string, error) {
	verb, id, ok := strings.Cut(k.Arg, ":")
	if !ok || id == "" {
		return "", "", errors.New("botão inválido")
	}
	if verb == "x" {
		if err := c.Svc.Cancel(ctx, id, k.User.ID); err != nil {
			return "", "", c.say(err)
		}
		return "Cancelado.", "Proposta cancelada: nada foi gravado.", nil
	}
	d, err := c.Svc.Confirm(ctx, id, k.User.ID, "")
	switch {
	case errors.Is(err, decrees.ErrPINRequired):
		return c.askPIN(ctx, k.User, id)
	case err != nil:
		return "", "", c.say(err)
	}
	return fmt.Sprintf("Decreto %d.", d.Number), c.confirmed(ctx, d), nil
}

// askPIN opens the request of PIN of the proposal and asks it.
func (c *DecreeChannel) askPIN(ctx context.Context, u users.User, proposal string) (string, string, error) {
	req, err := c.Svc.BeginPIN(ctx, proposal, u.ID)
	if err != nil {
		return "", "", c.pinError(err)
	}
	if err := c.bot.Ask(ctx, u.ID, PINKind, req.ID, "Digite o seu PIN para confirmar o decreto. A mensagem com o PIN é apagada.", promptTTL); err != nil {
		if errors.Is(err, ErrCaptureBusy) {
			return "", "", errors.New("termine a resposta anterior primeiro")
		}
		return "", "", err
	}
	return "Confirme com o PIN.", "", nil
}

func (c *DecreeChannel) pinError(err error) error {
	switch {
	case errors.Is(err, users.ErrNoPIN):
		return errors.New("cadastre o seu PIN primeiro")
	case errors.Is(err, users.ErrPINLocked):
		return errors.New("o seu PIN está bloqueado por um tempo")
	}
	return c.say(err)
}

// say is the error a user reads: the engine's own words for a refusal.
func (c *DecreeChannel) say(err error) error {
	var ref *decrees.Refusal
	switch {
	case errors.As(err, &ref):
		return errors.New(ref.Message)
	case errors.Is(err, decrees.ErrNotAuthor):
		return errors.New("só o autor da ordem confirma")
	case errors.Is(err, decrees.ErrNoProposal):
		return errors.New("essa proposta expirou ou já foi respondida")
	case errors.Is(err, decrees.ErrNotAllowed):
		return errors.New("o seu nível não revoga esse decreto")
	case errors.Is(err, decrees.ErrEnded):
		return errors.New("esse decreto já acabou")
	case errors.Is(err, decrees.ErrNoDecree):
		return errors.New("não existe esse decreto")
	case errors.Is(err, decrees.ErrLocked):
		return errors.New("o Mestre está travado: só o Senhor da Guilda destrava")
	case errors.Is(err, decrees.ErrPINFailed):
		return errors.New("PIN incorreto ou expirado")
	}
	return err
}

func (c *DecreeChannel) confirmed(ctx context.Context, d decrees.Decree) string {
	text := fmt.Sprintf("Decreto %d ativo: %s.", d.Number, d.Summary())
	if !d.Until.IsZero() {
		text += " Vale até " + c.Svc.Moment(ctx, d.Until) + "."
	}
	if d.State == decrees.Suspended {
		text += " Ficou suspenso: ele se opõe a outro decreto do mesmo nível, e os dois autores e o Senhor da Guilda foram avisados."
	}
	return text
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
		c.tell(ctx, u, "PIN incorreto ou expirado: a proposta foi cancelada e nada foi gravado.")
		return
	}
	d, cerr := c.Svc.Confirm(ctx, proposal, u.ID, requestID)
	if cerr != nil {
		c.tell(ctx, u, "Não gravei o decreto: "+c.say(cerr).Error())
		return
	}
	c.tell(ctx, u, c.confirmed(ctx, d))
}

func (c *DecreeChannel) tell(ctx context.Context, u users.User, text string) {
	if u.TelegramID == 0 {
		return
	}
	if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Text: text, Origin: "motor"}); err != nil {
		c.bot.log.Warn("telegram: the user was not told the result of a decree", "err", err)
	}
}

// revoke is the Revogar of a decree.
func (c *DecreeChannel) revoke(ctx context.Context, k Click) (string, string, error) {
	n, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil {
		return "", "", errors.New("botão inválido")
	}
	err = c.Svc.Revoke(ctx, n, k.User.ID, "")
	switch {
	case errors.Is(err, decrees.ErrPINRequired):
		req, err := c.Svc.BeginRevokePIN(ctx, n, k.User.ID)
		if err != nil {
			return "", "", c.pinError(err)
		}
		if err := c.bot.Ask(ctx, k.User.ID, PINKind, req.ID, fmt.Sprintf("Digite o seu PIN para revogar o decreto %d, que restringia algo. A mensagem com o PIN é apagada.", n), promptTTL); err != nil {
			if errors.Is(err, ErrCaptureBusy) {
				return "", "", errors.New("termine a resposta anterior primeiro")
			}
			return "", "", err
		}
		return "Confirme com o PIN.", "", nil
	case err != nil:
		return "", "", c.say(err)
	}
	return "Revogado.", fmt.Sprintf("Decreto %d revogado por %s.", n, k.User.Name), nil
}

// pinRevoke is told whether the PIN of a revocation was right.
func (c *DecreeChannel) pinRevoke(ctx context.Context, u users.User, requestID string, err error) {
	if err == users.ErrBadPIN { //nolint:errorlint // only the bare one has tries left
		return
	}
	if err != nil {
		c.tell(ctx, u, "PIN incorreto ou expirado: o decreto não foi revogado.")
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
		c.tell(ctx, u, "Não revoguei: "+c.say(rerr).Error())
		return
	}
	c.tell(ctx, u, fmt.Sprintf("Decreto %d revogado por %s.", n, u.Name))
}

// revokeRule is the Revogar of a temporary table rule: an Administrator or above.
func (c *DecreeChannel) revokeRule(ctx context.Context, k Click) (string, string, error) {
	id, err := strconv.ParseInt(k.Arg, 10, 64)
	if err != nil || c.Rules == nil {
		return "", "", errors.New("botão inválido")
	}
	live, err := c.Rules.Live(ctx)
	if err != nil {
		return "", "", err
	}
	for _, r := range live {
		if r.ID != id {
			continue
		}
		if !k.User.Acts(r.Workspace) {
			return "", "", errors.New("essa regra não é de uma campanha sua")
		}
	}
	err = c.Rules.Revoke(ctx, id, tablerules.Revoker{Administrator: users.Can(k.User.Level, users.RevokeTemporaryRules), ID: k.User.ID})
	switch {
	case errors.Is(err, tablerules.ErrNotAllowed):
		return "", "", errors.New("o seu nível não revoga regras temporárias de mesa")
	case errors.Is(err, tablerules.ErrEnded):
		return "", "", errors.New("essa regra já acabou")
	case err != nil:
		return "", "", err
	}
	return "Revogada.", fmt.Sprintf("Regra temporária de mesa %d revogada por %s.", id, k.User.Name), nil
}

// list is /decretos: the active and suspended decrees of the workspaces of the
// user, with the table rules marked as such. The buttons Revogar are the ones
// the level of the user may use.
func (c *DecreeChannel) list(ctx context.Context, call Call) (string, error) {
	views, err := c.Svc.List(ctx, call.User.ID)
	if err != nil {
		return "", err
	}
	var lines []string
	var buttons [][]Button
	addButton := func(b Button) {
		if len(buttons) < maxListed {
			buttons = append(buttons, []Button{b})
		}
	}
	for _, v := range views {
		line := fmt.Sprintf("#%d · %s (%s) · %s", v.Number, v.AuthorName, levelName(v), v.Summary())
		switch {
		case v.Kind == decrees.KindException:
			line += " · exceção pontual"
		case v.Until.IsZero():
			line += " · permanente"
		default:
			line += " · até " + c.Svc.Moment(ctx, v.Until)
		}
		switch {
		case v.Frozen:
			line += " · SUSPENSO (sistema travado)"
		case v.State == decrees.Suspended:
			line += " · SUSPENSO"
		case v.OverriddenBy != 0:
			line += fmt.Sprintf(" · sobreposto pelo decreto %d", v.OverriddenBy)
		}
		lines = append(lines, line)
		if !v.Frozen && canRevoke(call.User, v) {
			addButton(Button{Text: fmt.Sprintf("Revogar #%d", v.Number), Action: DecreeRevokeAction, Arg: strconv.FormatInt(v.Number, 10)})
		}
	}
	if c.Rules != nil {
		live, err := c.Rules.Live(ctx)
		if err != nil {
			return "", err
		}
		for _, r := range live {
			if !call.User.Acts(r.Workspace) {
				continue
			}
			what := r.Pattern
			if r.Action != nil {
				what = r.Action.String()
			}
			line := fmt.Sprintf("regra temporária de mesa %d · mesa %s · classe %s · %s · até %s", r.ID, r.Table, r.Class, what, c.Svc.Moment(ctx, r.Until))
			if r.State == tablerules.Suspended {
				line += " · SUSPENSA"
			}
			lines = append(lines, line)
			if users.Can(call.User.Level, users.RevokeTemporaryRules) {
				addButton(Button{Text: fmt.Sprintf("Revogar regra de mesa %d", r.ID), Action: TableRuleRevokeAction, Arg: strconv.FormatInt(r.ID, 10), Capability: users.RevokeTemporaryRules})
			}
		}
	}
	if len(lines) == 0 {
		return "Nenhum decreto ativo nos seus workspaces.", nil
	}
	_, err = c.bot.Send(ctx, Out{ChatID: call.ChatID, UserID: call.User.ID, Origin: "motor", Text: "Decretos:\n" + strings.Join(lines, "\n"), Buttons: buttons})
	return "", err
}

func levelName(v decrees.View) string {
	switch v.Level {
	case classes.LevelOwner:
		return users.Owner.Name()
	case classes.LevelAdmin:
		return users.Administrator.Name()
	case classes.LevelApprover:
		return users.Approver.Name()
	case "":
		return "sem nível"
	}
	return string(v.Level)
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
