package approvals

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Chain is what the flow needs of the chain of approval (chain.Service).
type Chain interface {
	Get(ctx context.Context, id int64) (chain.Request, error)
	DecideByPerson(ctx context.Context, d chain.PersonDecision) (chain.Outcome, error)
	Invalidate(ctx context.Context, id int64, why string) error
}

// Directory is the registry of the users (users.Service).
type Directory interface {
	Get(ctx context.Context, id string) (users.User, error)
	List(ctx context.Context) ([]users.User, error)
}

// PINs is the PIN of the users (users.Service).
type PINs interface {
	StartPIN(ctx context.Context, userID, action string) (users.PINRequest, error)
	Consume(ctx context.Context, requestID, userID, action string) error
}

// ErrNoChannel is what a Messenger answers when the user can not be reached by
// it (no Telegram): the user still sees the request in the panel.
var ErrNoChannel = errors.New("approvals: the user has no channel")

// Message is the question to one user.
type Message struct {
	UserID  string
	Request chain.Request
	Text    string
	// Answers are those this user may give.
	Answers []chain.Answer
}

// Messenger is the channel the questions go through (the Telegram bot).
type Messenger interface {
	// Send puts the question in front of the user and returns the reference
	// of the message, to edit it.
	Send(ctx context.Context, m Message) (ref string, err error)
	// Edit replaces the text of a message and its buttons: the answers it
	// lists (none takes them all away).
	Edit(ctx context.Context, e MessageEdit) error
}

// MessageEdit is the new content of a question already sent.
type MessageEdit struct {
	UserID, Ref, Text string
	Request           int64
	Answers           []chain.Answer
}

// Targets reads the live target of a request (the head of the PR, the
// pergaminho and its revision…), to compare with the impression the request
// was bound to.
type Targets interface {
	Current(ctx context.Context, r chain.Request) (json.RawMessage, error)
}

// Options are the pieces of the Flow.
type Options struct {
	DB    *store.DB
	Clock clock.Clock
	Chain Chain
	Users Directory
	PINs  PINs
	// Messenger puts the questions in front of the users.
	Messenger Messenger
	// Targets is required for a request bound to an impression: without it
	// such a request is not approved (it can not be verified).
	Targets Targets
	// Locked says the channel is locked (/travar): no approval is taken.
	Locked func(ctx context.Context) bool
	// Location is where the hours of the messages are said (UTC by default).
	Location *time.Location
	Logger   *slog.Logger
	// OnChange is told after a question was put to the people, edited, counted or
	// taken back: the panel updates what it shows (nil: nobody listens). It runs in
	// the goroutine of the change and must not block.
	OnChange func(ctx context.Context, request int64)
}

// Flow is the flow of approval over the people.
type Flow struct {
	// mu serialises the votes with the invalidation of a user: a vote that
	// counts is one of a user who was eligible when the quorum was reached.
	mu  sync.Mutex
	o   Options
	clk clock.Clock
	log *slog.Logger
}

