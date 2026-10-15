package workspaces_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

func TestSlugify(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		"Meu_App":                 "meu-app",
		"meu-app":                 "meu-app",
		"  .Dotfiles.  ":          "dotfiles",
		"a__b..c":                 "a-b-c",
		"API":                     "api",
		"x":                       "", // below the 2 characters of the format
		"___":                     "",
		"ação":                    "a-o", // only [a-z0-9] survive
		strings.Repeat("a", 40):   strings.Repeat("a", 32),
		strings.Repeat("ab-", 20): "ab-ab-ab-ab-ab-ab-ab-ab-ab-ab-ab",
	} {
		got := workspaces.Slugify(in)
		if got != want {
			t.Errorf("Slugify(%q) = %q; want %q", in, got, want)
		}
		if got != "" && !workspaces.ValidSlug(got) {
			t.Errorf("Slugify(%q) = %q, which is not a valid slug", in, got)
		}
	}
}

func TestValidSlug(t *testing.T) {
	t.Parallel()
	for _, ok := range []string{"ab", "meu-app", "a1", "a-b-c", strings.Repeat("a", 32)} {
		if !workspaces.ValidSlug(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "a", "-ab", "ab-", "A-b", "a_b", "a b", "a/b", "..", "--help", strings.Repeat("a", 33)} {
		if workspaces.ValidSlug(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestParseRepo(t *testing.T) {
	t.Parallel()
	r, err := workspaces.ParseRepo("lucas/Meu_App")
	if err != nil || r.Owner != "lucas" || r.Name != "Meu_App" || r.Key() != "lucas/meu_app" {
		t.Fatalf("ParseRepo = %+v, %v", r, err)
	}
	for _, bad := range []string{"", "lucas", "lucas/", "/app", "a/b/c", "lu cas/app", "lucas/..", "-x/app", "lucas/a b", "https://github.com/lucas/app"} {
		if _, err := workspaces.ParseRepo(bad); !errors.Is(err, workspaces.ErrInvalidRepo) {
			t.Errorf("ParseRepo(%q) = %v; want ErrInvalidRepo", bad, err)
		}
	}
}

// CA-WS-01
func TestSlugComesFromTheRepositoryAndTheSameRepositoryIsRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	if w.Slug != "meu-app" {
		t.Fatalf("slug = %q; want meu-app", w.Slug)
	}
	// The same repository, with another slug and spelled in another case, is
	// the same repository: refused, naming the workspace that has it.
	for _, req := range []workspaces.CreateRequest{
		{Repo: "lucas/Meu_App", Slug: "outro"},
		{Repo: "Lucas/meu_app"},
	} {
		_, err := r.svc.Create(r.ctx, req)
		if !errors.Is(err, workspaces.ErrRepoTaken) || err.Error() != "repositório já é o workspace meu-app" {
			t.Fatalf("Create(%+v) = %v; want the repository already is the workspace meu-app", req, err)
		}
	}
	if list, err := r.svc.List(r.ctx); err != nil || len(list) != 1 {
		t.Fatalf("workspaces = %v, %v; want only meu-app", list, err)
	}
}

func TestSlugErrorsSuggestAFreeSlug(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if _, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"}); err != nil {
		t.Fatal(err)
	}
	var se *workspaces.SlugError
	_, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/other", Slug: "meu-app"})
	if !errors.Is(err, workspaces.ErrSlugTaken) || !errors.As(err, &se) || se.Suggestion != "meu-app-2" {
		t.Fatalf("taken slug: err = %v; want ErrSlugTaken suggesting meu-app-2", err)
	}
	_, err = r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/other", Slug: "Outro App"})
	if !errors.Is(err, workspaces.ErrInvalidSlug) || !errors.As(err, &se) || se.Suggestion != "outro-app" {
		t.Fatalf("malformed slug: err = %v; want ErrInvalidSlug suggesting outro-app", err)
	}
	_, err = r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/x"})
	if !errors.Is(err, workspaces.ErrInvalidSlug) {
		t.Fatalf("a repository name with no slug in it: err = %v; want ErrInvalidSlug", err)
	}
	if _, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "not a repo"}); !errors.Is(err, workspaces.ErrInvalidRepo) {
		t.Fatalf("err = %v; want ErrInvalidRepo", err)
	}
}

