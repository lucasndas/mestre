package approvals

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/users"
)

// What the panel reads of the flow: the same questions the Telegram gets, as
// data (docs/painel/conversa-e-aprovacoes.md#central-de-aprovações). The panel
// is a channel of the flow, not a second flow: every answer goes through
// Answer/AnswerWithPIN, which check the user, the level, the state, the target
// and the lock again.

// Pending is a question to the people as the panel shows it.
type Pending struct {
	Request chain.Request
	// Text is the question as the Telegram message has it (the fields of the
	// engine first, then the block of the text of the agents).
	Text string
	// Msg is the same question as a message, said in the language of who reads it.
	Msg i18n.Msg
	// Approvals is the quorum, Counted how many approvals stand and Require the
	// users whose approval is required.
	Approvals, Counted int
	Require            []string
	MinLevel           classes.Level
	PIN                bool
	AskedAt            time.Time
	// Voted says the user of the view already answered.
	Voted bool
	// Members are the requests that share the question with this one for the
	// user (this one included, in the order of their ids); nil: it is alone.
	Members []chain.Request
}

// Closure is how a request ended, for "Já respondido por <nome>".
type Closure struct {
	State chain.State
	// By is the name of the user who decided ("" when no user did: an agent, the
	// time, the engine) and ByID his id.
	By, ByID string
	At       time.Time
	Answer   chain.Answer
}

// Visible says whether the user may see the question at all: active, acting in the
// workspace, able to decide approvals and at the minimum level of the request, as the
// Telegram asks it (eligible). A request of the engine, with no workspace, is for who
// acts in "": the Owner.
func Visible(u users.User, workspace string, min classes.Level) bool {
	return eligible(u, workspace, min)
}

// decreeAnswer says the answer makes a rule for the class, which needs the
// exact action of a request of a class permission.
func decreeAnswer(a chain.Answer) bool {
	switch a {
	case chain.AnswerApproveFor, chain.AnswerApprovePattern, chain.AnswerApproveAlways, chain.AnswerDenyAlways:
		return true
	}
	return false
}

// AnswersOf are the answers the user is given for the request, by his level and
// by its type, as the Telegram gives them (docs/aprovacoes/fluxo.md#pergunta-aos-usuários):
// the rules that stay need a class permission with an action (and a pattern, an
// action a pattern can name) and a level that makes them; Delegar is for a request of
// risk alto or medio, and for the level that delegates. The server checks it
// again at every answer; this is what the button shows.
func (f *Flow) AnswersOf(u users.User, r chain.Request, fr Pending) []chain.Answer {
	var out []chain.Answer
	for _, a := range chain.AnswersFor(chain.LevelPeople, r) {
		if !f.mayFor(u, r, a) {
			continue
		}
		if decreeAnswer(a) && (r.Action == nil || !permissionType(r.Type)) {
			continue
		}
		if a == chain.AnswerApprovePattern && !patternable(r) {
			continue
		}
		if a == chain.AnswerDelegate && r.Risk.Risk != classes.RiskHigh && r.Risk.Risk != classes.RiskMedium {
			continue
		}
		if a == chain.AnswerAskBack && r.Requester == "" {
			continue // nobody asked it: there is no one to ask
		}
		if fr.Approvals > 1 && (a.Approves() && a != chain.AnswerApprove || a == chain.AnswerDelegate) {
			continue // with more than one approval required only "Aprovar agora" counts
		}
		out = append(out, a)
	}
	return out
}

func permissionType(t classes.RequestType) bool {
	return t == "" || t == classes.RequestClassPermission
}

// patternable says a pattern can name the action of the request.
func patternable(r chain.Request) bool {
	if r.Action == nil || !permissionType(r.Type) {
		return false
	}
	switch r.Action.Kind {
	case providers.KindShell, providers.KindMCP, providers.KindOther, providers.KindSubagent:
		return true
	}
	return false
}

// SuggestPattern is the pattern the engine suggests for "Aprovar o padrão": the
// program and its subcommand with a star (`npm install foo` gives `npm install *`),
// only when the engine accepts it for the action; "" when it has none to suggest.
// The user edits it, and the engine checks what he sends.
func SuggestPattern(r chain.Request) string {
	if !patternable(r) || r.Action.Kind != providers.KindShell {
		return ""
	}
	words := strings.Fields(r.Action.Command)
	var pattern string
	switch {
	case len(words) >= 3:
		pattern = words[0] + " " + words[1] + " *"
	case len(words) == 2:
		pattern = words[0] + " *"
	default:
		return ""
	}
	if _, err := classes.CheckApprovablePattern(pattern, *r.Action, r.Class, r.Table); err != nil {
		return ""
	}
	return pattern
}

// SuggestGroupPattern is the pattern the requests of a group have in common ("" when none).
func SuggestGroupPattern(rs []chain.Request) string { return groupPattern(rs) }

