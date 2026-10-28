package panel

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/mestre-ia/mestre/internal/users"
)

// The workspaces screens (docs/painel/administracao.md): the panel reads and acts
// through the WorkspaceSource, the engine's own services. The server checks the
// level and the workspace of the user at every request; the buttons of the front
// end are only a convenience.

// ErrWorkspaceNotFound is what a WorkspaceSource says of a slug that does not exist.
var ErrWorkspaceNotFound = errors.New("panel: no such workspace")

// WorkspaceSource is the engine, as the workspaces screens see it.
type WorkspaceSource interface {
	// List is every workspace (the panel keeps the ones the user sees).
	List(ctx context.Context) ([]WorkspaceRow, error)
	// Sheets are the sheets that may be the Grand Master of a new workspace.
	Sheets(ctx context.Context) ([]SheetOption, error)
	// Create makes the workspace (session zero) and records the sheet of its Grand Master.
	Create(ctx context.Context, by users.User, req CreateWorkspaceRequest) (WorkspaceRow, error)
	// Retry runs the current stage of a preparing workspace now.
	Retry(ctx context.Context, slug string) error
	Pause(ctx context.Context, slug string) error
	Resume(ctx context.Context, slug string) error
	// AnswerVersioning records whether .mestre/ goes into the project (local or versioned).
	AnswerVersioning(ctx context.Context, by users.User, slug, answer string) error
	Verification(ctx context.Context, slug string) (VerificationView, error)
	// ConfirmVerification makes doc the confirmed verificacao.yaml and writes it
	// to the main clone; it answers the hash.
	ConfirmVerification(ctx context.Context, by users.User, slug string, doc []byte) (hash string, err error)
}

type workspaceHolder struct{ src WorkspaceSource }

// SetWorkspaces gives the panel the workspaces of the engine; until then the
// screens answer that they are not available.
func (p *Panel) SetWorkspaces(src WorkspaceSource) { p.workspaces.Store(&workspaceHolder{src}) }

func init() { RegisterArea("workspaces", mountWorkspaces) }

type workspaceArea struct {
	p *Panel
}

// workspacesTopic is the topic the screens follow: its events say a workspace changed.
const workspacesTopic = "workspaces"

func mountWorkspaces(r *Router) {
	p := r.Panel()
	a := &workspaceArea{p: p}
	p.hub.Provide(workspacesTopic, Topic{Authorize: func(u users.User, id string) bool { return id == "all" && users.Can(u.Level, users.Status) },
		Snapshot: func(ctx context.Context, u users.User, _ string) (any, error) { return a.list(ctx, u) }})
	r.Require("GET /api/workspaces", a.handleList, users.Status)
	r.Require("POST /api/workspaces", a.handleCreate, users.CreateWorkspace)
	r.Require("POST /api/workspaces/{slug}/retry", a.act("retry", users.ConfigureWorkspace), users.ConfigureWorkspace)
	r.Require("POST /api/workspaces/{slug}/pause", a.act("pause", users.Pause), users.Pause)
	r.Require("POST /api/workspaces/{slug}/resume", a.act("resume", users.Pause), users.Pause)
	r.Require("POST /api/workspaces/{slug}/versioning", a.handleVersioning, users.ConfigureWorkspace)
	r.Require("GET /api/workspaces/{slug}/verification", a.handleVerification, users.Status)
	r.Require("PUT /api/workspaces/{slug}/verification", a.handleConfirm, users.ConfigureWorkspace)
	mountView(r, a)
}

func (p *Panel) workspaceSource() WorkspaceSource {
	if h := p.workspaces.Load(); h != nil {
		return h.src
	}
	return nil
}

// list is the workspaces the user sees, with what the user may do.
func (a *workspaceArea) list(ctx context.Context, u users.User) (WorkspaceList, error) {
	out := WorkspaceList{Workspaces: []WorkspaceRow{}, Sheets: []SheetOption{}, CanCreate: users.Can(u.Level, users.CreateWorkspace)}
	src := a.p.workspaceSource()
	if src == nil {
		return out, nil
	}
	all, err := src.List(ctx)
	if err != nil {
		return out, err
	}
	for _, w := range all {
		if !visible(u, w.Slug) {
			continue
		}
		w.CanAct = u.Acts(w.Slug)
		out.Workspaces = append(out.Workspaces, w)
	}
	if out.CanCreate {
		if out.Sheets, err = src.Sheets(ctx); err != nil {
			return out, err
		}
		out.Sheets = listOf(out.Sheets)
	}
	return out, nil
}

func (a *workspaceArea) source(w http.ResponseWriter) (WorkspaceSource, bool) {
	src := a.p.workspaceSource()
	if src == nil {
		WriteError(w, Err(CodeUnavailable, "api.workspaces_unavailable", nil))
		return nil, false
	}
	return src, true
}

// target finds the workspace of the path for a user who acts in it: one the user
// does not act in is "not found", as the canvas does.
func (a *workspaceArea) target(w http.ResponseWriter, r *http.Request, act bool) (WorkspaceSource, string, bool) {
	src, ok := a.source(w)
	if !ok {
		return nil, "", false
	}
	slug, u := r.PathValue("slug"), UserOf(r)
	if !visible(u, slug) || (act && !u.Acts(slug)) {
		WriteError(w, Err(CodeNotFound, "api.workspace_not_found", nil))
		return nil, "", false
	}
	return src, slug, true
}

