package approvals

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/providers"
)

// Similar requests (docs/aprovacoes/fluxo.md#quórum-e-já-respondido-por): requests
// open with the people, of the same table and the same type, whose shell actions
// match one approvable pattern go in one question to each user, which lists every
// action. The group is the set of requests that share the message of a user (the
// same reference in approval_messages); each request keeps its own id, target and
// impression, and one that no longer fits leaves the group and is asked again alone.

// groupable says whether two requests may share a question: open class
// permissions of shell commands of the same workspace, table and risk.
func groupable(a, b chain.Request) bool {
	return a.ID != b.ID && a.Workspace == b.Workspace && a.Table == b.Table && a.Class == b.Class &&
		a.Type == b.Type && (a.Type == "" || a.Type == classes.RequestClassPermission) &&
		a.Action != nil && b.Action != nil && a.Action.Kind == providers.KindShell && b.Action.Kind == providers.KindShell &&
		a.Risk.Risk == b.Risk.Risk && !a.State.Closed() && !b.State.Closed() && a.Level.Kind == chain.LevelPeople && b.Level.Kind == chain.LevelPeople
}

// groupPattern is the approvable pattern the actions of the requests have in
// common: their longest shared words and "*", accepted only when each action is
// matched, the pattern is not too broad and it reaches no more risk than the
// requests themselves have.
func groupPattern(rs []chain.Request) string {
	if len(rs) < 2 {
		return ""
	}
	prefix := strings.Fields(rs[0].Action.Command)
	for _, r := range rs[1:] {
		words := strings.Fields(r.Action.Command)
		n := 0
		for n < len(prefix) && n < len(words) && prefix[n] == words[n] {
			n++
		}
		prefix = prefix[:n]
	}
	if len(prefix) == 0 {
		return ""
	}
	pattern := strings.Join(prefix, " ") + " *"
	for _, r := range rs {
		ap, err := classes.CheckApprovablePattern(pattern, *r.Action, r.Class, r.Table)
		if err != nil || ap.Risk.Risk != r.Risk.Risk {
			return ""
		}
	}
	return pattern
}