// New returns the Flow.
func New(o Options) (*Flow, error) {
	if o.DB == nil || o.Chain == nil || o.Users == nil || o.Messenger == nil || o.Locked == nil {
		return nil, errors.New("approvals: the flow needs the database, the chain, the users, the channel and the lock of the channel")
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Location == nil {
		o.Location = time.UTC
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Flow{o: o, clk: o.Clock, log: o.Logger}, nil
}

// Refusals the user reads.
var (
	ErrNotYours      = errors.New("você não pode responder a esse pedido")
	ErrNotOpen       = errors.New("o pedido já foi encerrado")
	ErrAnswered      = errors.New("você já respondeu a esse pedido")
	ErrLevel         = errors.New("seu nível não permite essa resposta")
	ErrLocked        = errors.New("o canal está travado: nenhuma aprovação é aceita até destravar")
	ErrTargetChanged = errors.New("o alvo do pedido mudou: ele foi invalidado")
	ErrNoVerify      = errors.New("o alvo do pedido não pôde ser conferido: nada foi aprovado")
	ErrPIN           = errors.New("o PIN não foi confirmado")
	ErrExpired       = errors.New("o prazo do pedido acabou")
	ErrQuorumEffect  = errors.New("com mais de uma aprovação exigida, só \"Aprovar agora\" vale: decreto e delegação são de quem decide sozinho")
)

// Capability is what the level of a user needs to give an answer.
func Capability(a chain.Answer) users.Capability {
	switch a {
	case chain.AnswerApproveFor, chain.AnswerApproveAlways, chain.AnswerApprovePattern, chain.AnswerDenyAlways:
		return users.PermanentDecree
	case chain.AnswerDelegate:
		return users.DelegateHigh
	}
	return users.DecideApprovals
}

// asksPIN says the answer makes a permanent rule that frees, or a delegation:
// they ask the PIN whatever the policy says (docs/telegram/decretos.md#confirmação-reforçada-com-pin).
// "Negar sempre" only restricts and asks none.
func asksPIN(a chain.Answer) bool {
	switch a {
	case chain.AnswerApproveFor, chain.AnswerApproveAlways, chain.AnswerApprovePattern, chain.AnswerDelegate:
		return true
	}
	return false
}

// CapabilityFor is Capability for an answer to a request: Delegar on a request of risk
// medio makes a decree of policy, which an Administrator or above makes
// (docs/aprovacoes/delegacao.md#delegação-de-decisão); the rest follows the answer.
func CapabilityFor(r chain.Request, a chain.Answer) users.Capability {
	if a == chain.AnswerDelegate && r.Risk.Risk == classes.RiskMedium {
		return users.PermanentDecree
	}
	return Capability(a)
}

func (f *Flow) mayFor(u users.User, r chain.Request, a chain.Answer) bool {
	return users.Can(u.Level, CapabilityFor(r, a))
}

// atLeast says whether the level of a user is at least min, the minimum of the policy.
func atLeast(l users.Level, min classes.Level) bool {
	rank := map[string]int{string(users.Observer): 1, string(users.Approver): 2, string(users.Administrator): 3, string(users.Owner): 4}
	need := map[classes.Level]int{classes.LevelApprover: 2, classes.LevelAdmin: 3, classes.LevelOwner: 4}[min]
	return need > 0 && rank[string(l)] >= need
}

// eligible says whether the user may answer the request at all: active, acting
// in its workspace, able to decide approvals and at the minimum level.
func eligible(u users.User, workspace string, min classes.Level) bool {
	return u.State == users.Active && u.Acts(workspace) && users.Can(u.Level, users.DecideApprovals) && atLeast(u.Level, min)
}

func (f *Flow) ms(t time.Time) int64 { return t.UnixMilli() }

// flowRow is what the question needs to be answered.
type flowRow struct {
	Approvals int
	Require   []string
	Min       classes.Level
	PIN       bool
	Text      string
}

func (f *Flow) row(ctx context.Context, request int64) (flowRow, bool, error) {
	var r flowRow
	var req, min string
	var pin int
	err := f.o.DB.Reader().QueryRowContext(ctx, `SELECT approvals, require, min_level, pin, text FROM approval_flow WHERE request = ?`, request).Scan(&r.Approvals, &req, &min, &pin, &r.Text)
	if errors.Is(err, sql.ErrNoRows) {
		return flowRow{}, false, nil
	}
	if err != nil {
		return flowRow{}, false, err
	}
	if err := json.Unmarshal([]byte(req), &r.Require); err != nil {
		return flowRow{}, false, err
	}
	r.Min, r.PIN = classes.Level(min), pin == 1
	return r, true, nil
}

// Ask implements chain.UserAsker: it records the question, and sends it to
// each user who may answer (or edits the one they have, when the question
// changed).
func (f *Flow) Ask(ctx context.Context, q chain.PeopleQuestion) error {
	all, err := f.o.Users.List(ctx)
	if err != nil {
		return err
	}
	q.Text = withFields(q, all)
	req, _ := json.Marshal(q.Require)
	if q.Require == nil {
		req = []byte("[]")
	}
	pin := 0
	if q.PIN {
		pin = 1
	}
	err = f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_flow (request, approvals, require, min_level, pin, text, asked_at) VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (request) DO UPDATE SET approvals = excluded.approvals, require = excluded.require, min_level = excluded.min_level, pin = excluded.pin, text = excluded.text`,
			q.Request.ID, max(q.Approvals, 1), string(req), string(q.MinLevel), pin, q.Text, f.ms(f.clk.Now()))
		return err
	})
	if err != nil {
		return err
	}
	var sent int
	for _, u := range all {
		if !eligible(u, q.Request.Workspace, q.MinLevel) {
			continue
		}
		var answers []chain.Answer
		for _, a := range q.Answers {
			if f.mayFor(u, q.Request, a) {
				answers = append(answers, a)
			}
		}
		if len(answers) == 0 {
			continue
		}
		text, err := f.render(ctx, q.Request.ID, q.Text, max(q.Approvals, 1))
		if err != nil {
			return err
		}
		if ref, ok, err := f.ref(ctx, q.Request.ID, u.ID); err != nil {
			return err
		} else if ok {
			if err := f.setRef(ctx, q.Request.ID, u.ID, ref, answers); err != nil {
				return err
			}
			if err := f.edit(ctx, MessageEdit{UserID: u.ID, Ref: ref, Text: text, Request: q.Request.ID, Answers: answers}); err != nil {
				f.log.Warn("approvals: the question was not updated", "request", q.Request.ID, "user", u.ID, "err", err)
			}
			sent++
			continue
		}
		if f.joinGroup(ctx, q, u.ID, answers) {
			sent++
			continue
		}
		ref, err := f.o.Messenger.Send(ctx, Message{UserID: u.ID, Request: q.Request, Text: text, Answers: answers})
		switch {
		case errors.Is(err, ErrNoChannel):
			continue
		case err != nil:
			f.log.Warn("approvals: the question was not sent", "request", q.Request.ID, "user", u.ID, "err", err)
			continue
		}
		if err := f.setRef(ctx, q.Request.ID, u.ID, ref, answers); err != nil {
			return err
		}
		sent++
	}
	if sent == 0 {
		f.log.Warn("approvals: nobody can answer the request", "request", q.Request.ID, "min", q.MinLevel)
	}
	f.changed(ctx, q.Request.ID)
	return nil
}

// changed tells the panel that a request changed.
func (f *Flow) changed(ctx context.Context, request int64) {
	if f.o.OnChange != nil {
		f.o.OnChange(ctx, request)
	}
}

// render is the text of the question, with the count of the quorum when it is
// more than one.
func (f *Flow) render(ctx context.Context, request int64, text string, quorum int) (string, error) {
	if quorum <= 1 {
		return text, nil
	}
	n, err := f.counted(ctx, request)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s\n\n%d de %d aprovações", text, n, quorum), nil
}

func (f *Flow) ref(ctx context.Context, request int64, user string) (string, bool, error) {
	var ref string
	err := f.o.DB.Reader().QueryRowContext(ctx, `SELECT ref FROM approval_messages WHERE request = ? AND user_id = ?`, request, user).Scan(&ref)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return ref, err == nil, err
}

func (f *Flow) setRef(ctx context.Context, request int64, user, ref string, answers []chain.Answer) error {
	body, _ := json.Marshal(answers)
	return f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO approval_messages (request, user_id, ref, answers) VALUES (?, ?, ?, ?)
			ON CONFLICT (request, user_id) DO UPDATE SET ref = excluded.ref, answers = excluded.answers`, request, user, ref, string(body))
		return err
	})
}

