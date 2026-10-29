package panel

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/notices"
	"github.com/mestre-ia/mestre/internal/users"
)

// The bell of the notices (docs/painel/conversa-e-aprovacoes.md#avisos): what the engine
// tells the users on the Telegram, kept on the Citadel with a mark of what each user read.

func init() { RegisterArea("notices", mountNotices) }

// NoticeSource is the feed the bell reads.
type NoticeSource struct {
	Feed *notices.Feed
}

func mountNotices(r *Router) {
	p := r.Panel()
	r.Auth("GET /api/notices", p.handleNotices)
	r.Auth("POST /api/notices/read", p.handleNoticesRead)
	p.hub.Provide("notices", Topic{
		Authorize: func(u users.User, id string) bool { return u.ID == id },
		Snapshot: func(ctx context.Context, u users.User, _ string) (any, error) {
			if p.notes == nil {
				return NoticesSnapshot{}, nil
			}
			n, err := p.notes.Feed.Unread(ctx, u)
			return NoticesSnapshot{Unread: n}, err
		},
	})
}

// listenNotices tells the bell of each user who sees a new notice.
func (p *Panel) listenNotices() {
	if p.notes == nil || p.notes.Feed == nil {
		return
	}
	p.stop = append(p.stop, p.notes.Feed.OnAdd(func(ctx context.Context, _ notices.Notice, to []users.User) {
		ctx = context.WithoutCancel(ctx)
		for _, u := range to {
			n, err := p.notes.Feed.Unread(ctx, u)
			if err != nil {
				continue
			}
			_ = p.hub.Publish("notices:"+u.ID, "unread", NoticesSnapshot{Unread: n})
		}
	}))
}

func (p *Panel) needNotices(w http.ResponseWriter) bool {
	if p.notes == nil || p.notes.Feed == nil {
		WriteError(w, Err(CodeUnavailable, "api.notices_unavailable", nil))
		return false
	}
	return true
}

// handleNotices is GET /api/notices: the notices the user sees, newest first.
func (p *Panel) handleNotices(w http.ResponseWriter, r *http.Request) {
	if !p.needNotices(w) {
		return
	}
	ctx, u := r.Context(), UserOf(r)
	list, err := p.notes.Feed.For(ctx, u, 100)
	if err != nil {
		p.log.Error("panel: reading the notices", "err", err)
		WriteError(w, errInternal())
		return
	}
	out := NoticeList{Notices: make([]NoticeView, 0, len(list))}
	lang := u.LanguageFor(u.TelegramLanguage)
	for _, n := range list {
		var msg *i18n.Msg
		if !n.Msg.IsZero() {
			m := n.Msg
			msg = &m
		}
		out.Notices = append(out.Notices, NoticeView{ID: n.ID, Kind: string(n.Kind), Workspace: n.Workspace, Table: n.Mesa, Text: n.In(lang), Msg: msg,
			Link: NoticeLink{Kind: n.LinkKind, Ref: n.LinkRef}, CreatedAt: n.At, Read: n.Read})
		if !n.Read {
			out.Unread++
		}
	}
	WriteJSON(w, http.StatusOK, out)
}

// handleNoticesRead is POST /api/notices/read: marks notices as read by the user (all
// of them when no ids come). A notice the user does not see is ignored.
func (p *Panel) handleNoticesRead(w http.ResponseWriter, r *http.Request) {
	if !p.needNotices(w) {
		return
	}
	var body NoticeReadRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.IDs) > 500 {
		WriteError(w, errBadRequest())
		return
	}
	u := UserOf(r)
	if err := p.notes.Feed.MarkRead(r.Context(), u, body.IDs...); err != nil {
		WriteError(w, errInternal())
		return
	}
	n, _ := p.notes.Feed.Unread(r.Context(), u)
	// the other tabs of the user update their bell
	_ = p.hub.Publish("notices:"+u.ID, "unread", NoticesSnapshot{Unread: n})
	w.WriteHeader(http.StatusNoContent)
}