// groupOf are the open requests that share with request the message of the user,
// in the order of their ids; the request itself is among them.
func (f *Flow) groupOf(ctx context.Context, user string, request int64) ([]chain.Request, error) {
	ref, ok, err := f.ref(ctx, request, user)
	if err != nil || !ok {
		return nil, err
	}
	rows, err := f.o.DB.Reader().QueryContext(ctx, `SELECT request FROM approval_messages WHERE user_id = ? AND ref = ? ORDER BY request`, user, ref)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()
	var out []chain.Request
	for _, id := range ids {
		r, err := f.o.Chain.Get(ctx, id)
		if err != nil || r.State.Closed() || r.Level.Kind != chain.LevelPeople {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// joinGroup puts the request in the question the user already has about similar
// requests, when there is one: the message is edited to list this action too.
func (f *Flow) joinGroup(ctx context.Context, q chain.PeopleQuestion, user string, answers []chain.Answer) bool {
	if q.Request.Action == nil || q.Request.Action.Kind != providers.KindShell {
		return false
	}
	rows, err := f.o.DB.Reader().QueryContext(ctx, `SELECT m.request FROM approval_messages m JOIN approval_requests r ON r.id = m.request WHERE m.user_id = ? AND m.request != ? AND r.state = 'open' ORDER BY m.request`, user, q.Request.ID)
	if err != nil {
		return false
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		other, err := f.o.Chain.Get(ctx, id)
		if err != nil || !groupable(q.Request, other) {
			continue
		}
		members, err := f.groupOf(ctx, user, id)
		if err != nil || len(members) == 0 {
			continue
		}
		all := append(slices.Clone(members), q.Request)
		if len(all) > maxGroup || !slices.ContainsFunc(members, func(m chain.Request) bool { return groupable(q.Request, m) }) || groupPattern(all) == "" {
			continue
		}
		// the members answer together, so they must ask the same of who answers, and none
		// may be waiting for a PIN (the one who types it would approve what joined meanwhile)
		if !f.sameAsk(ctx, q.Request.ID, members) || f.pinPending(ctx, members) {
			continue
		}
		ref, _, err := f.ref(ctx, id, user)
		if err != nil {
			continue
		}
		// the message is rewritten first: a request enters the group only when the user
		// can read it there
		if !f.editGroup(ctx, user, ref, all, answers) {
			continue
		}
		if err := f.setRef(ctx, q.Request.ID, user, ref, answers); err != nil {
			f.editGroup(ctx, user, ref, members, answers)
			continue
		}
		return true
	}
	return false
}

// maxGroup is how many requests share a question, and maxGroupText the longest
// text of it: the channel cuts a longer message, and a request in the cut part
// would be answered without being read.
const (
	maxGroup     = 8
	maxGroupText = 3000
)

// sameAsk says whether the flow rows of the requests ask the same of who answers
// (the approvals, the users, the minimum level and the PIN).
func (f *Flow) sameAsk(ctx context.Context, request int64, members []chain.Request) bool {
	a, ok, err := f.row(ctx, request)
	if err != nil || !ok {
		return false
	}
	for _, m := range members {
		b, ok, err := f.row(ctx, m.ID)
		if err != nil || !ok || a.Approvals != b.Approvals || a.Min != b.Min || a.PIN != b.PIN || !slices.Equal(a.Require, b.Require) {
			return false
		}
	}
	return true
}

// pinPending says whether a PIN waits to approve any of the requests.
func (f *Flow) pinPending(ctx context.Context, members []chain.Request) bool {
	for _, m := range members {
		var n int
		if err := f.o.DB.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM approval_pins WHERE request = ?`, m.ID).Scan(&n); err != nil || n > 0 {
			return true
		}
	}
	return false
}

// groupText is the question of the first request and the list of the actions.
func groupText(question string, members []chain.Request) string {
	var b strings.Builder
	b.WriteString(question)
	fmt.Fprintf(&b, "\n\nPedidos parecidos, na mesma pergunta (%d):\n", len(members))
	for _, m := range members {
		cmd := inbox.OneLine(m.Action.Command)
		if r := []rune(cmd); len(r) > 200 {
			cmd = string(r[:200]) + "…"
		}
		fmt.Fprintf(&b, "- #%d: %s\n", m.ID, cmd)
	}
	b.WriteString("Aprovar agora e Negar valem para todos os listados")
	if p := groupPattern(members); p != "" {
		fmt.Fprintf(&b, "; Aprovar o padrão vale para os que casam o padrão (por exemplo %s)", inbox.OneLine(p))
	}
	b.WriteString(".")
	return b.String()
}

// editGroup rewrites the shared message: the question of its first request and
// the list of the actions, the buttons answering for the first of them. It says
// whether the user can read it as written.
func (f *Flow) editGroup(ctx context.Context, user, ref string, members []chain.Request, answers []chain.Answer) bool {
	lead := members[0]
	fr, ok, err := f.row(ctx, lead.ID)
	if err != nil || !ok {
		return false
	}
	text := fr.Text
	if len(members) > 1 {
		text = groupText(fr.Text, members)
		if len([]rune(text)) > maxGroupText {
			return false
		}
	}
	if err := f.o.Messenger.Edit(ctx, MessageEdit{UserID: user, Ref: ref, Text: text, Request: lead.ID, Answers: answers}); err != nil {
		f.log.Warn("approvals: the question of similar requests was not updated", "request", lead.ID, "user", user, "err", err)
		return false
	}
	return true
}

// edit rewrites the message of a request for a user: when the message is shared
// with other open requests it is the question of the group, never one request's.
func (f *Flow) edit(ctx context.Context, e MessageEdit) error {
	if members, err := f.groupOf(ctx, e.UserID, e.Request); err == nil && len(members) > 1 {
		if f.editGroup(ctx, e.UserID, e.Ref, members, e.Answers) {
			return nil
		}
		return errors.New("approvals: the question of the group was not written")
	}
	return f.o.Messenger.Edit(ctx, e)
}

// regroup is called when a request ended: the users who read it in a question
// shared with other open requests get that question again, without it and with
// the buttons for the next one. It says whether it did, for the user.
func (f *Flow) regroup(ctx context.Context, request int64, m recipient) bool {
	members, err := f.groupOf(ctx, m.user, request)
	if err != nil {
		return false
	}
	rest := slices.DeleteFunc(slices.Clone(members), func(r chain.Request) bool { return r.ID == request })
	if len(rest) == 0 {
		return false
	}
	f.editGroup(ctx, m.user, m.ref, rest, m.answers)
	return true
}

// leaveGroup takes the request out of the shared question and asks the user about
// it alone, with a message of its own.
func (f *Flow) leaveGroup(ctx context.Context, user string, r chain.Request) {
	fr, ok, err := f.row(ctx, r.ID)
	if err != nil || !ok {
		return
	}
	var answersJSON string
	if err := f.o.DB.Reader().QueryRowContext(ctx, `SELECT answers FROM approval_messages WHERE request = ? AND user_id = ?`, r.ID, user).Scan(&answersJSON); err != nil {
		return
	}
	var answers []chain.Answer
	if err := jsonUnmarshal(answersJSON, &answers); err != nil {
		return
	}
	if err := f.o.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM approval_messages WHERE request = ? AND user_id = ?`, r.ID, user)
		return err
	}); err != nil {
		return
	}
	text, err := f.render(ctx, r.ID, fr.Text, fr.Approvals)
	if err != nil {
		return
	}
	ref, err := f.o.Messenger.Send(ctx, Message{UserID: user, Request: r, Text: text, Answers: answers})
	if err != nil {
		f.log.Warn("approvals: the request that left the group was not asked again", "request", r.ID, "user", user, "err", err)
		return
	}
	_ = f.setRef(ctx, r.ID, user, ref, answers)
}

// fits says whether a request still goes with the answer given to its group: the
// live target is still the one it was bound to (a request that changed is asked
// again, not invalidated by someone else's answer) and, for a pattern, the pattern
// covers its action.
func (f *Flow) fits(ctx context.Context, r chain.Request, in Input) bool {
	if !in.Answer.Approves() {
		return true // a denial stands whatever the target became
	}
	if r.Impression != "" && f.o.Targets != nil {
		cur, err := f.o.Targets.Current(ctx, r)
		if err != nil {
			return false
		}
		if imp, err := chain.Impress(cur); err != nil || imp != r.Impression {
			return false
		}
	}
	if in.Answer == chain.AnswerApprovePattern {
		if r.Action == nil {
			return false
		}
		if _, err := classes.CheckApprovablePattern(in.Pattern, *r.Action, r.Class, r.Table); err != nil {
			return false
		}
	}
	return true
}

// groupAnswers are the answers that stand for every request of the question:
// "Aprovar agora", "Negar" (also with the instruction) and "Aprovar o padrão", this
// one for the requests the pattern covers. Any other answer is for the request the
// button was made for.
func groupAnswers(a chain.Answer) bool {
	switch a {
	case chain.AnswerApprove, chain.AnswerDeny, chain.AnswerDenyInstruction, chain.AnswerApprovePattern:
		return true
	}
	return false
}

// applyGroup applies the answer to every request of the group it stands for. The
// ones that no longer fit leave the group first, so the shared message ends only
// with the requests that were answered. The pattern is made a decree once, with
// the first request it covers; the other requests it covers are approved as
// covered by it.
func (f *Flow) applyGroup(ctx context.Context, userID string, members []chain.Request, in Input) (Reply, error) {
	var fitting []chain.Request
	for _, m := range members {
		if f.fits(ctx, m, in) {
			fitting = append(fitting, m)
			continue
		}
		f.leaveGroup(ctx, userID, m)
	}
	if len(fitting) == 0 {
		return Reply{}, ErrTargetChanged
	}
	var reply Reply
	var firstErr error
	done := 0
	for _, m := range fitting {
		one := in
		one.Request = m.ID
		if in.Answer == chain.AnswerApprovePattern && done > 0 {
			one.Answer, one.Pattern = chain.AnswerApprove, ""
			one.Reason = strings.TrimSpace(in.Reason + fmt.Sprintf(" (coberto pelo padrão do pedido %d)", fitting[0].ID))
		}
		u, r, fr, err := f.check(ctx, userID, m.ID, one.Answer)
		if err == nil {
			var rep Reply
			if rep, err = f.apply(ctx, u, r, fr, one); err == nil {
				if done == 0 {
					reply = rep
				}
				done++
				continue
			}
		}
		if firstErr == nil {
			firstErr = err
		}
		f.log.Warn("approvals: a request of the group was not answered", "request", m.ID, "user", userID, "err", err)
	}
	if done == 0 {
		return Reply{}, firstErr
	}
	return reply, nil
}