type recipient struct {
	user, ref string
	answers   []chain.Answer
}

func (f *Flow) recipients(ctx context.Context, request int64) ([]recipient, error) {
	rows, err := f.o.DB.Reader().QueryContext(ctx, `SELECT user_id, ref, answers FROM approval_messages WHERE request = ? ORDER BY user_id`, request)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []recipient
	for rows.Next() {
		var r recipient
		var answers string
		if err := rows.Scan(&r.user, &r.ref, &answers); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(answers), &r.answers); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// counted is how many approvals count to the quorum now.
func (f *Flow) counted(ctx context.Context, request int64) (int, error) {
	var n int
	err := f.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM approval_votes WHERE request = ? AND counts = 1`, request).Scan(&n)
	return n, err
}

func (f *Flow) voters(ctx context.Context, request int64) ([]string, error) {
	rows, err := f.o.DB.Reader().QueryContext(ctx, `SELECT user_id FROM approval_votes WHERE request = ? AND counts = 1 ORDER BY user_id`, request)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Withdraw implements chain.UserAsker: the request ended (or went up), so each
// recipient reads how, and the buttons go away.
func (f *Flow) Withdraw(ctx context.Context, request int64, why string) error {
	defer f.changed(ctx, request)
	r, err := f.o.Chain.Get(ctx, request)
	if err != nil {
		return err
	}
	rs, err := f.recipients(ctx, request)
	if err != nil {
		return err
	}
	by, at := f.decider(ctx, r)
	for _, m := range rs {
		if f.regroup(ctx, request, m) {
			continue // the question is shared with requests that are still open
		}
		text := f.endText(why, by, at, m.user == by.id)
		if err := f.o.Messenger.Edit(ctx, MessageEdit{UserID: m.user, Ref: m.ref, Text: text, Request: request}); err != nil {
			f.log.Warn("approvals: the message was not edited", "request", request, "user", m.user, "err", err)
		}
	}
	return nil
}

type who struct{ id, name string }

// decider is who decided the request and when ("" when none did).
func (f *Flow) decider(ctx context.Context, r chain.Request) (who, time.Time) {
	if r.Decision == nil || !strings.HasPrefix(r.Decision.By, "user:") {
		return who{}, r.ClosedAt
	}
	id := strings.TrimPrefix(r.Decision.By, "user:")
	name := id
	if u, err := f.o.Users.Get(ctx, id); err == nil {
		name = plain(u.Name)
	}
	return who{id: id, name: name}, r.ClosedAt
}

func (f *Flow) endText(why string, by who, at time.Time, self bool) string {
	hour := at.In(f.o.Location).Format("15:04")
	verb := map[string]string{"approved": "Aprovado", "denied": "Negado"}[why]
	switch {
	case verb != "" && by.id != "" && self:
		return fmt.Sprintf("Você respondeu: %s às %s.", verb, hour)
	case verb != "" && by.id != "":
		return fmt.Sprintf("Já respondido por %s: %s às %s.", by.name, verb, hour)
	case why == "approved":
		return "Aprovado."
	case why == "denied":
		return "Negado."
	case why == "expired":
		return "Expirado: ninguém aprovou no prazo e nada foi aprovado."
	case why == "invalidated":
		return "Invalidado: o alvo mudou ou quem aprovou perdeu o direito. Nada foi aprovado."
	case why == "cancelled":
		return "Cancelado: a ação não é mais necessária."
	}
	return "Esse pedido mudou de nível; você receberá a pergunta de novo se for preciso."
}

// Input is an answer of a user.
type Input struct {
	Request int64
	Answer  chain.Answer
	Reason  string
	// Instruction, Pattern and For as in chain.PersonDecision.
	Instruction string
	Pattern     string
	For         time.Duration
}

// Reply is what the user is told. PIN is the request of PIN the channel must
// ask the PIN for (users.PINRequest.ID); when set, Answer was not applied yet.
type Reply struct {
	Text string
	PIN  string
}

// Answer takes the answer of a user to a request: the user, the level, the
// state and the target are checked again here, at the click.
func (f *Flow) Answer(ctx context.Context, userID string, in Input) (Reply, error) {
	return f.answer(ctx, userID, in, nil)
}

// ErrPINNeeded is what AnswerWithPIN answers when the answer asks the PIN and the
// caller did not check one.
var ErrPINNeeded = errors.New("approvals: the answer asks the PIN of the user")

// AnswerWithPIN is Answer for a channel that checks the PIN of the user in the very
// request of the answer (the panel). pinOK says the caller checked it, and spent it.
// An answer that asks the PIN without it is refused with ErrPINNeeded and nothing
// is recorded (the group the answer stands for is read again here, so one that
// grew meanwhile cannot be approved with a PIN that was not asked for it); one that
// asks none is as in Answer.
func (f *Flow) AnswerWithPIN(ctx context.Context, userID string, in Input, pinOK bool) (Reply, error) {
	return f.answer(ctx, userID, in, &pinOK)
}

// NeedsPIN says whether the answer of the user asks the PIN: the request or the
// group it is answered with does, or the answer makes a rule that frees or a
// delegation. It checks nothing else.
func (f *Flow) NeedsPIN(ctx context.Context, userID string, in Input) (bool, error) {
	fr, ok, err := f.row(ctx, in.Request)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, ErrNotOpen
	}
	for _, m := range f.sharedWith(ctx, userID, in) {
		if other, ok, err := f.row(ctx, m.ID); err == nil && ok && other.PIN {
			fr.PIN = true
		}
	}
	return (in.Answer.Approves() || in.Answer == chain.AnswerDelegate) && (fr.PIN || asksPIN(in.Answer)), nil
}

func (f *Flow) answer(ctx context.Context, userID string, in Input, pinChecked *bool) (Reply, error) {
	if f.o.Locked(ctx) {
		return Reply{}, ErrLocked
	}
	u, r, fr, err := f.check(ctx, userID, in.Request, in.Answer)
	if err != nil {
		return Reply{}, err
	}
	members := f.sharedWith(ctx, userID, in)
	for _, m := range members { // one PIN for the group asks it if any of them does
		if other, ok, err := f.row(ctx, m.ID); err == nil && ok && other.PIN {
			fr.PIN = true
		}
	}
	if (in.Answer.Approves() || in.Answer == chain.AnswerDelegate) && (fr.PIN || asksPIN(in.Answer)) {
		if pinChecked != nil {
			if !*pinChecked {
				return Reply{}, ErrPINNeeded
			}
			return f.applyAnswer(ctx, u, r, fr, members, userID, in)
		}
		if f.o.PINs == nil {
			return Reply{}, ErrPIN
		}
		pr, err := f.o.PINs.StartPIN(ctx, u.ID, fmt.Sprintf("approval:%d", r.ID))
		if err != nil {
			return Reply{}, fmt.Errorf("%w: %v", ErrPIN, err)
		}
		if err := f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO approval_pins (pin_request, request, user_id, answer, reason, instruction, pattern, for_ms) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				pr.ID, r.ID, u.ID, string(in.Answer), in.Reason, in.Instruction, in.Pattern, in.For.Milliseconds())
			return err
		}); err != nil {
			return Reply{}, err
		}
		return Reply{Text: "Digite o seu PIN para confirmar.", PIN: pr.ID}, nil
	}
	return f.applyAnswer(ctx, u, r, fr, members, userID, in)
}

// applyAnswer applies an answer whose PIN, if it asks one, is settled.
func (f *Flow) applyAnswer(ctx context.Context, u users.User, r chain.Request, fr flowRow, members []chain.Request, userID string, in Input) (Reply, error) {
	if len(members) > 1 {
		return f.applyGroup(ctx, userID, members, in)
	}
	return f.apply(ctx, u, r, fr, in)
}

// sharedWith are the requests that answer together with the one of the input: the
// ones of the question it is in, when the answer stands for all of them.
func (f *Flow) sharedWith(ctx context.Context, userID string, in Input) []chain.Request {
	if !groupAnswers(in.Answer) {
		return nil
	}
	members, err := f.groupOf(ctx, userID, in.Request)
	if err != nil {
		return nil
	}
	return members
}

// ConfirmPIN applies the answer that waited for the PIN, once, after the PIN
// of the request was right.
func (f *Flow) ConfirmPIN(ctx context.Context, userID, pinRequest string) (Reply, error) {
	var in Input
	var owner, answer string
	var forMS int64
	err := f.o.DB.Reader().QueryRowContext(ctx, `SELECT request, user_id, answer, reason, instruction, pattern, for_ms FROM approval_pins WHERE pin_request = ?`, pinRequest).
		Scan(&in.Request, &owner, &answer, &in.Reason, &in.Instruction, &in.Pattern, &forMS)
	if err != nil || owner != userID {
		return Reply{}, ErrPIN
	}
	in.Answer, in.For = chain.Answer(answer), time.Duration(forMS)*time.Millisecond
	if f.o.PINs == nil {
		return Reply{}, ErrPIN
	}
	// The PIN request is spent once, before anything is applied.
	if err := f.o.PINs.Consume(ctx, pinRequest, userID, fmt.Sprintf("approval:%d", in.Request)); err != nil {
		return Reply{}, ErrPIN
	}
	if err := f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM approval_pins WHERE pin_request = ?`, pinRequest)
		return err
	}); err != nil {
		return Reply{}, err
	}
	if f.o.Locked(ctx) {
		return Reply{}, ErrLocked
	}
	u, r, fr, err := f.check(ctx, userID, in.Request, in.Answer)
	if err != nil {
		return Reply{}, err
	}
	if members := f.sharedWith(ctx, userID, in); len(members) > 1 {
		return f.applyGroup(ctx, userID, members, in)
	}
	return f.apply(ctx, u, r, fr, in)
}