func (f *Flow) pending(ctx context.Context, u users.User, r chain.Request, fr flowRow, asked time.Time) (Pending, error) {
	counted, err := f.counted(ctx, r.ID)
	if err != nil {
		return Pending{}, err
	}
	p := Pending{Request: r, Text: i18n.Render(i18n.PtBR, fr.Msg), Msg: fr.Msg, Approvals: fr.Approvals, Counted: counted, Require: fr.Require, MinLevel: fr.Min, PIN: fr.PIN, AskedAt: asked}
	if err := f.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM approval_votes WHERE request = ? AND user_id = ?`, r.ID, u.ID).Scan(&counted); err == nil {
		p.Voted = counted > 0
	}
	if !r.State.Closed() {
		if ms, err := f.groupOf(ctx, u.ID, r.ID); err == nil && len(ms) > 1 {
			p.Members = ms
		}
	}
	return p, nil
}

// PendingFor are the questions open with the people that the user may answer, in
// every workspace he acts in, newest first. A request that shares a question with
// others (similar requests) is listed once, by its first member.
func (f *Flow) PendingFor(ctx context.Context, u users.User) ([]Pending, error) {
	rows, err := f.o.DB.Reader().QueryContext(ctx, `SELECT q.request, q.asked_at FROM approval_flow q JOIN approval_requests r ON r.id = q.request
		WHERE r.state = 'open' AND r.level_kind = 'people' ORDER BY q.request DESC`)
	if err != nil {
		return nil, err
	}
	type ask struct {
		id int64
		at int64
	}
	var asks []ask
	for rows.Next() {
		var a ask
		if err := rows.Scan(&a.id, &a.at); err != nil {
			rows.Close()
			return nil, err
		}
		asks = append(asks, a)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	var out []Pending
	seen := map[int64]bool{}
	for _, a := range asks {
		if seen[a.id] {
			continue
		}
		p, ok, err := f.view(ctx, u, a.id, time.UnixMilli(a.at).UTC())
		if err != nil {
			return nil, err
		}
		if !ok || p.Request.State.Closed() || p.Request.Level.Kind != chain.LevelPeople {
			continue
		}
		for _, m := range p.Members {
			seen[m.ID] = true
		}
		out = append(out, p)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Request.ID > out[j].Request.ID })
	return out, nil
}

// Question is one request as the user sees it, open or closed. ErrNotYours when the
// user may not see it (another workspace, a level below, no such request).
func (f *Flow) Question(ctx context.Context, u users.User, request int64) (Pending, error) {
	p, ok, err := f.view(ctx, u, request, time.Time{})
	if err != nil {
		return Pending{}, err
	}
	if !ok {
		return Pending{}, ErrNotYours
	}
	return p, nil
}

// view reads a request for a user; ok is false when it is not his to see.
func (f *Flow) view(ctx context.Context, u users.User, request int64, asked time.Time) (Pending, bool, error) {
	r, err := f.o.Chain.Get(ctx, request)
	if err != nil {
		return Pending{}, false, nil //nolint:nilerr // a request that is not there is not the user's to see
	}
	fr, ok, err := f.row(ctx, request)
	if err != nil || !ok {
		return Pending{}, false, err
	}
	if !Visible(u, r.Workspace, fr.Min) {
		return Pending{}, false, nil
	}
	p, err := f.pending(ctx, u, r, fr, asked)
	return p, err == nil, err
}

// Closure is how the request ended (the zero Closure for one still open).
func (f *Flow) Closure(ctx context.Context, r chain.Request) Closure {
	if !r.State.Closed() {
		return Closure{}
	}
	by, at := f.decider(ctx, r)
	c := Closure{State: r.State, By: by.name, ByID: by.id, At: at}
	if r.Decision != nil {
		c.Answer = r.Decision.Answer
	}
	return c
}

// Eligible are the users who may answer the request now: those whose panel shows it.
func (f *Flow) Eligible(ctx context.Context, request int64) ([]users.User, error) {
	r, err := f.o.Chain.Get(ctx, request)
	if err != nil {
		return nil, err
	}
	fr, ok, err := f.row(ctx, request)
	if err != nil || !ok {
		return nil, err
	}
	all, err := f.o.Users.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []users.User
	for _, u := range all {
		if Visible(u, r.Workspace, fr.Min) {
			out = append(out, u)
		}
	}
	return out, nil
}

// Pendings counts the questions open that the user may answer, each question of
// similar requests once.
func (f *Flow) Pendings(ctx context.Context, u users.User) (int, error) {
	ps, err := f.PendingFor(ctx, u)
	return len(ps), err
}

// DetailText is the whole question as the engine builds it, with the exact target
// of the request, for a user who may see the request (Question checked it).
func DetailText(r chain.Request) string { return DetailTextIn(i18n.PtBR, r) }

// DetailTextIn is DetailText in a language.
func DetailTextIn(lang string, r chain.Request) string { return i18n.Render(lang, detailMsg(r)) }

// detailMsg is DetailText as a message, said in the language of who reads it.
func detailMsg(r chain.Request) i18n.Msg {
	m := chain.Card(r, r.Level, nil).Msg()
	if len(r.Target) > 0 {
		m = i18n.Concat(m, i18n.M("approval.exact_target", "target", string(r.Target)))
	}
	return m
}

// VisibleInHistory says whether the user may read the ended request in the history:
// the one who would have been asked, or, for a request the agents decided alone and
// the people never saw, who acts in its workspace with the level to decide approvals.
func (f *Flow) VisibleInHistory(ctx context.Context, u users.User, r chain.Request) bool {
	min := classes.LevelApprover
	if fr, ok, err := f.row(ctx, r.ID); err == nil && ok {
		min = fr.Min
	}
	return Visible(u, r.Workspace, min)
}
