package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/orchestration/verification"
	"github.com/mestre-ia/mestre/internal/orchestration/workspaces"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/safefile"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/users"
)

// WorkspacePanelSource is what an IntegrationRun may also be: the workspaces as
// the screens of the panel see them (workspacehost.go).
type WorkspacePanelSource interface {
	PanelWorkspaces() panel.WorkspaceSource
}

// PanelWorkspaces implements WorkspacePanelSource.
func (r *stdRun) PanelWorkspaces() panel.WorkspaceSource { return workspaceHost{r} }

// outsideEditEvery is how often the file of the verification commands is compared
// with the confirmed version.
const outsideEditEvery = time.Minute

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// maxVerificationFile is the most the engine reads of verificacao.yaml.
const maxVerificationFile = 64 << 10

// workspaceHost is the panel.WorkspaceSource over the orchestration and the session
// zero of the Citadel that acts: every rule is the one of the services, which the
// CLI and the bot use too (docs/painel/administracao.md).
type workspaceHost struct{ r *stdRun }

var _ panel.WorkspaceSource = workspaceHost{}

func (h workspaceHost) parts() (*Orchestration, *sessionzero.Service, error) {
	orc, zero := h.r.orc.Load(), h.r.zero.Load()
	if orc == nil || orc.Workspaces == nil || zero == nil {
		return nil, nil, errNoOrchestration
	}
	return orc, zero, nil
}

func (h workspaceHost) verifications() verification.Store {
	return verification.Store{DB: h.r.env.DB, Clock: h.r.env.Clock}
}

func (h workspaceHost) List(ctx context.Context) ([]panel.WorkspaceRow, error) {
	orc, zero, err := h.parts()
	if err != nil {
		return nil, err
	}
	all, err := orc.Workspaces.List(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]panel.WorkspaceRow, 0, len(all))
	for _, w := range all {
		st, err := zero.State(ctx, w.Slug)
		if err != nil {
			return nil, err
		}
		row := panel.WorkspaceRow{
			Slug: w.Slug, Repo: w.Repo.String(), DefaultBranch: w.DefaultBranch, State: string(w.State), Stages: h.stages(orc, w),
			Versioning: string(st.Versioning), VersioningAsked: !st.VersioningAskedAt.IsZero(), SessionZeroPR: st.PRURL,
			GrandMasterSheet: st.GrandMasterSheet, GrandMaster: "none", FetchError: w.Fetch.Error, CreatedAt: w.CreatedAt,
		}
		if gm, ok, err := orc.Registry.GrandMaster(ctx, w.Slug); err != nil {
			return nil, err
		} else if ok {
			row.GrandMaster = string(gm.State)
		}
		out = append(out, row)
	}
	return out, nil
}

// stages is the session zero of the workspace as the screen draws it.
func (h workspaceHost) stages(orc *Orchestration, w workspaces.Workspace) []panel.WorkspaceStage {
	out := make([]panel.WorkspaceStage, 0, len(workspaces.Stages))
	reached := false
	for _, s := range workspaces.Stages {
		st := panel.WorkspaceStage{Key: string(s), State: panel.StagePending}
		switch {
		case w.State != workspaces.StatePreparing:
			st.State = panel.StageDone
		case s == w.Stage:
			reached = true
			st.State = panel.StageCurrent
			if f := w.StageFailure; f != nil {
				st.State = panel.StageFailed
				st.Failure = &panel.StageFailureInfo{Code: f.Code, Reason: f.Reason, Failures: f.Failures, At: f.At, RetryAt: f.RetryAt}
			} else if why, ok := orc.Workspaces.Waiting(w.Slug); ok {
				st.State, st.Waiting = panel.StageWaiting, why
			}
		case !reached:
			st.State = panel.StageDone
		}
		out = append(out, st)
	}
	return out
}

// Sheets are the sheets that run an agent: the Grand Master is one, so a plain shell is not.
func (h workspaceHost) Sheets(ctx context.Context) ([]panel.SheetOption, error) {
	orc, _, err := h.parts()
	if err != nil {
		return nil, err
	}
	all, err := orc.Presets.Sheets(ctx)
	if err != nil {
		return nil, err
	}
	out := []panel.SheetOption{}
	for _, s := range all {
		if s.Agent != presets.AgentShell {
			out = append(out, panel.SheetOption{Name: s.Name, Agent: s.Agent})
		}
	}
	return out, nil
}

