package panel

import (
	"context"
	"math"
	"net/http"
	"strings"
	"unicode"

	"github.com/mestre-ia/mestre/internal/users"
)

// The workspace view (docs/painel/visao-do-workspace.md): the panel reads the
// blocks of the tables through the ViewSource and pauses a table through it. The
// server checks the level and the workspace at every request; the button of the
// front end is only a convenience.

// ViewSource is the engine, as the workspace view sees it.
type ViewSource interface {
	// View reads the Grand Master and the tables of an active workspace, with the
	// place of each element. The clock of the engine decides the quiet of a table.
	View(ctx context.Context, workspace string) (ViewData, error)
	// SaveLayout keeps the place of the elements that moved.
	SaveLayout(ctx context.Context, workspace string, items []CanvasItem) error
	// PauseTable is the button **Pausar mesa**: the engine checks the level of by
	// again. reason is the sentence the user wrote (already trimmed, never empty). It
	// answers false when the table was paused already.
	PauseTable(ctx context.Context, by users.User, workspace, table, reason string) (bool, error)
}

type viewHolder struct{ src ViewSource }

// SetWorkspaceView gives the panel the engine of the workspace view; until then
// the view shows the workspace with no tables.
func (p *Panel) SetWorkspaceView(src ViewSource) { p.view.Store(&viewHolder{src}) }

func (p *Panel) viewSource() ViewSource {
	if h := p.view.Load(); h != nil {
		return h.src
	}
	return nil
}

// maxViewItems bounds the elements of a layout the panel takes: the Grand Master
// and the tables of a workspace.
const maxViewItems = 500

func mountView(r *Router, a *workspaceArea) {
	r.Require("GET /api/workspaces/{slug}/view", a.handleView, users.Status)
	r.Require("PUT /api/workspaces/{slug}/view/layout", a.handleViewLayout, users.Status)
	r.Require("POST /api/workspaces/{slug}/tables/{table}/pause", a.handlePauseTable, users.PauseButtons)
}

func (a *workspaceArea) handleView(w http.ResponseWriter, r *http.Request) {
	src, slug, ok := a.target(w, r, false)
	if !ok {
		return
	}
	all, err := src.List(r.Context())
	if err != nil {
		a.fail(w, err)
		return
	}
	u := UserOf(r)
	out := WorkspaceView{Tables: []TableBlock{}, Layout: []CanvasItem{}}
	found := false
	for _, row := range all {
		if row.Slug == slug {
			row.CanAct = u.Acts(slug)
			out.Workspace, found = row, true
		}
	}
	if !found {
		WriteError(w, Err(CodeNotFound, "api.workspace_not_found", nil))
		return
	}
	if out.Workspace.State != "preparing" {
		if vs := a.p.viewSource(); vs != nil {
			data, err := vs.View(r.Context(), slug)
			if err != nil {
				a.fail(w, err)
				return
			}
			out.GrandMaster, out.Tables, out.Layout = data.GrandMaster, listOf(data.Tables), listOf(data.Layout)
			for i := range out.Tables {
				out.Tables[i].Party, out.Tables[i].Waits = listOf(out.Tables[i].Party), listOf(out.Tables[i].Waits)
			}
		}
	}
	out.Workspace.Stages = listOf(out.Workspace.Stages)
	out.CanPause = users.Can(u.Level, users.PauseButtons) && u.Acts(slug)
	out.CanType = users.Can(u.Level, users.TypeInTerminal) && u.Acts(slug)
	WriteJSON(w, http.StatusOK, out)
}

func (a *workspaceArea) handleViewLayout(w http.ResponseWriter, r *http.Request) {
	_, slug, ok := a.target(w, r, false)
	if !ok {
		return
	}
	vs := a.p.viewSource()
	if vs == nil {
		WriteError(w, Err(CodeUnavailable, "api.view_unavailable", nil))
		return
	}
	var in ViewLayoutRequest
	if !decode(w, r, &in) {
		return
	}
	if len(in.Items) > maxViewItems {
		WriteError(w, errBadRequest())
		return
	}
	for _, it := range in.Items {
		if !viewKey(it.Key) || math.IsNaN(it.X) || math.IsInf(it.X, 0) || math.IsNaN(it.Y) || math.IsInf(it.Y, 0) {
			WriteError(w, errBadRequest())
			return
		}
	}
	if err := vs.SaveLayout(r.Context(), slug, in.Items); err != nil {
		a.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *workspaceArea) handlePauseTable(w http.ResponseWriter, r *http.Request) {
	_, slug, ok := a.target(w, r, true)
	if !ok {
		return
	}
	vs := a.p.viewSource()
	if vs == nil {
		WriteError(w, Err(CodeUnavailable, "api.view_unavailable", nil))
		return
	}
	var in PauseTableRequest
	if !decode(w, r, &in) {
		return
	}
	reason := cleanReason(in.Reason)
	if reason == "" {
		WriteError(w, Err(CodeBadRequest, "api.pause_reason", nil))
		return
	}
	table := r.PathValue("table")
	changed, err := vs.PauseTable(r.Context(), UserOf(r), slug, table, reason)
	if err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r, "table_paused", map[string]any{"workspace": slug, "table": table, "changed": changed, "reason": reason})
	WriteJSON(w, http.StatusOK, PauseTableResponse{Changed: changed})
}

// cleanReason is the reason of a pause as it is kept: one line, no control characters,
// at most MaxPauseReason characters; "" when nothing is left.
func cleanReason(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Cf, r):
			// invisible format characters (bidi marks, zero width): not kept
		case unicode.IsControl(r) || unicode.IsSpace(r):
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	out := strings.Join(strings.Fields(b.String()), " ")
	if rs := []rune(out); len(rs) > MaxPauseReason {
		out = string(rs[:MaxPauseReason])
	}
	return out
}

// viewKey says the key names the Grand Master or a table: the only elements of the view.
func viewKey(k string) bool {
	return k == "gm" || (strings.HasPrefix(k, "table:") && len(k) > len("table:"))
}