// Details is the text behind "Ver detalhes": the whole question as the engine
// builds it, with the exact target of the request (the files and the PR link). It
// is for a user who acts in the workspace of the request and may decide approvals.
func (f *Flow) Details(ctx context.Context, userID string, request int64) (string, error) {
	u, err := f.o.Users.Get(ctx, userID)
	if err != nil {
		return "", ErrNotYours
	}
	r, err := f.o.Chain.Get(ctx, request)
	if err != nil || !u.Acts(r.Workspace) || !users.Can(u.Level, users.DecideApprovals) {
		return "", ErrNotYours
	}
	text := chain.Text(r, r.Level, nil)
	if len(r.Target) > 0 {
		text += "\nAlvo exato: " + string(r.Target) + "\n"
	}
	return text, nil
}

// check is everything that must hold for the answer of a user to count.
func (f *Flow) check(ctx context.Context, userID string, request int64, a chain.Answer) (users.User, chain.Request, flowRow, error) {
	u, err := f.o.Users.Get(ctx, userID)
	if err != nil || u.State != users.Active {
		return users.User{}, chain.Request{}, flowRow{}, ErrNotYours
	}
	r, err := f.o.Chain.Get(ctx, request)
	if err != nil {
		return users.User{}, chain.Request{}, flowRow{}, ErrNotYours
	}
	if r.State.Closed() {
		return users.User{}, chain.Request{}, flowRow{}, ErrNotOpen
	}
	fr, ok, err := f.row(ctx, request)
	if err != nil {
		return users.User{}, chain.Request{}, flowRow{}, err
	}
	if !ok || r.Level.Kind != chain.LevelPeople {
		return users.User{}, chain.Request{}, flowRow{}, ErrNotOpen
	}
	if !eligible(u, r.Workspace, fr.Min) {
		return users.User{}, chain.Request{}, flowRow{}, ErrNotYours
	}
	if !f.mayFor(u, r, a) {
		return users.User{}, chain.Request{}, flowRow{}, ErrLevel
	}
	if !a.Valid() || a == chain.AnswerClimb {
		return users.User{}, chain.Request{}, flowRow{}, fmt.Errorf("resposta desconhecida: %q", a)
	}
	// A request of the engine (the entry in the Guild) is approved or denied: no
	// decree, no pattern, no delegation and no question back to nobody.
	if (chain.IsSystem(r.Type) || r.Type == classes.RequestGMAction) && !slices.Contains(chain.AnswersFor(chain.LevelPeople, r), a) {
		return users.User{}, chain.Request{}, flowRow{}, ErrLevel
	}
	// The time ran out: whether or not the timer of the chain fired yet, a
	// late click approves nothing.
	if !r.Deadline.IsZero() && !f.clk.Now().Before(r.Deadline) {
		return users.User{}, chain.Request{}, flowRow{}, ErrExpired
	}
	// With more than one approval required, the effect is the plain exception:
	// a decree or a delegation is made by who decides alone, never by the last
	// of the voters.
	if fr.Approvals > 1 && (a.Approves() && a != chain.AnswerApprove || a == chain.AnswerDelegate) {
		return users.User{}, chain.Request{}, flowRow{}, ErrQuorumEffect
	}
	return u, r, fr, nil
}

