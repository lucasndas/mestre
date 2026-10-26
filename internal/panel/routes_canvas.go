package panel

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/mestre-ia/mestre/internal/users"
)

// The canvas of a table (docs/painel/canvas.md): the panel reads the state of the
// table and acts through the same services of the engine the CLI uses, behind
// the CanvasSource. The server checks the level at every action; the buttons of
// the front end are only a convenience.

// canvasQuiet is how long the commits to the database must pause before the
// subscribers of a canvas are told it changed.
const canvasQuiet = 150 * time.Millisecond

// Errors of a CanvasSource.
var (
	// ErrCanvasNotFound: the table, or the element, does not exist.
	ErrCanvasNotFound = errors.New("panel: no such element on the canvas")
	// ErrCanvasConflict: the element changed since the browser read it.
	ErrCanvasConflict = errors.New("panel: the element changed")
)

// Refusal is an action the engine refused, with the reason it gives: Reason is a
// stable key the front end translates ("automatic_connection"), Message the text
// of the engine and Params what the key needs (the reasons of the connection).
type Refusal struct {
	Reason  string
	Message string
	Params  map[string]any
}

func (r *Refusal) Error() string { return r.Reason + ": " + r.Message }

// CanvasSource is the engine, as the canvas sees it. A method that acts takes the
// user whose level the engine checks again.
type CanvasSource interface {
	// State reads the table; Layout is complete (every element has a place).
	State(ctx context.Context, workspace, table string) (CanvasState, error)
	SaveLayout(ctx context.Context, workspace, table string, req LayoutRequest) error
	Connect(ctx context.Context, by users.User, workspace, table string, a, b CanvasRef) error
	Disconnect(ctx context.Context, by users.User, workspace, table string, a, b CanvasRef) error
	Pin(ctx context.Context, by users.User, workspace, table, character string, pinned bool) error
	WriteScroll(ctx context.Context, by users.User, workspace, table, scroll string, req ScrollWriteRequest) (CanvasScroll, error)
	CreateQuest(ctx context.Context, by users.User, workspace, table string, req QuestRequest) (CanvasQuest, error)
	EditQuest(ctx context.Context, by users.User, workspace, table, quest string, req QuestRequest) (CanvasQuest, error)
	Grimoires(ctx context.Context, by users.User) ([]GrimoireInfo, error)
	SaveGrimoire(ctx context.Context, by users.User, workspace, table string, req GrimoireSaveRequest) (GrimoireInfo, error)
	RestoreGrimoire(ctx context.Context, by users.User, workspace, table string, req GrimoireRestoreRequest) (GrimoirePlan, error)
	Rituals(ctx context.Context, workspace, table string) ([]RitualInfo, error)
	EditRitual(ctx context.Context, by users.User, workspace, table, ritual string, req RitualEditRequest) (RitualInfo, error)
}

type canvasHolder struct{ src CanvasSource }

// SetCanvas gives the panel the engine of the tables; until then the canvas
// answers that it is not available.
func (p *Panel) SetCanvas(src CanvasSource) { p.canvas.Store(&canvasHolder{src}) }

func (p *Panel) canvasSource() CanvasSource {
	if h := p.canvas.Load(); h != nil {
		return h.src
	}
	return nil
}

func init() { RegisterArea("canvas", mountCanvas) }

type canvasArea struct{ p *Panel }

const canvasBase = "/api/workspaces/{ws}/tables/{table}/canvas"

func mountCanvas(r *Router) {
	p := r.Panel()
	a := &canvasArea{p: p}
	p.hub.Provide("canvas", Topic{Authorize: a.authorize, Snapshot: a.snapshot})
	r.Require("GET "+canvasBase, a.state, users.Status)
	r.Require("PUT "+canvasBase+"/layout", a.layout, users.Status)
	r.Require("POST "+canvasBase+"/connections", a.connect(true), users.EditCanvas)
	r.Require("DELETE "+canvasBase+"/connections", a.connect(false), users.EditCanvas)
	r.Require("PUT "+canvasBase+"/characters/{id}/pin", a.pin, users.EditCanvas)
	r.Require("PUT "+canvasBase+"/scrolls/{id}", a.scroll, users.EditCanvas)
	r.Require("POST "+canvasBase+"/quests", a.quest(false), users.EditCanvas)
	r.Require("PUT "+canvasBase+"/quests/{id}", a.quest(true), users.EditCanvas)
	r.Require("GET /api/grimoires", a.grimoires, users.Status)
	r.Require("POST "+canvasBase+"/grimoires", a.saveGrimoire, users.EditCanvas)
	r.Require("POST "+canvasBase+"/grimoires/restore", a.restoreGrimoire, users.EditCanvas)
	r.Require("GET "+canvasBase+"/rituals", a.rituals, users.Status)
	r.Require("PUT "+canvasBase+"/rituals/{id}", a.ritual, users.EditCanvas)
}