func (h workspaceHost) Create(ctx context.Context, by users.User, req panel.CreateWorkspaceRequest) (panel.WorkspaceRow, error) {
	orc, zero, err := h.parts()
	if err != nil {
		return panel.WorkspaceRow{}, err
	}
	sheets, err := h.Sheets(ctx)
	if err != nil {
		return panel.WorkspaceRow{}, err
	}
	known := false
	for _, s := range sheets {
		known = known || s.Name == req.GrandMasterSheet
	}
	if !known {
		return panel.WorkspaceRow{}, &panel.Refusal{Reason: "unknown_sheet", Message: "a ficha " + req.GrandMasterSheet + " não existe ou não roda um agente"}
	}
	w, err := orc.Workspaces.Create(ctx, workspaces.CreateRequest{Repo: req.Repo, Slug: req.Slug})
	if err != nil {
		return panel.WorkspaceRow{}, createRefusal(err)
	}
	if err := zero.ChooseGrandMasterSheet(ctx, w.Slug, req.GrandMasterSheet); err != nil {
		return panel.WorkspaceRow{}, err
	}
	rows, err := h.List(ctx)
	if err != nil {
		return panel.WorkspaceRow{}, err
	}
	for _, row := range rows {
		if row.Slug == w.Slug {
			return row, nil
		}
	}
	return panel.WorkspaceRow{}, panel.ErrWorkspaceNotFound
}

// createRefusal turns what the creation refuses into the refusal the screen shows.
func createRefusal(err error) error {
	var taken *workspaces.RepoTakenError
	var slug *workspaces.SlugError
	switch {
	case errors.As(err, &taken):
		return &panel.Refusal{Reason: "repo_taken", Message: err.Error(), Params: map[string]any{"slug": taken.Slug}}
	case errors.As(err, &slug):
		return &panel.Refusal{Reason: "invalid_slug", Message: err.Error(), Params: map[string]any{"suggestion": slug.Suggestion}}
	case errors.Is(err, workspaces.ErrInvalidRepo):
		return &panel.Refusal{Reason: "invalid_repo", Message: err.Error()}
	case errors.Is(err, workspaces.ErrInvalidSlug), errors.Is(err, workspaces.ErrSlugTaken):
		return &panel.Refusal{Reason: "invalid_slug", Message: err.Error()}
	}
	return &panel.Refusal{Reason: "creation_failed", Message: err.Error()}
}

func (h workspaceHost) Retry(ctx context.Context, slug string) error {
	orc, _, err := h.parts()
	if err != nil {
		return err
	}
	w, err := orc.Workspaces.Get(ctx, slug)
	if err != nil {
		return notFound(err)
	}
	if w.State != workspaces.StatePreparing {
		return &panel.Refusal{Reason: "not_preparing", Message: "o workspace já passou da sessão zero"}
	}
	// the stage can take minutes (a clone): it runs on, and the screen follows the
	// workspace as it changes; a failure is recorded in the stage
	go func() { _ = orc.Workspaces.Prepare(context.WithoutCancel(ctx), slug) }()
	return nil
}

func notFound(err error) error {
	if errors.Is(err, workspaces.ErrNotFound) {
		return panel.ErrWorkspaceNotFound
	}
	return err
}

func (h workspaceHost) Pause(ctx context.Context, slug string) error {
	orc, _, err := h.parts()
	if err != nil {
		return err
	}
	return transitionRefusal(orc.Tables.PauseWorkspace(ctx, slug))
}

func (h workspaceHost) Resume(ctx context.Context, slug string) error {
	orc, _, err := h.parts()
	if err != nil {
		return err
	}
	return transitionRefusal(orc.Tables.ResumeWorkspace(ctx, slug))
}

func transitionRefusal(err error) error {
	switch {
	case errors.Is(err, workspaces.ErrNotFound):
		return panel.ErrWorkspaceNotFound
	case errors.Is(err, workspaces.ErrPausePreparing):
		return &panel.Refusal{Reason: "preparing", Message: err.Error()}
	case errors.Is(err, workspaces.ErrBadTransition):
		return &panel.Refusal{Reason: "bad_state", Message: err.Error()}
	}
	return err
}

func (h workspaceHost) AnswerVersioning(ctx context.Context, _ users.User, slug, answer string) error {
	orc, zero, err := h.parts()
	if err != nil {
		return err
	}
	if _, err := orc.Workspaces.Get(ctx, slug); err != nil {
		return notFound(err)
	}
	if err := zero.AnswerVersioning(ctx, slug, sessionzero.Versioning(answer)); err != nil {
		if w, gerr := orc.Workspaces.Get(ctx, slug); gerr == nil && !w.MirrorReady() {
			return &panel.Refusal{Reason: "not_ready", Message: err.Error()}
		}
		return err
	}
	return nil
}

