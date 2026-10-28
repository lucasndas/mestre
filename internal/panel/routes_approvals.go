package panel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mestre-ia/mestre/internal/approvals"
	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/users"
)

// The central of approvals (docs/painel/conversa-e-aprovacoes.md#central-de-aprovações).
// The panel is one more channel of the flow of approval: it reads the questions the
// Telegram gets and gives its answers to the same Flow, which checks the user, the
// level, the state, the target and the lock again at every answer. The browser only
// hides what is not allowed.

func init() { RegisterArea("approvals", mountApprovals) }

// The codes of the errors of the central.
const (
	CodeApprovalClosed   Code = "approval_closed"
	CodeApprovalAnswered Code = "approval_answered"
	CodeApprovalLocked   Code = "approval_locked"
	CodeApprovalTarget   Code = "approval_target_changed"
	CodeApprovalNoVerify Code = "approval_no_verify"
	CodeApprovalExpired  Code = "approval_expired"
	CodeApprovalQuorum   Code = "approval_quorum"
	CodeNoticeUnknown    Code = "notice_unknown"
)

func init() {
	for c, s := range map[Code]int{
		CodeApprovalClosed:   http.StatusConflict,
		CodeApprovalAnswered: http.StatusConflict,
		CodeApprovalLocked:   http.StatusConflict,
		CodeApprovalTarget:   http.StatusConflict,
		CodeApprovalNoVerify: http.StatusConflict,
		CodeApprovalExpired:  http.StatusConflict,
		CodeApprovalQuorum:   http.StatusConflict,
		CodeNoticeUnknown:    http.StatusNotFound,
	} {
		status[c] = s
	}
}

// BlockingWindow is how long a notice that a table stopped stays in the central: it
// has no state of its own, so the central shows the ones of the last days.
const BlockingWindow = 7 * 24 * time.Hour

// ApprovalSource is what the central needs of the engine.
type ApprovalSource struct {
	// Flow is the flow of approval over the people.
	Flow *approvals.Flow
	// Blocking reads the notices that a table stopped (nil: none are shown).
	Blocking *blocking.Store
	// Listen registers fn to be told when a request changes and returns what ends
	// it (nil: the screens are not updated live).
	Listen func(fn func(ctx context.Context, request int64)) func()
	// Mask takes the secrets out of every text of a request before it leaves (the
	// Telegram is masked by its outbox; the panel is masked here, just before the
	// answer, because the text of a request comes as the agent wrote it). Nil: none.
	Mask func(string) string
	// Diff reads the whole diff of the PR a request is about, masked (nil: only the
	// exact target is shown).
	Diff func(ctx context.Context, r chain.Request) ([]ApprovalDiffFile, error)
}

func mountApprovals(r *Router) {
	p := r.Panel()
	r.Require("GET /api/approvals", p.handleApprovals, users.DecideApprovals)
	r.Require("GET /api/approvals/count", p.handleApprovalCount, users.DecideApprovals)
	r.Require("GET /api/approvals/history", p.handleApprovalHistory, users.DecideApprovals)
	r.Require("GET /api/approvals/{id}", p.handleApproval, users.DecideApprovals)
	r.Require("GET /api/approvals/{id}/details", p.handleApprovalDetails, users.DecideApprovals)
	r.Require("POST /api/approvals/{id}/answer", p.handleApprovalAnswer, users.DecideApprovals)
	p.hub.Provide("approvals", Topic{
		Authorize: func(u users.User, id string) bool { return u.ID == id && users.Can(u.Level, users.DecideApprovals) },
		Snapshot: func(ctx context.Context, u users.User, _ string) (any, error) {
			n, err := p.pendingOf(ctx, u)
			return ApprovalsSnapshot{Pending: n}, err
		},
	})
}

// listenApprovals tells the screens that a request changed; it is what the flow
// calls from its own goroutine, so nothing in it waits for a browser.
func (p *Panel) listenApprovals() {
	if p.appr == nil || p.appr.Listen == nil {
		return
	}
	p.stop = append(p.stop, p.appr.Listen(p.approvalChanged))
}