// mustBind are the types that are never approved without a target.
var mustBind = map[classes.RequestType]bool{
	classes.RequestPR: true, classes.RequestMerge: true, classes.RequestWorkflows: true, classes.RequestDestructive: true, classes.RequestRepoSettings: true,
	classes.RequestAgentsMD: true,
}

// verify compares the live target with the impression of the request; a
// target that changed ends the request as invalidated.
func (f *Flow) verify(ctx context.Context, r chain.Request) error {
	if r.Impression == "" {
		// Types whose approval acts outside the engine, or destroys, are
		// always bound to a target; a request of them without one is not
		// approved (it can not be verified).
		if mustBind[r.Type] {
			return ErrNoVerify
		}
		return nil
	}
	if f.o.Targets == nil {
		return ErrNoVerify
	}
	cur, err := f.o.Targets.Current(ctx, r)
	if err != nil {
		return ErrNoVerify
	}
	imp, err := chain.Impress(cur)
	if err != nil {
		return ErrNoVerify
	}
	if imp != r.Impression {
		if err := f.o.Chain.Invalidate(ctx, r.ID, "o alvo mudou antes de a aprovação ser aplicada"); err != nil {
			f.log.Warn("approvals: the request was not invalidated", "request", r.ID, "err", err)
		}
		return ErrTargetChanged
	}
	return nil
}

