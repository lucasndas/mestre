package telegram

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/users"
)

// The kinds of the flow of approval in the bot.
const (
	// ApprovalAction is the button of an answer; its argument is "<request>:<answer>".
	ApprovalAction = "approval"
	// ApprovalTextKind is the capture of the text an answer needs (an
	// instruction, a time, a pattern).
	ApprovalTextKind = "approval_text"
)

// promptTTL is how long a prompt of the flow waits for the answer.
const promptTTL = 10 * time.Minute

// ApprovalChannel is the approvals.Messenger over the bot, and the owner of
// the button and the captures of the flow (docs/aprovacoes/fluxo.md,
// docs/telegram/bot.md). The bot is made with its Actions and Captures and
// then bound: the flow needs the channel and the channel needs the flow.
type ApprovalChannel struct {
	Users *users.Service
	// PINs, when set, is where the answer of the PIN of an approval is routed
	// (the bot has one capture of the PIN for all the actions that ask it);
	// without it the channel makes that capture itself.
	PINs *PINRouter

	bot  *Bot
	flow *approvals.Flow
}

var _ approvals.Messenger = (*ApprovalChannel)(nil)

// Bind ties the channel to the bot and the flow.
func (c *ApprovalChannel) Bind(bot *Bot, flow *approvals.Flow) { c.bot, c.flow = bot, flow }

// Actions are the buttons of the flow, for Options.Actions.
func (c *ApprovalChannel) Actions() map[string]Action {
	return map[string]Action{ApprovalAction: c.click}
}

// Captures are the answers the flow takes by text, for Options.Captures: the
// text an answer needs and the PIN that confirms an approval.
func (c *ApprovalChannel) Captures() map[string]CaptureFunc {
	if c.PINs != nil {
		c.PINs.On("approval:", c.pinDone)
		return map[string]CaptureFunc{ApprovalTextKind: c.text}
	}
	return map[string]CaptureFunc{ApprovalTextKind: c.text, PINKind: PINCapture(c.Users, c.pinDone)}
}

// detailsArg is the answer part of the argument of "Ver detalhes": not an answer
// of the chain, so it is taken before any answer is read.
const detailsArg = "details"

func buttonsFor(request int64, answers []chain.Answer) [][]Button {
	var rows [][]Button
	var row []Button
	for _, a := range answers {
		row = append(row, Button{Msg: a.LabelMsg(), Action: ApprovalAction, Arg: fmt.Sprintf("%d:%s", request, a), Capability: approvals.Capability(a)})
		if len(row) == 2 {
			rows, row = append(rows, row), nil
		}
	}
	if len(answers) > 0 { // a message with its answers taken away has no details either
		row = append(row, Button{Msg: i18n.M("block.details"), Action: ApprovalAction, Arg: fmt.Sprintf("%d:%s", request, detailsArg), Capability: users.DecideApprovals})
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	return rows
}

// Send implements approvals.Messenger.
func (c *ApprovalChannel) Send(ctx context.Context, m approvals.Message) (string, error) {
	u, err := c.Users.Get(ctx, m.UserID)
	if err != nil {
		return "", err
	}
	if u.TelegramID == 0 {
		return "", approvals.ErrNoChannel
	}
	id, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: m.Msg, Workspace: m.Request.Workspace, Mesa: m.Request.Table, Origin: "aprovacao",
		Buttons: buttonsFor(m.Request.ID, m.Answers)})
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(id, 10), nil
}

// Edit implements approvals.Messenger.
func (c *ApprovalChannel) Edit(ctx context.Context, e approvals.MessageEdit) error {
	id, err := strconv.ParseInt(e.Ref, 10, 64)
	if err != nil {
		return err
	}
	return c.bot.Edit(ctx, Edit{Outbox: id, UserID: e.UserID, Msg: e.Msg, Buttons: buttonsFor(e.Request, e.Answers)})
}

// parseArg reads "<request>:<answer>".
func parseArg(arg string) (int64, chain.Answer, bool) {
	id, ans, ok := strings.Cut(arg, ":")
	n, err := strconv.ParseInt(id, 10, 64)
	return n, chain.Answer(ans), ok && err == nil && ans != ""
}

// needs says what an answer asks the user to write.
func needs(a chain.Answer) i18n.Msg {
	switch a {
	case chain.AnswerDenyInstruction:
		return i18n.M("bot.needs_instruction")
	case chain.AnswerAskBack:
		return i18n.M("bot.needs_question")
	case chain.AnswerApproveFor:
		return i18n.M("bot.needs_duration")
	case chain.AnswerApprovePattern:
		return i18n.M("bot.needs_pattern")
	}
	return i18n.Msg{}
}