// m masks a text of a request.
func (p *Panel) m(s string) string {
	if p.appr == nil || p.appr.Mask == nil {
		return s
	}
	return p.appr.Mask(s)
}

func (p *Panel) pendingOf(ctx context.Context, u users.User) (int, error) {
	if p.appr == nil || p.appr.Flow == nil {
		return 0, nil
	}
	return p.appr.Flow.Pendings(ctx, u)
}

// approvalChanged publishes a change of a request to everybody who may answer it,
// and the new count of each.
func (p *Panel) approvalChanged(ctx context.Context, request int64) {
	if p.appr == nil || p.appr.Flow == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	r, err := (&chain.Store{DB: p.db}).Get(ctx, request)
	if err != nil {
		return
	}
	to, err := p.appr.Flow.Eligible(ctx, request)
	if err != nil {
		return
	}
	for _, u := range to {
		ev := ApprovalEvent{Request: request, State: string(r.State)}
		if r.State.Closed() {
			ev.Closed = p.closedOf(ctx, u, r)
		}
		topic := "approvals:" + u.ID
		_ = p.hub.Publish(topic, "changed", ev)
		if n, err := p.pendingOf(ctx, u); err == nil {
			_ = p.hub.Publish(topic, "count", ApprovalsSnapshot{Pending: n})
		}
	}
}

func (p *Panel) closedOf(ctx context.Context, u users.User, r chain.Request) *ApprovalClosed {
	c := p.appr.Flow.Closure(ctx, r)
	at := c.At
	if at.IsZero() {
		at = r.ClosedAt
	}
	return &ApprovalClosed{State: string(c.State), By: c.By, ByID: c.ByID, At: at, Answer: string(c.Answer), Self: c.ByID != "" && c.ByID == u.ID}
}

// splitAgent separates the block of the text of the agents from the text of the engine.
func splitAgent(text string) (engine, agent string) {
	head, tail, ok := strings.Cut(text, chain.UnverifiedOpen)
	if !ok {
		return strings.TrimRight(text, "\n"), ""
	}
	inner, _, _ := strings.Cut(tail, chain.UnverifiedClose)
	return strings.TrimRight(head, "\n"), strings.Trim(inner, "\n")
}

func orPermission(t classes.RequestType) string {
	if t == "" {
		return string(classes.RequestClassPermission)
	}
	return string(t)
}

const defaultDelegationDays = 30

// field is the data an answer asks before it is confirmed.
func field(a chain.Answer) string {
	switch a {
	case chain.AnswerApproveFor:
		return "duration"
	case chain.AnswerApprovePattern:
		return "pattern"
	case chain.AnswerDenyInstruction, chain.AnswerAskBack:
		return "text"
	case chain.AnswerDelegate:
		return "scope"
	}
	return ""
}

// approvalItem is the request as the user reads it: the question, and the answers he
// is given by his level and by its type.
func (p *Panel) approvalItem(ctx context.Context, u users.User, pd approvals.Pending) ApprovalItem {
	r := pd.Request
	engine, agent := splitAgent(p.m(pd.Text))
	it := ApprovalItem{
		ID: r.ID, Workspace: r.Workspace, Table: r.Table, Type: orPermission(r.Type), Risk: string(r.Risk.Risk), State: string(r.State),
		About: p.m(r.About), Text: engine, Agent: agent, Requester: p.m(r.RequesterName), CreatedAt: r.CreatedAt.UTC(),
		Approvals: pd.Approvals, Counted: pd.Counted, Voted: pd.Voted, Answers: []ApprovalAnswer{}, Members: []ApprovalMember{},
	}
	if !r.Deadline.IsZero() {
		d := r.Deadline.UTC()
		it.Deadline = &d
	}
	if r.State.Closed() {
		it.Closed = p.closedOf(ctx, u, r)
		return it
	}
	suggested := approvals.SuggestPattern(r)
	if len(pd.Members) > 1 {
		if g := approvals.SuggestGroupPattern(pd.Members); g != "" {
			suggested = g
		}
		for _, m := range pd.Members {
			it.Members = append(it.Members, ApprovalMember{ID: m.ID, About: p.m(m.About)})
		}
	}
	if p.m(suggested) != suggested {
		suggested = "" // a pattern that carries a secret is never suggested
	}
	for _, a := range p.appr.Flow.AnswersOf(u, r, pd) {
		x := ApprovalAnswer{Answer: string(a), Field: field(a)}
		if a == chain.AnswerApprovePattern {
			x.Suggested = suggested
		}
		x.NeedsPIN, _ = p.appr.Flow.NeedsPIN(ctx, u.ID, approvals.Input{Request: r.ID, Answer: a})
		it.Answers = append(it.Answers, x)
		if a == chain.AnswerDelegate {
			it.Scope = &ApprovalScope{Workspace: r.Workspace, Table: r.Table, Type: orPermission(r.Type), Days: defaultDelegationDays}
		}
	}
	return it
}