// apply records the answer and, when it decides, hands it to the chain.
func (f *Flow) apply(ctx context.Context, u users.User, r chain.Request, fr flowRow, in Input) (Reply, error) {
	approves := in.Answer.Approves() || in.Answer == chain.AnswerDelegate
	// Only an approval needs the target to be the one the request was bound
	// to; a denial stands whatever the target became.
	if approves {
		if err := f.verify(ctx, r); err != nil {
			return Reply{}, err
		}
	}
	d := chain.PersonDecision{Request: r.ID, User: u.ID, Level: string(u.Level), Answer: in.Answer, Reason: in.Reason, Instruction: in.Instruction, Pattern: in.Pattern, For: in.For}
	if in.Answer == chain.AnswerAskBack {
		out, err := f.o.Chain.DecideByPerson(ctx, d)
		return f.reply(out, err)
	}
	counts := 0
	if approves {
		counts = 1
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// The user may have been suspended while the target was read.
	if cur, err := f.o.Users.Get(ctx, u.ID); err != nil || !eligible(cur, r.Workspace, fr.Min) {
		return Reply{}, ErrNotYours
	}
	if err := f.vote(ctx, r.ID, u.ID, in.Answer, counts); err != nil {
		return Reply{}, err
	}
	if approves {
		n, missing, err := f.quorum(ctx, r, fr)
		if err != nil {
			_ = f.unvote(ctx, r.ID, u.ID)
			return Reply{}, err
		}
		if n < fr.Approvals || missing > 0 {
			f.refresh(ctx, r.ID, fr)
			return Reply{Text: fmt.Sprintf("Sua aprovação foi contada: %d de %d.", n, fr.Approvals)}, nil
		}
	}
	out, err := f.o.Chain.DecideByPerson(ctx, d)
	if err != nil {
		// After an error the user can answer again.
		_ = f.unvote(ctx, r.ID, u.ID)
		return Reply{}, err
	}
	return f.reply(out, nil)
}

// quorum counts the approvals that stand now: every voter is looked at again
// (a user suspended since the vote does not count) and how many of the
// required users are among them.
func (f *Flow) quorum(ctx context.Context, r chain.Request, fr flowRow) (n, missing int, err error) {
	voters, err := f.voters(ctx, r.ID)
	if err != nil {
		return 0, 0, err
	}
	var standing []string
	for _, id := range voters {
		u, err := f.o.Users.Get(ctx, id)
		if err != nil || !eligible(u, r.Workspace, fr.Min) {
			if err := f.revoke(ctx, r.ID, id); err != nil {
				return 0, 0, err
			}
			continue
		}
		standing = append(standing, id)
	}
	for _, must := range fr.Require {
		if !slices.Contains(standing, must) {
			missing++
		}
	}
	return len(standing), missing, nil
}

func (f *Flow) reply(out chain.Outcome, err error) (Reply, error) {
	if err != nil {
		return Reply{}, err
	}
	switch out.State {
	case chain.Approved:
		return Reply{Text: "Aprovado."}, nil
	case chain.Denied:
		return Reply{Text: "Negado."}, nil
	}
	return Reply{Text: "Resposta registrada."}, nil
}

// vote records the first answer of a user; a second one is refused.
func (f *Flow) vote(ctx context.Context, request int64, user string, a chain.Answer, counts int) error {
	return f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO approval_votes (request, user_id, answer, counts, at) VALUES (?, ?, ?, ?, ?) ON CONFLICT (request, user_id) DO NOTHING`,
			request, user, string(a), counts, f.ms(f.clk.Now()))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrAnswered
		}
		return nil
	})
}

// revoke ends the count of a vote and keeps the row: it stays in the record of
// the request, and the user does not vote again.
func (f *Flow) revoke(ctx context.Context, request int64, user string) error {
	return f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE approval_votes SET counts = 0, revoked_at = ? WHERE request = ? AND user_id = ? AND counts = 1`, f.ms(f.clk.Now()), request, user)
		return err
	})
}