func (a *workspaceArea) fail(w http.ResponseWriter, err error) {
	var ref *Refusal
	switch {
	case errors.As(err, &ref):
		WriteError(w, ErrMsg(ref.Msg))
	case errors.Is(err, ErrWorkspaceNotFound), errors.Is(err, ErrCanvasNotFound):
		WriteError(w, Err(CodeNotFound, "api.workspace_not_found", nil))
	default:
		a.p.log.Warn("panel: an action of the workspaces failed", "err", err)
		WriteError(w, errInternal())
	}
}

func (a *workspaceArea) audit(r *http.Request, action string, detail map[string]any) {
	detail["user_id"], detail["ip"] = UserOf(r).ID, clientIP(r)
	a.p.audit(r.Context(), "panel.workspace."+action, detail)
}

func (a *workspaceArea) handleList(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.source(w); !ok {
		return
	}
	out, err := a.list(r.Context(), UserOf(r))
	if err != nil {
		a.fail(w, err)
		return
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *workspaceArea) handleCreate(w http.ResponseWriter, r *http.Request) {
	src, ok := a.source(w)
	if !ok {
		return
	}
	var in CreateWorkspaceRequest
	if !decode(w, r, &in) {
		return
	}
	in.Repo, in.Slug, in.GrandMasterSheet = strings.TrimSpace(in.Repo), strings.TrimSpace(in.Slug), strings.TrimSpace(in.GrandMasterSheet)
	if in.Repo == "" || in.GrandMasterSheet == "" {
		WriteError(w, errBadRequest())
		return
	}
	row, err := src.Create(r.Context(), UserOf(r), in)
	if err != nil {
		a.fail(w, err)
		return
	}
	row.CanAct = UserOf(r).Acts(row.Slug)
	a.audit(r, "created", map[string]any{"workspace": row.Slug, "repo": row.Repo, "grand_master_sheet": in.GrandMasterSheet})
	WriteJSON(w, http.StatusCreated, row)
}

// act is the handler of retry, pause and resume.
func (a *workspaceArea) act(action string, _ users.Capability) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		src, slug, ok := a.target(w, r, true)
		if !ok {
			return
		}
		var err error
		switch action {
		case "retry":
			err = src.Retry(r.Context(), slug)
		case "pause":
			err = src.Pause(r.Context(), slug)
		default:
			err = src.Resume(r.Context(), slug)
		}
		if err != nil {
			a.fail(w, err)
			return
		}
		a.audit(r, action, map[string]any{"workspace": slug})
		w.WriteHeader(http.StatusNoContent)
	}
}

func (a *workspaceArea) handleVersioning(w http.ResponseWriter, r *http.Request) {
	src, slug, ok := a.target(w, r, true)
	if !ok {
		return
	}
	var in VersioningRequest
	if !decode(w, r, &in) {
		return
	}
	if in.Answer != "local" && in.Answer != "versioned" {
		WriteError(w, errBadRequest())
		return
	}
	if err := src.AnswerVersioning(r.Context(), UserOf(r), slug, in.Answer); err != nil {
		a.fail(w, err)
		return
	}
	a.audit(r, "versioning", map[string]any{"workspace": slug, "after": in.Answer})
	w.WriteHeader(http.StatusNoContent)
}

func (a *workspaceArea) handleVerification(w http.ResponseWriter, r *http.Request) {
	src, slug, ok := a.target(w, r, false)
	if !ok {
		return
	}
	v, err := src.Verification(r.Context(), slug)
	if err != nil {
		a.fail(w, err)
		return
	}
	v.Commands = listOf(v.Commands)
	WriteJSON(w, http.StatusOK, v)
}

// maxVerificationDoc bounds the document the panel takes (the engine bounds it
// again: 20 commands of 1000 bytes).
const maxVerificationDoc = 32 << 10

func (a *workspaceArea) handleConfirm(w http.ResponseWriter, r *http.Request) {
	src, slug, ok := a.target(w, r, true)
	if !ok {
		return
	}
	var in ConfirmVerificationRequest
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Doc) == "" || len(in.Doc) > maxVerificationDoc {
		WriteError(w, errBadRequest())
		return
	}
	before, err := src.Verification(r.Context(), slug)
	if err != nil {
		a.fail(w, err)
		return
	}
	hash, err := src.ConfirmVerification(r.Context(), UserOf(r), slug, []byte(in.Doc))
	if err != nil {
		a.fail(w, err)
		return
	}
	detail := map[string]any{"workspace": slug, "after": map[string]any{"hash": hash, "doc": a.p.mask(in.Doc)}}
	if before.Confirmed != nil {
		detail["before"] = map[string]any{"hash": before.Confirmed.Hash, "doc": a.p.mask(before.Confirmed.Doc)}
	}
	a.audit(r, "verification_confirmed", detail)
	v, err := src.Verification(r.Context(), slug)
	if err != nil {
		a.fail(w, err)
		return
	}
	v.Commands = listOf(v.Commands)
	WriteJSON(w, http.StatusOK, v)
}