func (p *Panel) blockingView(u users.User, n blocking.Saved) BlockingView {
	v := BlockingView{ID: n.ID, Workspace: n.Workspace, Table: n.Mesa, Motive: string(n.Motive), Stopped: p.m(n.Stopped), Evidence: p.m(n.Evidence),
		Owner: p.m(n.Owner), Agent: p.m(n.Agent), CreatedAt: n.At, Steps: []BlockingStep{}}
	for _, s := range n.Steps {
		st := BlockingStep{Text: p.m(s.Text)}
		if s.Kind == blocking.StepVault {
			st.Action, st.CanDo = "open_vault", blocking.CanDo(s.Kind, u.Level)
		}
		v.Steps = append(v.Steps, st)
	}
	return v
}

func (p *Panel) needApprovals(w http.ResponseWriter) bool {
	if p.appr == nil || p.appr.Flow == nil {
		WriteError(w, Err(CodeUnavailable, "api.approvals_unavailable", nil))
		return false
	}
	return true
}

// handleApprovals is GET /api/approvals?workspace=&table=&type=: the requests that wait
// for the user across his workspaces, newest first, and the notices that a table
// stopped (type=aviso is only those).
func (p *Panel) handleApprovals(w http.ResponseWriter, r *http.Request) {
	if !p.needApprovals(w) {
		return
	}
	ctx, u, q := r.Context(), UserOf(r), r.URL.Query()
	ws, table, typ := q.Get("workspace"), q.Get("table"), q.Get("type")
	pend, err := p.appr.Flow.PendingFor(ctx, u)
	if err != nil {
		p.log.Error("panel: reading the approvals", "err", err)
		WriteError(w, errInternal())
		return
	}
	out := ApprovalList{Requests: []ApprovalItem{}, Blocking: []BlockingView{}, Pending: len(pend)}
	for _, pd := range pend {
		rq := pd.Request
		if typ == "aviso" || (ws != "" && rq.Workspace != ws) || (table != "" && rq.Table != table) || (typ != "" && orPermission(rq.Type) != typ) {
			continue
		}
		out.Requests = append(out.Requests, p.approvalItem(ctx, u, pd))
	}
	if p.appr.Blocking != nil && (typ == "" || typ == "aviso") {
		list, err := p.appr.Blocking.Since(ctx, p.clk.Now().Add(-BlockingWindow))
		if err != nil {
			p.log.Error("panel: reading the notices that block", "err", err)
			WriteError(w, errInternal())
			return
		}
		for _, n := range list {
			if !blocking.Sees(u, n.Workspace) || (ws != "" && n.Workspace != ws) || (table != "" && n.Mesa != table) {
				continue
			}
			out.Blocking = append(out.Blocking, p.blockingView(u, n))
		}
	}
	WriteJSON(w, http.StatusOK, out)
}

// handleApprovalCount is GET /api/approvals/count.
func (p *Panel) handleApprovalCount(w http.ResponseWriter, r *http.Request) {
	n, err := p.pendingOf(r.Context(), UserOf(r))
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	WriteJSON(w, http.StatusOK, ApprovalCount{Pending: n})
}

func requestID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		WriteError(w, Err(CodeNotFound, "api.request_not_found", nil))
		return 0, false
	}
	return id, true
}