func (f *Flow) unvote(ctx context.Context, request int64, user string) error {
	return f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM approval_votes WHERE request = ? AND user_id = ?`, request, user)
		return err
	})
}

// refresh edits the question of every recipient with the count of the quorum.
func (f *Flow) refresh(ctx context.Context, request int64, fr flowRow) {
	defer f.changed(ctx, request)
	text, err := f.render(ctx, request, fr.Text, fr.Approvals)
	if err != nil {
		return
	}
	rs, err := f.recipients(ctx, request)
	if err != nil {
		return
	}
	for _, m := range rs {
		if err := f.edit(ctx, MessageEdit{UserID: m.user, Ref: m.ref, Text: text, Request: request, Answers: m.answers}); err != nil {
			f.log.Warn("approvals: the question was not updated", "request", request, "user", m.user, "err", err)
		}
	}
}

// InvalidateBy implements users.Approvals: the user was suspended, removed or
// lost the level, so what they approved in the requests still open stops
// counting (the request goes on to the others) and a PIN they had not
// typed yet is dropped.
func (f *Flow) InvalidateBy(ctx context.Context, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	rows, err := f.o.DB.Reader().QueryContext(ctx, `SELECT v.request FROM approval_votes v JOIN approval_requests r ON r.id = v.request
		WHERE v.user_id = ? AND v.counts = 1 AND r.state = 'open'`, userID)
	if err != nil {
		return err
	}
	var open []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		open = append(open, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	err = f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE approval_votes SET counts = 0, revoked_at = ? WHERE user_id = ? AND counts = 1 AND request IN (SELECT id FROM approval_requests WHERE state = 'open')`, f.ms(f.clk.Now()), userID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM approval_pins WHERE user_id = ?`, userID)
		return err
	})
	if err != nil {
		return err
	}
	for _, id := range open {
		if fr, ok, err := f.row(ctx, id); err == nil && ok {
			f.refresh(ctx, id, fr)
		}
	}
	return nil
}

// RevokeVote ends the count of the approval of a user in a request still open
// (the review that made it was dismissed on GitHub): the row stays in the
// record, the quorum is counted again and the question shows it. A request
// already decided is left as it was.
func (f *Flow) RevokeVote(ctx context.Context, request int64, userID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, err := f.o.Chain.Get(ctx, request)
	if err != nil || r.State.Closed() {
		return err
	}
	if err := f.revoke(ctx, request, userID); err != nil {
		return err
	}
	if fr, ok, err := f.row(ctx, request); err == nil && ok {
		f.refresh(ctx, request, fr)
	}
	return nil
}

// DropPIN forgets the approval of a user that waits for the PIN of a request (the
// review that made it was dismissed): the PIN that comes later confirms nothing.
func (f *Flow) DropPIN(ctx context.Context, request int64, userID string) error {
	return f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM approval_pins WHERE request = ? AND user_id = ?`, request, userID)
		return err
	})
}

