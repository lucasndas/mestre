package panel

import (
	"net/http"

	"github.com/mestre-ia/mestre/internal/users"
)

func init() { RegisterArea("sessions", mountSessions) }

func mountSessions(r *Router) {
	p := r.Panel()
	r.Auth("GET /api/sessions", p.handleSessions)
	r.Require("GET /api/admin/sessions", p.handleAllSessions, users.ManageUsers)
	r.Auth("DELETE /api/sessions/{id}", p.handleEndSession)
}

// handleSessions lists the sessions of the user.
func (p *Panel) handleSessions(w http.ResponseWriter, r *http.Request) {
	list, err := p.sessions.List(r.Context(), UserOf(r).ID)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	p.answerSessions(w, r, list)
}

// handleAllSessions lists the sessions of everyone: the Owner.
func (p *Panel) handleAllSessions(w http.ResponseWriter, r *http.Request) {
	list, err := p.sessions.List(r.Context(), "")
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	p.answerSessions(w, r, list)
}

func (p *Panel) answerSessions(w http.ResponseWriter, r *http.Request, list []SessionInfo) {
	cur := SessionOf(r).ID
	if list == nil {
		list = []SessionInfo{}
	}
	for i := range list {
		list[i].Current = list[i].ID == cur
	}
	WriteJSON(w, http.StatusOK, SessionList{Sessions: list})
}

// handleEndSession ends a session: any user's own, and the Owner's of anyone.
func (p *Panel) handleEndSession(w http.ResponseWriter, r *http.Request) {
	ctx, u, id := r.Context(), UserOf(r), r.PathValue("id")
	owner, err := p.sessions.Owner(ctx, id)
	if err != nil {
		WriteError(w, errInternal())
		return
	}
	if owner == "" || (owner != u.ID && !users.Can(u.Level, users.ManageUsers)) {
		// somebody else's session is not there for those who may not end it
		WriteError(w, Err(CodeNotFound, "api.session_not_found", nil))
		return
	}
	if err := p.sessions.End(ctx, id); err != nil {
		WriteError(w, errInternal())
		return
	}
	p.audit(ctx, "panel.session.ended", map[string]any{"user_id": u.ID, "owner": owner, "session": id, "ip": clientIP(r)})
	if id == SessionOf(r).ID {
		p.clearCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}