// authorize: the topic id is "<workspace>/<table>".
func (a *canvasArea) authorize(u users.User, id string) bool {
	ws, _, ok := cutSlash(id)
	return ok && visible(u, ws)
}

func cutSlash(s string) (before, after string, ok bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			return s[:i], s[i+1:], i > 0 && i+1 < len(s)
		}
	}
	return s, "", false
}

func (a *canvasArea) snapshot(ctx context.Context, u users.User, id string) (any, error) {
	src := a.p.canvasSource()
	if src == nil {
		return struct{}{}, nil
	}
	ws, table, _ := cutSlash(id)
	st, err := src.State(ctx, ws, table)
	if err != nil {
		return nil, err
	}
	st.CanEdit = users.Can(u.Level, users.EditCanvas) && u.Acts(ws)
	return st, nil
}

// source finds the engine and the table of the request, answering the error when
// the user does not see the workspace or the engine is not there.
func (a *canvasArea) source(w http.ResponseWriter, r *http.Request, edit bool) (src CanvasSource, ws, table string, ok bool) {
	src = a.p.canvasSource()
	if src == nil {
		WriteError(w, Err(CodeUnavailable, "o canvas não está disponível agora", nil))
		return nil, "", "", false
	}
	ws, table = r.PathValue("ws"), r.PathValue("table")
	u := UserOf(r)
	if !visible(u, ws) || (edit && !u.Acts(ws)) {
		WriteError(w, Err(CodeNotFound, "mesa não encontrada", nil))
		return nil, "", "", false
	}
	return src, ws, table, true
}

// fail answers the error of the engine: a refusal keeps the reason it gave.
func (a *canvasArea) fail(w http.ResponseWriter, err error) {
	var ref *Refusal
	switch {
	case errors.As(err, &ref):
		params := map[string]any{"reason": ref.Reason}
		for k, v := range ref.Params {
			params[k] = v
		}
		WriteError(w, Err(CodeRefused, ref.Message, params))
	case errors.Is(err, ErrCanvasNotFound):
		WriteError(w, Err(CodeNotFound, "não encontrado", nil))
	case errors.Is(err, ErrCanvasConflict):
		WriteError(w, Err(CodeConflict, "mudou desde que você abriu", nil))
	default:
		a.p.log.Warn("panel: an action of the canvas failed", "err", err)
		WriteError(w, errInternal())
	}
}

func (a *canvasArea) done(r *http.Request, action, ws, table string, detail map[string]any) {
	if detail == nil {
		detail = map[string]any{}
	}
	detail["user_id"], detail["workspace"], detail["table"] = UserOf(r).ID, ws, table
	a.p.audit(r.Context(), "panel.canvas_"+action, detail)
}

func (a *canvasArea) state(w http.ResponseWriter, r *http.Request) {
	src, ws, table, ok := a.source(w, r, false)
	if !ok {
		return
	}
	st, err := src.State(r.Context(), ws, table)
	if err != nil {
		a.fail(w, err)
		return
	}
	u := UserOf(r)
	st.CanEdit = users.Can(u.Level, users.EditCanvas) && u.Acts(ws)
	WriteJSON(w, http.StatusOK, st)
}

// maxLayout bounds one save of the layout.
const maxLayout = 2000

func (a *canvasArea) layout(w http.ResponseWriter, r *http.Request) {
	src, ws, table, ok := a.source(w, r, true)
	if !ok {
		return
	}
	var req LayoutRequest
	if !decode(w, r, &req) {
		return
	}
	if len(req.Items) > maxLayout || len(req.Remove) > maxLayout {
		WriteError(w, errBadRequest())
		return
	}
	for _, it := range req.Items {
		if _, id, found := cutKey(it.Key); !found || id == "" {
			WriteError(w, errBadRequest())
			return
		}
	}
	if err := src.SaveLayout(r.Context(), ws, table, req); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func cutKey(key string) (kind, id string, ok bool) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			return key[:i], key[i+1:], true
		}
	}
	return key, "", false
}

func (a *canvasArea) connect(add bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		src, ws, table, ok := a.source(w, r, true)
		if !ok {
			return
		}
		var req ConnectRequest
		if !decode(w, r, &req) {
			return
		}
		var err error
		action := "connect"
		if add {
			err = src.Connect(r.Context(), UserOf(r), ws, table, req.A, req.B)
		} else {
			action = "disconnect"
			err = src.Disconnect(r.Context(), UserOf(r), ws, table, req.A, req.B)
		}
		if err != nil {
			a.fail(w, err)
			return
		}
		a.done(r, action, ws, table, map[string]any{"a": req.A.Kind + ":" + req.A.ID, "b": req.B.Kind + ":" + req.B.ID})
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *canvasArea) pin(w http.ResponseWriter, r *http.Request) {
	src, ws, table, ok := a.source(w, r, true)
	if !ok {
		return
	}
	var req PinRequest
	if !decode(w, r, &req) {
		return
	}
	if err := src.Pin(r.Context(), UserOf(r), ws, table, r.PathValue("id"), req.Pinned); err != nil {
		a.fail(w, err)
		return
	}
	a.done(r, "pin", ws, table, map[string]any{"character": r.PathValue("id"), "pinned": req.Pinned})
	w.WriteHeader(http.StatusNoContent)
}