// Resend gives a user the buttons of an open request back: the ones they had
// were used up by an answer that did not end (a PIN that failed, a text that
// was refused).
func (f *Flow) Resend(ctx context.Context, request int64, userID string) {
	r, err := f.o.Chain.Get(ctx, request)
	if err != nil || r.State.Closed() {
		return
	}
	fr, ok, err := f.row(ctx, request)
	if err != nil || !ok {
		return
	}
	text, err := f.render(ctx, request, fr.Text, fr.Approvals)
	if err != nil {
		return
	}
	rs, err := f.recipients(ctx, request)
	if err != nil {
		return
	}
	for _, m := range rs {
		if m.user != userID {
			continue
		}
		if err := f.edit(ctx, MessageEdit{UserID: m.user, Ref: m.ref, Text: text, Request: request, Answers: m.answers}); err != nil {
			f.log.Warn("approvals: the buttons were not given back", "request", request, "user", userID, "err", err)
		}
	}
}

// PINFailed drops the approval that waited for a PIN that will not come (it
// failed for good) and gives the user the buttons back.
func (f *Flow) PINFailed(ctx context.Context, userID, pinRequest string) {
	var request int64
	err := f.o.DB.Reader().QueryRowContext(ctx, `SELECT request FROM approval_pins WHERE pin_request = ? AND user_id = ?`, pinRequest, userID).Scan(&request)
	if err != nil {
		return
	}
	_ = f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM approval_pins WHERE pin_request = ?`, pinRequest)
		return err
	})
	f.Resend(ctx, request, userID)
}

// plain takes the marks of Markdown out of a name, so that it can not make a
// link or a format in the message of the others.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune("[]()*_`<>~|\\", r) {
			return -1
		}
		return r
	}, s)
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