// click is a button of the flow.
func (c *ApprovalChannel) click(ctx context.Context, k Click) (i18n.Msg, i18n.Msg, error) {
	request, ans, ok := parseArg(k.Arg)
	if !ok {
		return i18n.Msg{}, i18n.Msg{}, errInvalidButton
	}
	if ans == detailsArg {
		text, err := c.flow.Details(ctx, k.User.ID, request)
		if err != nil {
			return i18n.Msg{}, i18n.Msg{}, err
		}
		u, err := c.Users.Get(ctx, k.User.ID)
		if err != nil || u.TelegramID == 0 {
			return i18n.Msg{}, i18n.Msg{}, approvals.ErrNoChannel
		}
		if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: i18n.M("bot.request_details", "id", request, "text", text), Origin: "aprovacao"}); err != nil {
			return i18n.Msg{}, i18n.Msg{}, err
		}
		return i18n.M("bot.details_sent"), i18n.Msg{}, nil
	}
	if prompt := needs(ans); !prompt.IsZero() {
		if err := c.bot.Ask(ctx, k.User.ID, ApprovalTextKind, k.Arg, prompt, promptTTL); err != nil {
			if errors.Is(err, ErrCaptureBusy) {
				return i18n.Msg{}, i18n.Msg{}, errFinishFirst
			}
			return i18n.Msg{}, i18n.Msg{}, err
		}
		return i18n.M("bot.answer_the_prompt"), i18n.Msg{}, nil
	}
	rep, err := c.flow.Answer(ctx, k.User.ID, approvals.Input{Request: request, Answer: ans})
	if err != nil {
		return i18n.Msg{}, i18n.Msg{}, err
	}
	return c.afterAnswer(ctx, k.User, rep), i18n.Msg{}, nil
}

// afterAnswer asks the PIN when the answer waits for it and gives the toast.
func (c *ApprovalChannel) afterAnswer(ctx context.Context, u users.User, rep approvals.Reply) i18n.Msg {
	if rep.PIN == "" {
		return rep.Msg
	}
	if err := c.bot.Ask(ctx, u.ID, PINKind, rep.PIN, i18n.M("bot.pin_prompt_approval"), promptTTL); err != nil {
		c.flow.PINFailed(ctx, u.ID, rep.PIN)
		return i18n.M("bot.pin_not_asked", "reason", i18n.OfError(err))
	}
	return i18n.M("bot.confirm_with_pin")
}

// AskPIN implements approvals.PINPrompter: the review a user made on GitHub is
// valid and the request asks the PIN, so the PIN is asked here, as after the
// button; the approval is applied when it is right (pinDone).
func (c *ApprovalChannel) AskPIN(ctx context.Context, userID string, rep approvals.Reply) error {
	if rep.PIN == "" {
		return nil
	}
	err := c.bot.Ask(ctx, userID, PINKind, rep.PIN, i18n.M("bot.pin_prompt_review"), promptTTL)
	if err != nil {
		c.flow.PINFailed(ctx, userID, rep.PIN)
	}
	return err
}

var _ approvals.PINPrompter = (*ApprovalChannel)(nil)

// text takes the text an answer needs and applies the answer.
func (c *ApprovalChannel) text(ctx context.Context, u users.User, ref, text string) (i18n.Msg, bool, error) {
	request, ans, ok := parseArg(ref)
	if !ok {
		return i18n.M("bot.invalid_answer"), false, nil
	}
	in := approvals.Input{Request: request, Answer: ans}
	text = strings.TrimSpace(text)
	switch ans {
	case chain.AnswerDenyInstruction, chain.AnswerAskBack:
		in.Instruction = text
	case chain.AnswerApprovePattern:
		in.Pattern = text
	case chain.AnswerApproveFor:
		d, err := ParseValidity(text)
		if err != nil {
			return i18n.M("bot.bad_duration"), true, nil
		}
		in.For = d
	}
	rep, err := c.flow.Answer(ctx, u.ID, in)
	if err != nil {
		c.flow.Resend(ctx, request, u.ID)
		return i18n.OfError(err), false, nil
	}
	if rep.PIN != "" {
		// the PIN is asked when this capture is over
		c.bot.Then(u.ID, func(ctx context.Context) { c.afterAnswer(ctx, u, rep) })
		return i18n.M("bot.now_confirm_pin"), false, nil
	}
	return rep.Msg, false, nil
}

// pinDone is told whether the PIN of an approval was right: if so the approval
// is applied, once; if not, the buttons come back (the request of PIN ended).
func (c *ApprovalChannel) pinDone(ctx context.Context, u users.User, pinRequest string, err error) {
	switch err { //nolint:errorlint // the error that cancelled the request wraps ErrBadPIN: only the bare one has tries left
	case nil:
		rep, aerr := c.flow.ConfirmPIN(ctx, u.ID, pinRequest)
		text := rep.Msg
		if aerr != nil {
			text = i18n.M("bot.approval_not_applied", "reason", i18n.OfError(aerr))
		}
		c.tell(ctx, u, text)
	case users.ErrBadPIN:
		// still has tries: the prompt stays
	default:
		c.flow.PINFailed(ctx, u.ID, pinRequest)
	}
}

func (c *ApprovalChannel) tell(ctx context.Context, u users.User, text i18n.Msg) {
	if u.TelegramID == 0 {
		return
	}
	if _, err := c.bot.Send(ctx, Out{ChatID: u.TelegramID, UserID: u.ID, Msg: text, Origin: "aprovacao"}); err != nil {
		c.bot.log.Warn("telegram: the user was not told the result of an approval", "err", err)
	}
}

// ParseValidity reads a time like 90m, 3h or 2d.
func ParseValidity(s string) (time.Duration, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 || days > 365 {
			// mestre:allow pt-text: an internal error that is never shown to a person
			return 0, errors.New("duração inválida")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > 365*24*time.Hour {
		// mestre:allow pt-text: an internal error that is never shown to a person
		return 0, errors.New("duração inválida")
	}
	return d, nil
}

// The refusals of a button that the user reads.
var (
	errInvalidButton = i18n.Err("bot.invalid_button")
	errFinishFirst   = i18n.Err("bot.finish_first")
)

var (
	errNoPIN     = i18n.Err("bot.register_pin_first_err")
	errPINLocked = i18n.Err("bot.pin_locked_err")
)