// handleApproval is GET /api/approvals/{id}: one request, open or ended.
func (p *Panel) handleApproval(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r)
	if !ok || !p.needApprovals(w) {
		return
	}
	pd, err := p.appr.Flow.Question(r.Context(), UserOf(r), id)
	if err != nil {
		p.writeApprovalError(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, p.approvalItem(r.Context(), UserOf(r), pd))
}

// handleApprovalDetails is GET /api/approvals/{id}/details: "Ver detalhes", the whole
// question with the exact target and, for a PR, the whole diff.
func (p *Panel) handleApprovalDetails(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r)
	if !ok || !p.needApprovals(w) {
		return
	}
	ctx := r.Context()
	pd, err := p.appr.Flow.Question(ctx, UserOf(r), id)
	if err != nil {
		p.writeApprovalError(w, err)
		return
	}
	out := ApprovalDetails{Text: p.m(approvals.DetailText(pd.Request)), Files: []ApprovalDiffFile{}}
	if pd.Request.PR != nil && p.appr.Diff != nil {
		files, err := p.appr.Diff(ctx, pd.Request)
		if err != nil {
			p.log.Warn("panel: the diff of a request was not read", "request", id, "err", err)
			out.DiffUnavailable = true
		} else {
			for _, f := range files {
				f.Patch = p.m(f.Patch)
				out.Files = append(out.Files, f)
			}
		}
	}
	WriteJSON(w, http.StatusOK, out)
}

// parseValidity reads a time like 90m, 3h or 2d, as the Telegram does.
func parseValidity(s string) (time.Duration, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil || days <= 0 || days > 365 {
			return 0, false
		}
		return time.Duration(days) * 24 * time.Hour, true
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 || d > 365*24*time.Hour {
		return 0, false
	}
	return d, true
}

const maxAnswerText = 2000

// handleApprovalAnswer is POST /api/approvals/{id}/answer. The level of the answer is
// checked here (the Owner delegates, an Administrator makes the rules that stay) and
// again by the flow; an answer that asks the PIN asks it in this very request.
func (p *Panel) handleApprovalAnswer(w http.ResponseWriter, r *http.Request) {
	id, ok := requestID(w, r)
	if !ok || !p.needApprovals(w) {
		return
	}
	var body ApprovalAnswerRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, errBadRequest())
		return
	}
	ans := chain.Answer(body.Answer)
	if !ans.Valid() || ans == chain.AnswerClimb {
		WriteError(w, errBadRequest())
		return
	}
	u := UserOf(r)
	// the user sees the request at all: another workspace or a level below is not found
	pd, err := p.appr.Flow.Question(r.Context(), u, id)
	if err != nil {
		p.writeApprovalError(w, err)
		return
	}
	capab := approvals.CapabilityFor(pd.Request, ans)
	if err := users.Require(u.Level, capab); err != nil {
		params := map[string]any{"capability": string(capab)}
		var le *users.LevelError
		if errors.As(err, &le) {
			params["need"], params["have"] = string(le.Need), string(le.Have)
		}
		WriteError(w, errForbidden(err, params))
		return
	}
	// the answers of the type of the request, as the screen gives them: a rule for the
	// class only for a permission with an action, Delegar only for risk alto or medio
	if !pd.Request.State.Closed() && !slices.Contains(p.appr.Flow.AnswersOf(u, pd.Request, pd), ans) {
		WriteError(w, ErrMsg(i18n.M("approval.answer_not_offered")).WithStatus(http.StatusUnprocessableEntity))
		return
	}
	in := approvals.Input{Request: id, Answer: ans}
	switch ans {
	case chain.AnswerDenyInstruction, chain.AnswerAskBack:
		in.Instruction = strings.TrimSpace(body.Text)
		if in.Instruction == "" || len(in.Instruction) > maxAnswerText {
			WriteError(w, errBadRequest())
			return
		}
	case chain.AnswerApprovePattern:
		in.Pattern = strings.TrimSpace(body.Pattern)
		if in.Pattern == "" || len(in.Pattern) > maxAnswerText {
			WriteError(w, errBadRequest())
			return
		}
	case chain.AnswerApproveFor:
		d, ok := parseValidity(body.For)
		if !ok {
			WriteError(w, errBadRequest())
			return
		}
		in.For = d
	}
	need, err := p.appr.Flow.NeedsPIN(r.Context(), u.ID, in)
	if err != nil {
		p.writeApprovalError(w, err)
		return
	}
	apply := func(w http.ResponseWriter, r *http.Request) {
		_, err := p.appr.Flow.AnswerWithPIN(r.Context(), u.ID, in, need)
		if err != nil {
			p.writeApprovalError(w, err)
			return
		}
		p.audit(r.Context(), "panel.approval.answered", map[string]any{"user_id": u.ID, "request": id, "answer": string(ans), "session": SessionOf(r).ID, "ip": clientIP(r)})
		state := "open"
		if rq, err := (&chain.Store{DB: p.db}).Get(r.Context(), id); err == nil && rq.State.Closed() {
			state = string(rq.State)
		}
		WriteJSON(w, http.StatusOK, ApprovalAnswered{State: state})
	}
	if need {
		p.withPIN("approval.answer", apply)(w, r)
		return
	}
	apply(w, r)
}