func (h workspaceHost) file(orc *Orchestration, slug string) string {
	return filepath.Join(orc.Workspaces.Layout().Principal(slug), filepath.FromSlash(verification.File))
}

func (h workspaceHost) Verification(ctx context.Context, slug string) (panel.VerificationView, error) {
	orc, _, err := h.parts()
	if err != nil {
		return panel.VerificationView{}, err
	}
	if _, err := orc.Workspaces.Get(ctx, slug); err != nil {
		return panel.VerificationView{}, notFound(err)
	}
	var v panel.VerificationView
	data, _, err := safefile.Read(h.file(orc, slug), maxVerificationFile)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return v, fmt.Errorf("read %s: %w", verification.File, err)
	default:
		v.File = string(data)
		if doc, perr := verification.Parse(data); perr != nil {
			v.FileError = perr.Error()
		} else {
			v.Commands = doc.Commands
		}
	}
	c, ok, err := h.verifications().Confirmation(ctx, slug)
	if err != nil {
		return v, err
	}
	if ok {
		v.Confirmed = &panel.VerificationConfirmed{Doc: c.Doc, Hash: c.Hash, By: h.userName(ctx, c.By), At: c.At}
		v.Outside = v.File != "" && hashOf([]byte(v.File)) != c.Hash
	}
	return v, nil
}

// userName is the name of a user for the screen (the id when the user is gone).
func (h workspaceHost) userName(ctx context.Context, id string) string {
	if u, err := h.r.users.Get(ctx, id); err == nil {
		return u.Name
	}
	return id
}

func (h workspaceHost) ConfirmVerification(ctx context.Context, by users.User, slug string, doc []byte) (string, error) {
	orc, _, err := h.parts()
	if err != nil {
		return "", err
	}
	w, err := orc.Workspaces.Get(ctx, slug)
	if err != nil {
		return "", notFound(err)
	}
	if !w.MirrorReady() {
		return "", &panel.Refusal{Reason: "not_ready", Message: "o clone principal do workspace ainda não existe"}
	}
	hash, err := h.verifications().Confirm(ctx, slug, by.ID, doc)
	if errors.Is(err, verification.ErrInvalid) {
		return "", &panel.Refusal{Reason: "invalid_document", Message: err.Error()}
	}
	if err != nil {
		return "", err
	}
	// the file of the main clone follows what was confirmed, so that a later
	// difference is an edit made outside the panel
	if err := safefile.Write(h.file(orc, slug), doc, safefile.Options{}); err != nil {
		return "", fmt.Errorf("write %s: %w", verification.File, err)
	}
	return hash, nil
}

// WorkspaceLoop tells the Administrators when verificacao.yaml is edited outside
// the panel: the confirmed version stays in force and each different content is
// told once (CA-SZ-WS-08, docs/sessao-zero/workspace.md#comandos-de-verificação).
func (r *stdRun) WorkspaceLoop(ctx context.Context) error {
	t := r.env.Clock.NewTicker(outsideEditEvery)
	defer t.Stop()
	for {
		r.checkOutsideEdits(ctx)
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
		}
	}
}

func (r *stdRun) checkOutsideEdits(ctx context.Context) {
	h := workspaceHost{r}
	orc, _, err := h.parts()
	if err != nil {
		return
	}
	all, err := orc.Workspaces.List(ctx)
	if err != nil {
		return
	}
	for _, w := range all {
		if !w.MirrorReady() {
			continue
		}
		data, _, err := safefile.Read(h.file(orc, w.Slug), maxVerificationFile)
		if err != nil {
			continue // no file is no edit: the confirmed version holds
		}
		changed, err := h.verifications().OutsideEdit(ctx, w.Slug, data)
		if err != nil {
			r.env.Log.Warn("app: reading the confirmed verification commands", "workspace", w.Slug, "err", err)
			continue
		}
		if changed {
			text := "O arquivo " + verification.File + " do workspace " + w.Slug + " foi mudado fora do painel. Vale a última versão confirmada; para aplicar a mudança, confirme-a na tela de workspaces."
			if _, err := r.alert.AdminAlert(ctx, w.Slug, text); err != nil && !errors.Is(err, telegram.ErrQueued) {
				r.env.Log.Warn("app: the Administrators were not told of an edit of the verification commands", "workspace", w.Slug, "err", err)
			}
		}
	}
}