func (a *canvasArea) scroll(w http.ResponseWriter, r *http.Request) {
	src, ws, table, ok := a.source(w, r, true)
	if !ok {
		return
	}
	var req ScrollWriteRequest
	if !decode(w, r, &req) {
		return
	}
	sc, err := src.WriteScroll(r.Context(), UserOf(r), ws, table, r.PathValue("id"), req)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.done(r, "scroll_edit", ws, table, map[string]any{"scroll": sc.ID, "revision": sc.Revision})
	WriteJSON(w, http.StatusOK, sc)
}

func (a *canvasArea) quest(edit bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		src, ws, table, ok := a.source(w, r, true)
		if !ok {
			return
		}
		var req QuestRequest
		if !decode(w, r, &req) {
			return
		}
		var (
			q   CanvasQuest
			err error
		)
		if edit {
			q, err = src.EditQuest(r.Context(), UserOf(r), ws, table, r.PathValue("id"), req)
		} else {
			q, err = src.CreateQuest(r.Context(), UserOf(r), ws, table, req)
		}
		if err != nil {
			a.fail(w, err)
			return
		}
		a.done(r, "quest", ws, table, map[string]any{"quest": q.ID, "edit": edit})
		WriteJSON(w, http.StatusOK, q)
	}
}

func (a *canvasArea) grimoires(w http.ResponseWriter, r *http.Request) {
	src := a.p.canvasSource()
	if src == nil {
		WriteError(w, Err(CodeUnavailable, "o canvas não está disponível agora", nil))
		return
	}
	list, err := src.Grimoires(r.Context(), UserOf(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"grimoires": list})
}

func (a *canvasArea) saveGrimoire(w http.ResponseWriter, r *http.Request) {
	src, ws, table, ok := a.source(w, r, true)
	if !ok {
		return
	}
	var req GrimoireSaveRequest
	if !decode(w, r, &req) {
		return
	}
	g, err := src.SaveGrimoire(r.Context(), UserOf(r), ws, table, req)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.done(r, "grimoire_save", ws, table, map[string]any{"grimoire": g.Name})
	WriteJSON(w, http.StatusOK, g)
}

func (a *canvasArea) restoreGrimoire(w http.ResponseWriter, r *http.Request) {
	src, ws, table, ok := a.source(w, r, true)
	if !ok {
		return
	}
	var req GrimoireRestoreRequest
	if !decode(w, r, &req) {
		return
	}
	plan, err := src.RestoreGrimoire(r.Context(), UserOf(r), ws, table, req)
	if err != nil {
		a.fail(w, err)
		return
	}
	if plan.Done {
		a.done(r, "grimoire_restore", ws, table, map[string]any{"grimoire": req.Name})
	}
	WriteJSON(w, http.StatusOK, plan)
}

func (a *canvasArea) rituals(w http.ResponseWriter, r *http.Request) {
	src, ws, table, ok := a.source(w, r, false)
	if !ok {
		return
	}
	list, err := src.Rituals(r.Context(), ws, table)
	if err != nil {
		a.fail(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, map[string]any{"rituals": list})
}

func (a *canvasArea) ritual(w http.ResponseWriter, r *http.Request) {
	src, ws, table, ok := a.source(w, r, true)
	if !ok {
		return
	}
	var req RitualEditRequest
	if !decode(w, r, &req) {
		return
	}
	ri, err := src.EditRitual(r.Context(), UserOf(r), ws, table, r.PathValue("id"), req)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.done(r, "ritual_edit", ws, table, map[string]any{"ritual": ri.ID})
	WriteJSON(w, http.StatusOK, ri)
}

// watchCanvas tells the followers of a canvas that the table changed: each
// commit to the database, once the commits pause for canvasQuiet, is an event
// "changed" on every canvas topic that has a follower. The browser then reads
// the state again, so what an agent does shows without reloading the page.
func (p *Panel) watchCanvas(ctx context.Context, next <-chan struct{}) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-next:
		}
		next = p.db.LogWatch()
		if len(p.hub.Topics("canvas"))+len(p.hub.Topics(workspacesTopic)) == 0 {
			continue
		}
		// wait for the commits to pause
		for settled := false; !settled; {
			t := p.clk.NewTimer(canvasQuiet)
			select {
			case <-ctx.Done():
				t.Stop()
				return
			case <-next:
				next = p.db.LogWatch()
				t.Stop()
			case <-t.C():
				settled = true
			}
		}
		for _, topic := range p.hub.Topics("canvas") {
			_ = p.hub.Publish(topic, EventChanged, struct{}{})
		}
		// the screens of the workspaces follow the same commits
		for _, topic := range p.hub.Topics(workspacesTopic) {
			_ = p.hub.Publish(topic, EventChanged, struct{}{})
		}
	}
}