func TestSuggestSlugFitsTheLimit(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("a", 32)
	got := workspaces.SuggestSlug(long, func(s string) bool { return s == long })
	if got != strings.Repeat("a", 30)+"-2" || !workspaces.ValidSlug(got) {
		t.Fatalf("SuggestSlug = %q", got)
	}
}

// Part of CA-WS-02: the order of the stages and the single way to active. The
// mirror and the main clone are the next test and the full suite; the stages
// other than the mirror belong to the session zero, which closes the
// criterion with its own runners, and the `table open` that refuses is the
// CheckOpenTable the tables ask (the tables are milestone 4, later stages).
func TestStagesRunInOrderAndOnlyThenTheWorkspaceIsActive(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	log := &stageLog{}
	r.registerStages(r.svc, log, nil)
	var seenAtStage []string
	if err := r.svc.Register(workspaces.StageFiles, func(ctx context.Context, w workspaces.Workspace) error {
		log.mu.Lock()
		log.stage = append(log.stage, w.Stage)
		log.mu.Unlock()
		// While a stage runs the workspace is preparing, at that stage, and
		// no table opens.
		err := r.svc.CheckOpenTable(ctx, w.Slug)
		seenAtStage = append(seenAtStage, string(w.State), string(w.Stage), errString(err))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	if w.State != workspaces.StatePreparing || w.Stage != workspaces.StageApps {
		t.Fatalf("created = %s/%s; want preparing/apps", w.State, w.Stage)
	}
	err = r.svc.CheckOpenTable(r.ctx, w.Slug)
	var na *workspaces.NotActiveError
	if !errors.Is(err, workspaces.ErrNotActive) || !errors.As(err, &na) || !strings.Contains(err.Error(), "preparação, etapa apps") {
		t.Fatalf("table open before the stages = %v; want it refused naming the stage apps", err)
	}
	if err := r.svc.Prepare(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	// The mirror stage ran between apps and files.
	if got := log.list(); !slices.Equal(got, []workspaces.Stage{workspaces.StageApps, workspaces.StageFiles, workspaces.StageAnalysis, workspaces.StageApprovalTest}) {
		t.Fatalf("stages other than the mirror ran as %v", got)
	}
	if want := []string{"preparing", "files", "workspace meu-app não está ativo: está em preparação, etapa files"}; !slices.Equal(seenAtStage, want) {
		t.Fatalf("at the files stage: %q; want %q", seenAtStage, want)
	}
	w = r.get(w.Slug)
	if w.State != workspaces.StateActive || w.Stage != workspaces.StageNone || w.StageFailure != nil {
		t.Fatalf("after the last stage = %s/%q; want active", w.State, w.Stage)
	}
	if err := r.svc.CheckOpenTable(r.ctx, w.Slug); err != nil {
		t.Fatalf("table open in the active workspace = %v", err)
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// Part of CA-WS-02: the mirror (bare, with the refspec) and the main clone on
// the main branch, as the fake git shows them; the full suite checks the
// same with the real git.
func TestMirrorStageCreatesTheMirrorAndTheMainClone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	layout := r.svc.Layout()
	mirror, err := r.git.Open(r.ctx, layout.Mirror(w.Slug))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := mirror.GetConfig(r.ctx, "remote.origin.fetch"); err != nil || got != "+refs/heads/*:refs/remotes/origin/*" {
		t.Fatalf("remote.origin.fetch = %q, %v", got, err)
	}
	list, err := mirror.ListWorktrees(r.ctx)
	if err != nil || len(list) != 2 || !list[0].Bare || list[1].Path != layout.Principal(w.Slug) || list[1].Branch != "main" {
		t.Fatalf("worktrees = %+v, %v; want the bare mirror and the main clone on main", list, err)
	}
	if got, err := mirror.ResolveRef(r.ctx, "refs/remotes/origin/main"); err != nil || got != r.head(w.Slug) {
		t.Fatalf("origin/main = %s, %v; want the head of the main clone %s", got, err, r.head(w.Slug))
	}
	if body, err := r.principal(w.Slug).ReadFile(r.ctx, "HEAD", "README.md"); err != nil || string(body) != "# Meu App\n" {
		t.Fatalf("README in the main clone = %q, %v", body, err)
	}
	if w.Fetch.LastAt != t0 {
		t.Fatalf("last fetch = %v; the mirror stage fetched at %v", w.Fetch.LastAt, t0)
	}
}

// CA-WS-07
func TestFailingMirrorStageShowsTheStageTheErrorAndTheReason(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.registerStages(r.svc, &stageLog{}, nil)
	r.mu.Lock()
	r.cloneErr = errors.New("fatal: unable to access 'https://github.com/lucas/Meu_App.git/': Could not resolve host: github.com")
	r.mu.Unlock()
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Prepare(r.ctx, w.Slug); err == nil {
		t.Fatal("Prepare = nil; the mirror stage should fail")
	}
	// What the panel reads.
	got, err := r.svc.Store().Get(r.ctx, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	f := got.StageFailure
	if got.State != workspaces.StatePreparing || got.Stage != workspaces.StageMirror || f == nil ||
		f.Code != "clone_failed" || !strings.Contains(f.Reason, "Could not resolve host") {
		t.Fatalf("workspace = %s/%s failure %+v; want preparing/mirror with clone_failed and the reason", got.State, got.Stage, f)
	}
}

func TestFailedStageIsTriedAgainWithBackoffAndTheWorkspaceGoesOn(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	log := &stageLog{}
	r.registerStages(r.svc, log, nil)
	r.mu.Lock()
	r.cloneErr = errors.New("connection reset")
	r.mu.Unlock()
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	next, ok, err := r.svc.Tick(r.ctx) // apps ran, the mirror failed
	if err != nil || !ok || next != t0.Add(30*time.Second) {
		t.Fatalf("Tick = %v, %v, %v; want the retry at +30s", next, ok, err)
	}
	if f := r.get(w.Slug).StageFailure; f == nil || f.Failures != 1 || f.RetryAt != t0.Add(30*time.Second) {
		t.Fatalf("failure = %+v", f)
	}
	r.runUntil(r.svc, t0.Add(29*time.Second))
	if f := r.get(w.Slug).StageFailure; f == nil || f.Failures != 1 {
		t.Fatalf("the stage was tried before its time: %+v", f)
	}
	r.runUntil(r.svc, t0.Add(30*time.Second)) // second failure: wait 1 min
	if f := r.get(w.Slug).StageFailure; f == nil || f.Failures != 2 || f.RetryAt != t0.Add(90*time.Second) {
		t.Fatalf("second failure = %+v; want the retry at +1m30", f)
	}
	r.mu.Lock()
	r.cloneErr = nil
	r.mu.Unlock()
	r.runUntil(r.svc, t0.Add(90*time.Second))
	if w = r.get(w.Slug); w.State != workspaces.StateActive || w.StageFailure != nil {
		t.Fatalf("after the retry: %s/%s %+v; want active", w.State, w.Stage, w.StageFailure)
	}
	// The "try again" button runs the stage at once, whatever the backoff.
	w2, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/other"})
	if err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	r.cloneErr = errors.New("again")
	r.mu.Unlock()
	if err := r.svc.Prepare(r.ctx, w2.Slug); err == nil {
		t.Fatal("Prepare = nil")
	}
	r.mu.Lock()
	r.cloneErr = nil
	r.mu.Unlock()
	if err := r.svc.Prepare(r.ctx, w2.Slug); err != nil || r.get(w2.Slug).State != workspaces.StateActive {
		t.Fatalf("Prepare after the failure = %v, state %s; want active", err, r.get(w2.Slug).State)
	}
}

func TestStageWithoutRunnerWaitsWithoutFailing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Prepare(r.ctx, w.Slug); !errors.Is(err, workspaces.ErrNoRunner) {
		t.Fatalf("Prepare = %v; want ErrNoRunner", err)
	}
	if next, ok, err := r.svc.Tick(r.ctx); err != nil || ok {
		t.Fatalf("Tick = %v, %v, %v; a workspace waiting for a runner schedules nothing", next, ok, err)
	}
	if got := r.get(w.Slug); got.Stage != workspaces.StageApps || got.StageFailure != nil {
		t.Fatalf("workspace = %+v; want it waiting at apps, with no failure", got)
	}
	if err := r.svc.Register(workspaces.StageMirror, nil); err == nil {
		t.Fatal("the mirror stage belongs to the service")
	}
}

func TestPauseResumeAndTheStateGate(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.registerStages(r.svc, &stageLog{}, nil)
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Pause(r.ctx, w.Slug); !errors.Is(err, workspaces.ErrPausePreparing) {
		t.Fatalf("Pause of a preparing workspace = %v; want ErrPausePreparing", err)
	}
	if err := r.svc.Prepare(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Resume(r.ctx, w.Slug); !errors.Is(err, workspaces.ErrBadTransition) {
		t.Fatalf("Resume of an active workspace = %v; want ErrBadTransition", err)
	}
	if err := r.svc.Pause(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	err = r.svc.CheckOpenTable(r.ctx, w.Slug)
	if !errors.Is(err, workspaces.ErrNotActive) || !strings.Contains(err.Error(), "estado paused") {
		t.Fatalf("table open in a paused workspace = %v; want it refused naming the state", err)
	}
	if err := r.svc.Pause(r.ctx, w.Slug); !errors.Is(err, workspaces.ErrBadTransition) {
		t.Fatalf("Pause of a paused workspace = %v; want ErrBadTransition", err)
	}
	if err := r.svc.Resume(r.ctx, w.Slug); err != nil || r.svc.CheckOpenTable(r.ctx, w.Slug) != nil {
		t.Fatalf("Resume = %v; the workspace should be active", err)
	}
	if err := r.svc.Pause(r.ctx, "nope"); !errors.Is(err, workspaces.ErrNotFound) {
		t.Fatalf("Pause of a missing workspace = %v; want ErrNotFound", err)
	}
}

// The workspaces are rows of the Guild database: opened again, they are what
// they were (the heavy criterion CA-WS-05 closes at the end of the project,
// with the whole Citadel restarted).
func TestWorkspacesSurviveReopeningTheDatabase(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	w := r.active()
	if err := r.svc.Pause(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	want := r.get(w.Slug)
	if err := r.db.Close(); err != nil {
		t.Fatal(err)
	}
	svc := r.newService(r.openDB())
	got, err := svc.Get(r.ctx, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != workspaces.StatePaused || got.Repo != want.Repo || got.DefaultBranch != "main" || got.CloneURL != want.CloneURL ||
		got.Fetch.LastAt != want.Fetch.LastAt || !got.CreatedAt.Equal(want.CreatedAt) {
		t.Fatalf("reopened = %+v; want %+v", got, want)
	}
}

func TestMirrorsListTheMirrorsThatExistAndOpenThem(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.registerStages(r.svc, &stageLog{}, nil)
	w, err := r.svc.Create(r.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	m := r.svc.Mirrors()
	if ids, err := m.IDs(r.ctx); err != nil || len(ids) != 0 {
		t.Fatalf("IDs before the mirror stage = %v, %v; want none", ids, err)
	}
	if _, err := m.Open(r.ctx, w.Slug); err == nil {
		t.Fatal("Open of a mirror that does not exist")
	}
	if err := r.svc.Prepare(r.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	ids, err := m.IDs(r.ctx)
	if err != nil || !slices.Equal(ids, []string{w.Slug}) {
		t.Fatalf("IDs = %v, %v", ids, err)
	}
	repo, err := m.Open(r.ctx, w.Slug)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := repo.ListRefs(r.ctx, "refs/heads/")
	if err != nil || len(refs) != 1 || refs[0].Name != "refs/heads/main" {
		t.Fatalf("refs of the mirror = %v, %v", refs, err)
	}
}