// writeApprovalError turns what the flow refused into the code of the API.
func (p *Panel) writeApprovalError(w http.ResponseWriter, err error) {
	var ref *chain.Refusal
	switch {
	case errors.Is(err, approvals.ErrNotYours):
		WriteError(w, Err(CodeNotFound, "api.request_not_found", nil))
	case errors.Is(err, approvals.ErrNotOpen):
		WriteError(w, Err(CodeApprovalClosed, "api.approval_closed", nil))
	case errors.Is(err, approvals.ErrAnswered):
		WriteError(w, Err(CodeApprovalAnswered, "api.approval_answered", nil))
	case errors.Is(err, approvals.ErrLevel):
		WriteError(w, Err(CodeForbiddenLevel, "api.approval_level", nil))
	case errors.Is(err, approvals.ErrLocked):
		WriteError(w, Err(CodeApprovalLocked, "api.approval_locked", nil))
	case errors.Is(err, approvals.ErrTargetChanged):
		WriteError(w, Err(CodeApprovalTarget, "api.approval_target", nil))
	case errors.Is(err, approvals.ErrNoVerify):
		WriteError(w, Err(CodeApprovalNoVerify, "api.approval_no_verify", nil))
	case errors.Is(err, approvals.ErrExpired):
		WriteError(w, Err(CodeApprovalExpired, "api.approval_expired", nil))
	case errors.Is(err, approvals.ErrQuorumEffect):
		WriteError(w, Err(CodeApprovalQuorum, "api.approval_quorum", nil))
	case errors.Is(err, approvals.ErrPINNeeded), errors.Is(err, approvals.ErrPIN):
		WriteError(w, Err(CodePINRequired, "api.pin_required", nil))
	case errors.As(err, &ref):
		WriteError(w, ErrMsg(ref.Msg).WithStatus(http.StatusUnprocessableEntity))
	default:
		p.log.Error("panel: answering an approval", "err", err)
		WriteError(w, errInternal())
	}
}

// historyPage is how many requests are read from the database at a time while the
// filters that need the whole request look for the ones that fit.
const historyPage = 100

