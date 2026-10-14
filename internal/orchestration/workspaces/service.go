package workspaces

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Defaults (docs/orquestracao/workspaces.md).
const (
	// DefaultFetchInterval is how often the mirror is fetched.
	DefaultFetchInterval = 15 * time.Minute
	// DefaultAlertAfter is how long a fetch or an advance may fail before the
	// users, or the Grand Master, hear of it.
	DefaultAlertAfter = time.Hour
	// DefaultFetchTimeout bounds one fetch.
	DefaultFetchTimeout = 5 * time.Minute
	// BackoffFirst and BackoffMax bound the wait after a failure: 30 s, 1
	// min, 2 min… up to 15 min.
	BackoffFirst = 30 * time.Second
	BackoffMax   = 15 * time.Minute
)

// FetchRefspec is the refspec the mirror gets after the bare clone, which
// writes none (docs/orquestracao/workspaces.md#espelho-e-clone-principal-na-cidadela).
const FetchRefspec = "+refs/heads/*:refs/remotes/origin/*"

// Options builds a Service.
type Options struct {
	DB     *store.DB
	Git    Git
	Layout Layout
	Clock  clock.Clock
	GitHub GitHub
	// Credentials may be nil: the fetches carry no credentials.
	Credentials Credentials
	Alerts      Alerts
	GrandMaster GrandMaster
	// Log may be nil.
	Log *slog.Logger
	// Gate, when set, is asked before the Service touches the outside (every
	// fetch): the fence of the Citadel (guild.Fence.Allow). An error holds
	// the work back; it is asked again later.
	Gate func() error

	// FetchInterval, AlertAfter (the fetch outage) and AdvanceAlertAfter
	// (the main clone held back) and FetchTimeout take the defaults when zero.
	FetchInterval     time.Duration
	AlertAfter        time.Duration
	AdvanceAlertAfter time.Duration
	FetchTimeout      time.Duration
}

// StageFunc runs one stage of the session zero for a workspace. It returns
// when the stage is done or has failed: a stage that waits for a person
// leaves the question pending and returns nil (docs/orquestracao/workspaces.md#estados).
// An error that carries Code() string gives the code the panel shows.
type StageFunc func(ctx context.Context, w Workspace) error

// Service is the workspaces of the Citadel.
type Service struct {
	o      Options
	store  Store
	layout Layout
	log    *slog.Logger

	mu     sync.Mutex
	stages map[Stage]StageFunc
	sched  map[string]*entry
	locks  map[string]*sync.Mutex // "stage/<slug>" for the stages, "fetch/<slug>" for fetch and advance
	wake   chan struct{}
}

// New builds the Service. The mirror stage is the Service's own; the others
// are registered with Register.
func New(o Options) (*Service, error) {
	switch {
	case o.DB == nil, o.Git == nil, o.Clock == nil, o.GitHub == nil, o.Alerts == nil, o.GrandMaster == nil, o.Layout.Root == "":
		return nil, errors.New("workspaces: DB, Git, Layout, Clock, GitHub, Alerts and GrandMaster are required")
	}
	if err := os.MkdirAll(o.Layout.Root, 0o755); err != nil {
		return nil, fmt.Errorf("workspaces: %w", err)
	}
	root, err := filepath.EvalSymlinks(o.Layout.Root)
	if err != nil {
		return nil, fmt.Errorf("workspaces: %w", err)
	}
	if root, err = filepath.Abs(root); err != nil {
		return nil, fmt.Errorf("workspaces: %w", err)
	}
	o.Layout.Root = root
	if o.FetchInterval <= 0 {
		o.FetchInterval = DefaultFetchInterval
	}
	if o.AlertAfter <= 0 {
		o.AlertAfter = DefaultAlertAfter
	}
	if o.AdvanceAlertAfter <= 0 {
		o.AdvanceAlertAfter = DefaultAlertAfter
	}
	if o.FetchTimeout <= 0 {
		o.FetchTimeout = DefaultFetchTimeout
	}
	log := o.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	s := &Service{o: o, store: Store{DB: o.DB}, layout: o.Layout, log: log,
		stages: map[Stage]StageFunc{}, sched: map[string]*entry{}, locks: map[string]*sync.Mutex{}, wake: make(chan struct{}, 1)}
	s.stages[StageMirror] = s.CreateMirror
	return s, nil
}

// Store is the store of the Service, for the panel to read.
func (s *Service) Store() Store { return s.store }

// Layout is where the workspaces live (Root resolved).
func (s *Service) Layout() Layout { return s.layout }

