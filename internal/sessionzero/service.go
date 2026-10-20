package sessionzero

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// AppsPoll is how often the stage apps looks again while it waits for the
// Apps to be installed.
const AppsPoll = 30 * time.Second

// Options builds a Service.
type Options struct {
	// Workspaces runs the stages; New registers the ones of the session zero.
	Workspaces *workspaces.Service
	DB         *store.DB
	Clock      clock.Clock

	Installations Installations
	Users         Users
	Approvals     Approvals
	Checklist     Checklist
	GrandMaster   GrandMaster
	Analyst       Analyst
	PullRequests  PullRequests
	AppReview     AppReview
	// Verification receives the commands of verification the Grand Master proposes in
	// the stage analysis (nil: none is kept); the confirmed ones are its own.
	Verification VerificationProposer

	// Worktree makes the files of the main clone.
	Worktree Worktree
	// Masker is the detector of secrets that looks at the .mestre/ files
	// before they go into the pull request; required.
	Masker *logging.Masker
	// Log may be nil.
	Log *slog.Logger
}

// Service is the session zero of the workspaces of the Citadel.
type Service struct {
	o     Options
	store Store
	log   *slog.Logger

	prMu sync.Mutex // one pull request of the session zero is built at a time
}

// New builds the Service and registers its stages (apps, files, analysis and
// approval_test) on the workspaces. Nothing runs until the workspaces do.
func New(o Options) (*Service, error) {
	switch {
	case o.Workspaces == nil, o.DB == nil, o.Clock == nil, o.Installations == nil, o.Users == nil, o.Approvals == nil,
		o.Checklist == nil, o.GrandMaster == nil, o.Analyst == nil, o.PullRequests == nil, o.AppReview == nil, o.Worktree.Providers == nil, o.Masker == nil:
		return nil, errors.New("sessionzero: Workspaces, DB, Clock, Installations, Users, Approvals, Checklist, GrandMaster, Analyst, PullRequests, AppReview, Masker and Worktree.Providers are required")
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Service{o: o, store: Store{DB: o.DB}, log: log}
	for stage, fn := range map[workspaces.Stage]workspaces.StageFunc{
		workspaces.StageApps:         s.runApps,
		workspaces.StageFiles:        s.runFiles,
		workspaces.StageAnalysis:     s.runAnalysis,
		workspaces.StageApprovalTest: func(ctx context.Context, w workspaces.Workspace) error { return s.runApprovalTest(ctx, w, false) },
	} {
		if err := o.Workspaces.Register(stage, fn); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// State is what the session zero keeps of the workspace: the answers it
// waits for and the result of the approval test (the panel and
// `mestre gh settings show` read it).
func (s *Service) State(ctx context.Context, slug string) (State, error) {
	return s.store.Get(ctx, slug)
}

// coded is an error with the code the panel shows for the stage.
type coded struct {
	code string
	err  error
}

func (e *coded) Error() string { return e.err.Error() }
func (e *coded) Unwrap() error { return e.err }
func (e *coded) Code() string  { return e.code }

func code(c string, err error) error { return &coded{code: c, err: err} }

// runApps is the stage apps: both fixed Apps installed in the repository.
// What is missing the engine adds through the API with the personal
// credential of the owner, or else the owner gets the link, once; meanwhile
// the stage waits and nothing is cloned, with no credential at all
// (docs/sessao-zero/workspace.md#início).
func (s *Service) runApps(ctx context.Context, w workspaces.Workspace) error {
	missing, err := s.o.Installations.Missing(ctx, w.Repo)
	if err != nil {
		return code("apps_check_failed", err)
	}
	if len(missing) == 0 {
		return nil
	}
	addErr := s.o.Installations.AddRepository(ctx, w.Repo, missing)
	if addErr == nil {
		if missing, err = s.o.Installations.Missing(ctx, w.Repo); err != nil {
			return code("apps_check_failed", err)
		}
		if len(missing) == 0 {
			return nil
		}
	}
	st, err := s.store.Get(ctx, w.Slug)
	if err != nil {
		return err
	}
	if st.AppsNotifiedAt.IsZero() {
		links := make([]InstallLink, 0, len(missing))
		for _, app := range missing {
			url, err := s.o.Installations.InstallLink(ctx, w.Repo, app)
			if err != nil {
				return code("apps_link_failed", err)
			}
			links = append(links, InstallLink{App: app, URL: url})
		}
		if err := s.o.Users.AskOwnerToInstall(ctx, w.Slug, links); err != nil {
			return code("apps_notify_failed", err)
		}
		if err := s.store.AppsNotified(ctx, w.Slug, s.o.Clock.Now()); err != nil {
			return err
		}
	}
	reason := "Apps ainda não instalados em " + w.Repo.String() + ": " + strings.Join(missing, ", ")
	if addErr != nil && !errors.Is(addErr, ErrNoPersonalCredential) {
		s.log.Warn("session zero: adding the repository to the installation", "workspace", w.Slug, "err", addErr)
	}
	return &workspaces.WaitError{Reason: reason, Poll: AppsPoll}
}

// runFiles is the stage files: .mestre/, the files of the providers and the
// exclusions in the main clone; then the Grand Master starts and the users
// are asked about versioning .mestre/ (the question does not hold the
// workspace).
func (s *Service) runFiles(ctx context.Context, w workspaces.Workspace) error {
	dir := s.o.Workspaces.Layout().Principal(w.Slug)
	repo, err := s.o.Workspaces.OpenPrincipal(ctx, w.Slug)
	if err != nil {
		return code("worktree_failed", err)
	}
	st, err := s.store.Get(ctx, w.Slug)
	if err != nil {
		return err
	}
	structure := EnsureStructure(dir)
	files, err := s.o.Worktree.Ensure(ctx, WorktreeRequest{Dir: dir, Repo: repo, ExcludeMestre: st.Versioning != VersioningVersioned})
	if err != nil {
		return code("files_failed", err)
	}
	if len(structure.Invalid) > 0 {
		if err := s.o.Users.Notify(ctx, w.Slug, Notice{Kind: "mestre_dir", Text: problemsText("O .mestre/ do repositório tem itens que o Mestre não tocou", structure.Invalid)}); err != nil {
			return code("notify_failed", err)
		}
	}
	if len(files.Problems) > 0 {
		if err := s.o.Users.Notify(ctx, w.Slug, Notice{Kind: "provider_files", Text: problemsText("Arquivos dos provedores que o Mestre não gerou (os personagens desses provedores não iniciam no clone principal até resolver)", files.Problems)}); err != nil {
			return code("notify_failed", err)
		}
	}
	if err := s.o.GrandMaster.Start(ctx, w.Slug); err != nil {
		return code("grand_master_failed", err)
	}
	if st.VersioningAskedAt.IsZero() {
		if err := s.o.Users.AskVersioning(ctx, w.Slug); err != nil {
			return code("notify_failed", err)
		}
		if err := s.store.VersioningAsked(ctx, w.Slug, s.o.Clock.Now()); err != nil {
			return err
		}
	}
	return nil
}

func problemsText(head string, ps []Problem) string {
	var b strings.Builder
	b.WriteString(head + ":")
	for _, p := range ps {
		b.WriteString("\n- " + p.String())
	}
	return b.String()
}

// AnswerVersioning records the answer of the users to whether .mestre/ is
// versioned and acts on it: the exclusions change at once, and choosing to
// version opens the pull request of the session zero with the files. Going
// back to "local" deletes nothing in the repository; the engine only stops
// proposing changes (docs/sessao-zero/workspace.md#versionar-ou-não-mestre).
func (s *Service) AnswerVersioning(ctx context.Context, slug string, v Versioning) error {
	if v != VersioningLocal && v != VersioningVersioned {
		return fmt.Errorf("sessionzero: %q is not an answer (local or versioned)", v)
	}
	w, err := s.o.Workspaces.Get(ctx, slug)
	if err != nil {
		return err
	}
	if !w.MirrorReady() {
		return fmt.Errorf("sessionzero: workspace %s has no main clone yet (stage %s)", slug, w.Stage)
	}
	repo, err := s.o.Workspaces.OpenPrincipal(ctx, slug)
	if err != nil {
		return err
	}
	if err := s.o.Worktree.SetExcludeMestre(ctx, repo, v == VersioningLocal); err != nil {
		return err
	}
	if err := s.store.VersioningAnswered(ctx, slug, s.o.Clock.Now(), v); err != nil {
		return err
	}
	if v == VersioningVersioned {
		return s.openPR(ctx, w)
	}
	return nil
}

// AgentsMDDecided records the decision of the users on the AGENTS.md
// proposed (the approval of type proposta_agents_md). Approved, the file goes
// into the pull request of the session zero; the AGENTS.md never exists in
// the project before that.
func (s *Service) AgentsMDDecided(ctx context.Context, slug string, approved bool) error {
	w, err := s.o.Workspaces.Get(ctx, slug)
	if err != nil {
		return err
	}
	st, err := s.store.Get(ctx, slug)
	if err != nil {
		return err
	}
	if st.AgentsMD != AgentsMDProposed {
		return fmt.Errorf("sessionzero: workspace %s has no AGENTS.md proposal waiting for an answer", slug)
	}
	if !approved {
		return s.store.AgentsMDDecided(ctx, slug, s.o.Clock.Now(), AgentsMDRejected)
	}
	if err := s.store.AgentsMDDecided(ctx, slug, s.o.Clock.Now(), AgentsMDApproved); err != nil {
		return err
	}
	return s.openPR(ctx, w)
}