// handleApprovalHistory is GET /api/approvals/history: the requests that ended, read
// from the audit of the flow, with the filters state, by (a user id, or "agent"),
// level (agent, mesa, grand_master, people), risk, workspace, from and to (dates).
func (p *Panel) handleApprovalHistory(w http.ResponseWriter, r *http.Request) {
	if !p.needApprovals(w) {
		return
	}
	ctx, u, q := r.Context(), UserOf(r), r.URL.Query()
	limit := 30
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 && n <= 100 {
		limit = n
	}
	before, _ := strconv.ParseInt(q.Get("before"), 10, 64)
	var from, to time.Time
	if v := q.Get("from"); v != "" {
		t, err := time.Parse(time.DateOnly, v)
		if err != nil {
			WriteError(w, errBadRequest())
			return
		}
		from = t
	}
	if v := q.Get("to"); v != "" {
		t, err := time.Parse(time.DateOnly, v)
		if err != nil {
			WriteError(w, errBadRequest())
			return
		}
		to = t.Add(24 * time.Hour)
	}
	state, by, level, risk, ws := q.Get("state"), q.Get("by"), q.Get("level"), q.Get("risk"), q.Get("workspace")
	store := &chain.Store{DB: p.db}
	out := ApprovalHistory{Items: []ApprovalHistoryItem{}}
	names := map[string]string{}
	cursor := before
	for round := 0; round < 10 && len(out.Items) < limit; round++ {
		ids, err := p.endedRequests(ctx, u, cursor, state, level, ws, historyPage)
		if err != nil {
			p.log.Error("panel: reading the history", "err", err)
			WriteError(w, errInternal())
			return
		}
		if len(ids) == 0 {
			break
		}
		for _, id := range ids {
			cursor = id
			rq, err := store.Get(ctx, id)
			if err != nil || !p.appr.Flow.VisibleInHistory(ctx, u, rq) {
				continue
			}
			if risk != "" && string(rq.Risk.Risk) != risk {
				continue
			}
			if (!from.IsZero() && rq.ClosedAt.Before(from)) || (!to.IsZero() && !rq.ClosedAt.Before(to)) {
				continue
			}
			item := p.historyItem(ctx, rq, names)
			switch {
			case by == "agent" && item.ByUser, by == "agent" && item.By == "":
				continue
			case by != "" && by != "agent" && (!item.ByUser || rq.Decision == nil || rq.Decision.By != "user:"+by):
				continue
			}
			out.Items = append(out.Items, item)
			if len(out.Items) == limit {
				out.Next = id
				break
			}
		}
	}
	WriteJSON(w, http.StatusOK, out)
}

func (p *Panel) endedRequests(ctx context.Context, u users.User, before int64, state, level, workspace string, n int) ([]int64, error) {
	q := `SELECT id FROM approval_requests WHERE state <> 'open'`
	var args []any
	if before > 0 {
		q += ` AND id < ?`
		args = append(args, before)
	}
	if state != "" {
		q += ` AND state = ?`
		args = append(args, state)
	}
	if level != "" {
		q += ` AND level_kind = ?`
		args = append(args, level)
	}
	if workspace != "" {
		q += ` AND workspace = ?`
		args = append(args, workspace)
	}
	if u.Level != users.Owner {
		// only the workspaces the user acts in: the rest is not even read
		if len(u.Workspaces) == 0 {
			return nil, nil
		}
		q += ` AND workspace IN (` + strings.TrimSuffix(strings.Repeat("?,", len(u.Workspaces)), ",") + `)`
		for _, w := range u.Workspaces {
			args = append(args, w)
		}
	}
	q += ` ORDER BY id DESC LIMIT ?`
	args = append(args, n)
	rows, err := p.db.Reader().QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (p *Panel) historyItem(ctx context.Context, r chain.Request, names map[string]string) ApprovalHistoryItem {
	it := ApprovalHistoryItem{ID: r.ID, Workspace: r.Workspace, Table: r.Table, Type: orPermission(r.Type), Risk: string(r.Risk.Risk), State: string(r.State),
		About: p.m(r.About), CreatedAt: r.CreatedAt.UTC(), ClosedAt: r.ClosedAt.UTC(), LevelKind: string(r.Level.Kind), Level: r.Level.Label()}
	d := r.Decision
	if d == nil {
		return it
	}
	it.Answer, it.Reason = string(d.Answer), p.m(d.Reason)
	if d.Level.Kind != "" {
		it.LevelKind, it.Level = string(d.Level.Kind), d.Level.Label()
	}
	switch id, isUser := strings.CutPrefix(d.By, "user:"); {
	case d.By == "":
	case isUser:
		it.ByUser, it.ByID = true, id
		if n, ok := names[d.By]; ok {
			it.By = n
			break
		}
		it.By = id
		if u, err := p.users.Get(ctx, id); err == nil {
			it.By = u.Name
		}
		names[d.By] = it.By
	default:
		if n, ok := names[d.By]; ok {
			it.By = n
			break
		}
		it.By = d.By
		if c, err := (characters.Registry{DB: p.db}).Get(ctx, d.By); err == nil && c.Name != "" {
			it.By = c.Name
		}
		names[d.By] = it.By
	}
	return it
}