// Register sets who runs a stage of the session zero. The mirror stage
// belongs to the Service and cannot be replaced.
func (s *Service) Register(stage Stage, fn StageFunc) error {
	if stage.index() < 0 || stage == StageMirror || fn == nil {
		return fmt.Errorf("workspaces: cannot register a runner for stage %q", stage)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stages[stage] = fn
	return nil
}

// lock takes the lock of a workspace.
func (s *Service) lock(slug string) func() {
	s.mu.Lock()
	l, ok := s.locks[slug]
	if !ok {
		l = &sync.Mutex{}
		s.locks[slug] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// Get returns a workspace.
func (s *Service) Get(ctx context.Context, slug string) (Workspace, error) {
	return s.store.Get(ctx, slug)
}

// List returns every workspace.
func (s *Service) List(ctx context.Context) ([]Workspace, error) { return s.store.List(ctx) }

// CreateRequest is the creation of a workspace.
type CreateRequest struct {
	// Repo is "owner/name".
	Repo string
	// Slug is optional: the user may choose it at creation; it is
	// immutable afterwards. Empty takes it from the name of the repository.
	Slug string
}

// Create registers a workspace for a repository: preparing, at the first
// stage. The reasons of a refusal are in the error (RepoTakenError,
// SlugError). The engine then walks the stages (Tick, or Prepare).
func (s *Service) Create(ctx context.Context, req CreateRequest) (Workspace, error) {
	repo, err := ParseRepo(req.Repo)
	if err != nil {
		return Workspace{}, err
	}
	if slug, taken, err := s.store.RepoSlug(ctx, repo); err != nil {
		return Workspace{}, err
	} else if taken {
		return Workspace{}, &RepoTakenError{Repo: repo.String(), Slug: slug}
	}
	slug, err := s.chooseSlug(ctx, repo, req.Slug)
	if err != nil {
		return Workspace{}, err
	}
	if s.o.Gate != nil {
		if err := s.o.Gate(); err != nil {
			return Workspace{}, fmt.Errorf("create workspace %s: %w", repo, err)
		}
	}
	info, err := s.o.GitHub.Repository(ctx, repo)
	if err != nil {
		return Workspace{}, fmt.Errorf("create workspace %s: %w", repo, err)
	}
	if err := checkInfo(info); err != nil {
		return Workspace{}, fmt.Errorf("create workspace %s: %w", repo, err)
	}
	canonical, err := ParseRepo(info.FullName)
	if err != nil || canonical.Key() != repo.Key() {
		return Workspace{}, fmt.Errorf("create workspace %s: GitHub answered for %q", repo, info.FullName)
	}
	now := s.o.Clock.Now()
	w := Workspace{Slug: slug, Repo: canonical, CloneURL: info.CloneURL, DefaultBranch: info.DefaultBranch,
		State: StatePreparing, Stage: Stages[0], CreatedAt: now, UpdatedAt: now}
	if err := s.store.Insert(ctx, w); err != nil {
		return Workspace{}, err
	}
	s.signal()
	return s.store.Get(ctx, slug)
}

// chooseSlug validates the slug the user gave, or makes one from the name of
// the repository; a slug that cannot be used is refused with a free one
// suggested.
func (s *Service) chooseSlug(ctx context.Context, repo Repo, given string) (string, error) {
	taken := func(slug string) bool {
		t, err := s.store.SlugTaken(ctx, slug)
		return err != nil || t
	}
	slug := given
	if slug == "" {
		slug = Slugify(repo.Name)
		if slug == "" {
			return "", &SlugError{Slug: repo.Name, Reason: "não dá um identificador válido (2 a 32 caracteres de a-z, 0-9 e -)", Suggestion: SuggestSlug("", taken), kind: ErrInvalidSlug}
		}
	}
	if !ValidSlug(slug) {
		return "", &SlugError{Slug: slug, Reason: "fora do formato (2 a 32 caracteres de a-z, 0-9 e -, sem - no começo nem no fim)", Suggestion: SuggestSlug(Slugify(slug), taken), kind: ErrInvalidSlug}
	}
	if taken(slug) {
		return "", &SlugError{Slug: slug, Reason: "já é de outro workspace", Suggestion: SuggestSlug(slug, taken), kind: ErrSlugTaken}
	}
	return slug, nil
}

// checkInfo refuses what GitHub answered that the engine would hand to git
// unchecked: a URL with credentials, a branch that could read as an option.
func checkInfo(i RepoInfo) error {
	if err := gitx.CheckURL(i.CloneURL); err != nil || i.CloneURL == "" {
		return fmt.Errorf("invalid clone URL %q", gitx.RedactURL(i.CloneURL))
	}
	if !validBranch(i.DefaultBranch) {
		return fmt.Errorf("invalid default branch %q", i.DefaultBranch)
	}
	return nil
}

// validBranch is a conservative branch name: no option, no revision syntax.
func validBranch(b string) bool {
	if b == "" || b[0] == '-' || b[0] == '/' || b[len(b)-1] == '/' || b[len(b)-1] == '.' {
		return false
	}
	for i := range len(b) {
		c := b[i]
		ok := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '/'
		if !ok || (i > 0 && c == '.' && b[i-1] == '.') || (i > 0 && c == '/' && b[i-1] == '/') {
			return false
		}
	}
	return true
}

// CheckOpenTable says whether a table may open in the workspace: only in an
// active one. Otherwise the error says the state and, while preparing, the
// stage (NotActiveError).
func (s *Service) CheckOpenTable(ctx context.Context, slug string) error {
	w, err := s.store.Get(ctx, slug)
	if err != nil {
		return err
	}
	if w.State != StateActive {
		return &NotActiveError{Slug: slug, State: w.State, Stage: w.Stage}
	}
	return nil
}

// Pause pauses an active workspace; one still preparing refuses (there are
// no tables to pause). Pausing the tables themselves is the job of the
// tables.
func (s *Service) Pause(ctx context.Context, slug string) error {
	w, err := s.store.Get(ctx, slug)
	if err != nil {
		return err
	}
	if w.State == StatePreparing {
		return fmt.Errorf("%w: %s is at stage %s", ErrPausePreparing, slug, w.Stage)
	}
	return s.store.SetState(ctx, slug, s.o.Clock.Now(), StatePaused, StateActive)
}

// Resume reactivates a paused workspace.
func (s *Service) Resume(ctx context.Context, slug string) error {
	return s.store.SetState(ctx, slug, s.o.Clock.Now(), StateActive, StatePaused)
}

// signal wakes Run.
func (s *Service) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// backoff is the wait after the n-th failure in a row (n from 1): 30 s,
// 1 min, 2 min… up to 15 min.
func backoff(n int) time.Duration {
	d := BackoffFirst
	for i := 1; i < n && d < BackoffMax; i++ {
		d *= 2
	}
	return min(d, BackoffMax)
}

// maxReason bounds the reason a failure keeps.
const maxReason = 1000

func reasonOf(err error) string {
	r := err.Error()
	if len(r) > maxReason {
		r = r[:maxReason] + "…"
	}
	return r
}

// coded is an error with the code a stage failure shows.
type coded struct {
	code string
	err  error
}

func (e *coded) Error() string { return e.err.Error() }
func (e *coded) Unwrap() error { return e.err }
func (e *coded) Code() string  { return e.code }

func code(c string, err error) error { return &coded{code: c, err: err} }

// Prepare runs the stages of a preparing workspace, from the current one,
// until one fails (the failure is recorded with its code and reason, and the
// engine tries again with backoff), one has nobody to run it (ErrNoRunner:
// the workspace waits there) or the workspace is active. It runs the current
// stage now, whatever the backoff: it is also the "try again" of the panel.
func (s *Service) Prepare(ctx context.Context, slug string) error {
	defer s.lock("stage/" + slug)()
	for {
		w, err := s.store.Get(ctx, slug)
		if err != nil {
			return err
		}
		if w.State != StatePreparing {
			return nil
		}
		s.mu.Lock()
		fn := s.stages[w.Stage]
		s.mu.Unlock()
		if fn == nil {
			return fmt.Errorf("%w: %s at %s", ErrNoRunner, slug, w.Stage)
		}
		if err := fn(ctx, w); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return s.stageFailed(ctx, w, err)
		}
		if err := s.store.StageDone(ctx, slug, s.o.Clock.Now(), w.Stage); err != nil {
			return err
		}
		s.log.Info("workspace stage done", "workspace", slug, "stage", w.Stage)
	}
}

// stageFailed records the failure of the current stage and returns the error.
func (s *Service) stageFailed(ctx context.Context, w Workspace, err error) error {
	now := s.o.Clock.Now()
	n := 1
	if w.StageFailure != nil {
		n = w.StageFailure.Failures + 1
	}
	c := "failed"
	var ce interface{ Code() string }
	if errors.As(err, &ce) {
		c = ce.Code()
	}
	f := StageFailure{Code: c, Reason: reasonOf(err), Failures: n, At: now, RetryAt: now.Add(backoff(n))}
	if werr := s.store.StageFailed(ctx, w.Slug, now, f); werr != nil {
		return errors.Join(err, werr)
	}
	s.log.Warn("workspace stage failed", "workspace", w.Slug, "stage", w.Stage, "code", c, "failures", n, "err", err)
	return fmt.Errorf("workspace %s: stage %s: %w", w.Slug, w.Stage, err)
}

// CreateMirror is the mirror stage: the bare clone of the repository, the
// refspec the bare clone does not write, a first fetch (so origin/<branch>
// exists) and the main clone, a worktree of the mirror on the main branch.
// It can be run again after a failure: what already exists is reused.
func (s *Service) CreateMirror(ctx context.Context, w Workspace) error {
	mirrorDir, principalDir := s.layout.Mirror(w.Slug), s.layout.Principal(w.Slug)
	if err := os.MkdirAll(s.layout.Dir(w.Slug), 0o755); err != nil {
		return code("mkdir_failed", err)
	}
	repo, err := s.o.Git.Open(ctx, mirrorDir)
	if errors.Is(err, gitx.ErrNotARepo) {
		auth, aerr := s.auth(ctx, w)
		if aerr != nil {
			return code("credentials_failed", aerr)
		}
		repo, err = s.o.Git.Clone(ctx, w.CloneURL, mirrorDir, gitx.CloneOptions{Bare: true, Auth: auth})
		if err != nil {
			return code("clone_failed", err)
		}
	} else if err != nil {
		return code("open_failed", err)
	}
	if err := repo.SetConfig(ctx, "remote.origin.fetch", FetchRefspec); err != nil {
		return code("config_failed", err)
	}
	if err := s.fetch(ctx, repo, w); err != nil {
		return code("fetch_failed", err)
	}
	if err := s.store.FetchOK(ctx, w.Slug, s.o.Clock.Now()); err != nil {
		return err
	}
	if err := s.addPrincipal(ctx, repo, w, principalDir); err != nil {
		return err
	}
	return nil
}

// addPrincipal creates the main clone, or checks the one that exists, and
// checks the record of the layout against what git says.
func (s *Service) addPrincipal(ctx context.Context, repo gitx.Repo, w Workspace, dir string) error {
	list, err := repo.ListWorktrees(ctx)
	if err != nil {
		return code("worktree_failed", err)
	}
	exists := false
	for _, wt := range list {
		exists = exists || sameFile(wt.Path, dir)
	}
	if !exists {
		if _, err := repo.AddWorktree(ctx, dir, gitx.WorktreeOptions{Branch: w.DefaultBranch}); err != nil {
			if errors.Is(err, gitx.ErrRefNotFound) {
				err = fmt.Errorf("a branch principal %q não existe no repositório: %w", w.DefaultBranch, err)
			}
			return code("worktree_failed", err)
		}
	}
	rec, err := s.o.Git.Record(ctx, dir)
	if err != nil {
		return code("worktree_failed", err)
	}
	if want := s.layout.record(w.Slug); !sameFile(rec.Dir, want.Dir) || !sameFile(rec.GitDir, want.GitDir) || !sameFile(rec.Common, want.Common) {
		return code("layout_mismatch", fmt.Errorf("git registrou o clone principal em %+v; o esperado era %+v", rec, want))
	}
	return nil
}

// sameFile compares two paths, as paths: git prints them absolute and
// resolved, the layout has been resolved at New.
func sameFile(a, b string) bool {
	a, b = filepath.Clean(filepath.FromSlash(a)), filepath.Clean(filepath.FromSlash(b))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func (s *Service) auth(ctx context.Context, w Workspace) (*gitx.Auth, error) {
	if s.o.Credentials == nil {
		return nil, nil
	}
	return s.o.Credentials.Auth(ctx, w.Repo)
}

// fetch fetches origin into the mirror, with the credentials of the App.
func (s *Service) fetch(ctx context.Context, repo gitx.Repo, w Workspace) error {
	auth, err := s.auth(ctx, w)
	if err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	ctx, cancel := clock.WithTimeout(ctx, s.o.Clock, s.o.FetchTimeout)
	defer cancel()
	if auth != nil {
		return repo.FetchWithAuth(ctx, *auth, "origin")
	}
	return repo.Fetch(ctx, "origin")
}
