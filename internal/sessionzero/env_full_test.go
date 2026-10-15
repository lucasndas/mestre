//go:build full

package sessionzero_test

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/pkg/clock"
)

var t0 = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

// env is the Citadel of the session zero over the real git: a local
// repository stands for the one on GitHub, and every port that reaches
// GitHub, the users or the Grand Master is a fake that records what it was
// asked, with the stage the workspace was at.
type env struct {
	t      *testing.T
	ctx    context.Context
	clk    *clock.Fake
	git    *gitx.Real
	remote gitx.Repo
	root   string
	db     *store.DB
	ws     *workspaces.Service
	sz     *sessionzero.Service
	f      *fakes
}

// newEnv makes the repository lucas/Meu_App with the files and the Citadel.
func newEnv(t *testing.T, files map[string]string) *env {
	t.Helper()
	g, err := gitx.New(gitx.Options{Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, ctx: t.Context(), clk: clock.NewFake(t0), git: g, root: t.TempDir()}
	e.f = &fakes{e: e, missing: nil, rules: sessionzero.Rules{Summary: "sem regras"}, decision: "APPROVED",
		analysis: sessionzero.Analysis{Summary: "Go", ProposedAgentsMD: "# Meu App\n\n## Comandos\n\n- go test ./...\n"}}
	remote, err := g.Init(e.ctx, filepath.Join(e.root, "remote", "meu-app"), gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	e.remote = remote
	e.commit("chore: initial commit", mergeFiles(map[string]string{"README.md": "# Meu App\n"}, files))
	e.db, err = guildtpl.Open(e.ctx, filepath.Join(e.root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: e.clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.db.Close() })
	e.ws, err = workspaces.New(workspaces.Options{
		DB: e.db, Git: g, Layout: workspaces.Layout{Root: filepath.Join(e.root, "workspaces")}, Clock: e.clk,
		GitHub: (*ghFake)(e.f), Credentials: (*credFake)(e.f), Alerts: (*alertFake)(e.f), GrandMaster: e.f,
	})
	if err != nil {
		t.Fatal(err)
	}
	e.sz, err = sessionzero.New(sessionzero.Options{
		Workspaces: e.ws, DB: e.db, Clock: e.clk,
		Installations: e.f, Users: e.f, Approvals: e.f, Checklist: e.f, GrandMaster: e.f, Analyst: e.f, PullRequests: e.f, AppReview: e.f,
		Worktree: sessionzero.Worktree{Providers: providers.NewRegistry(providers.Options{}), Binary: filepath.Join(e.root, "bin", "mestre")},
		Masker:   logging.NewMasker(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// commit makes a commit in the repository that stands for GitHub.
func (e *env) commit(msg string, files map[string]string) {
	e.t.Helper()
	for name, body := range files {
		p := filepath.Join(e.remote.Dir(), filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			e.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	if err := e.remote.Stage(e.ctx); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.remote.Commit(e.ctx, gitx.CommitOptions{Message: msg, Author: gitx.Signature{Name: "Teste", Email: "teste@example.invalid", When: t0}}); err != nil {
		e.t.Fatal(err)
	}
}

// create makes the workspace.
func (e *env) create() workspaces.Workspace {
	e.t.Helper()
	w, err := e.ws.Create(e.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		e.t.Fatal(err)
	}
	return w
}

// active creates the workspace and walks it to active.
func (e *env) active() workspaces.Workspace {
	e.t.Helper()
	w := e.create()
	if err := e.ws.Prepare(e.ctx, w.Slug); err != nil {
		e.t.Fatal(err)
	}
	return e.get(w.Slug)
}

func (e *env) get(slug string) workspaces.Workspace {
	e.t.Helper()
	w, err := e.ws.Get(e.ctx, slug)
	if err != nil {
		e.t.Fatal(err)
	}
	return w
}

func (e *env) state(slug string) sessionzero.State {
	e.t.Helper()
	st, err := e.sz.State(e.ctx, slug)
	if err != nil {
		e.t.Fatal(err)
	}
	return st
}

func (e *env) principal(slug string) string { return e.ws.Layout().Principal(slug) }

func (e *env) gitOut(dir string, args ...string) string {
	e.t.Helper()
	return testkit.ProjectGit(e.t, dir, args...)
}

// readPrincipal reads a file of the main clone.
func (e *env) readPrincipal(slug, rel string) []byte {
	e.t.Helper()
	b, err := os.ReadFile(filepath.Join(e.principal(slug), filepath.FromSlash(rel)))
	if err != nil {
		e.t.Fatal(err)
	}
	return b
}

func (e *env) exists(slug, rel string) bool {
	_, err := os.Lstat(filepath.Join(e.principal(slug), filepath.FromSlash(rel)))
	return err == nil
}

// excludeLines are the lines of the file info/exclude the main clone uses.
func (e *env) excludeLines(slug string) []string {
	e.t.Helper()
	p := strings.TrimSpace(e.gitOut(e.principal(slug), "rev-parse", "--path-format=absolute", "--git-path", "info/exclude"))
	b, err := os.ReadFile(p)
	if err != nil {
		e.t.Fatal(err)
	}
	return strings.Split(strings.ReplaceAll(string(b), "\r\n", "\n"), "\n")
}

// fakes are the ports to GitHub, the users, the approvals, the checklist and
// the Grand Master.
type fakes struct {
	e  *env
	mu sync.Mutex

	// events are "<stage>:<what>" in the order they happened.
	events []string

	// apps
	missing        []string
	addErr         error
	addCalls       int
	ownerLinks     [][]sessionzero.InstallLink
	credentialUses int

	// users
	notices    []sessionzero.Notice
	versioning int
	checklist  []sessionzero.ChecklistItem

	// grand master
	started int
	gmState workspaces.GrandMasterState

	// analysis and approvals
	analysis   sessionzero.Analysis
	analyzeErr error
	requests   []string // texts of the AGENTS.md proposals
	requestIDs int

	// pull requests
	published []sessionzero.PullRequest
	// branchFiles are the files of the branch of each pull request, read
	// from the mirror when it was published.
	branchFiles []map[string]string
	branchLog   []gitx.CommitInfo

	// approval test
	rules     sessionzero.Rules
	decision  string
	failAt    string
	opened    int
	closed    int
	approvals int
}

func (f *fakes) record(what string) {
	stage := ""
	if ws, err := f.e.ws.List(f.e.ctx); err == nil && len(ws) == 1 {
		stage = string(ws[0].Stage)
		if ws[0].State == workspaces.StateActive {
			stage = "active"
		}
	}
	f.events = append(f.events, stage+":"+what)
}

// stagesSeen are the stages the events happened in, in order, without
// repeats.
func (f *fakes) stagesSeen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, ev := range f.events {
		stage, _, _ := strings.Cut(ev, ":")
		if len(out) == 0 || out[len(out)-1] != stage {
			out = append(out, stage)
		}
	}
	return out
}

func (f *fakes) noticeKinds() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, n := range f.notices {
		out = append(out, n.Kind)
	}
	return out
}

// install makes the Apps installed.
func (f *fakes) install() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.missing = nil
}

// workspaces ports

type ghFake fakes

func (g *ghFake) Repository(_ context.Context, repo workspaces.Repo) (workspaces.RepoInfo, error) {
	return workspaces.RepoInfo{FullName: repo.String(), CloneURL: g.e.remote.Dir(), DefaultBranch: "main"}, nil
}

type credFake fakes

func (c *credFake) Auth(context.Context, workspaces.Repo) (*gitx.Auth, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.credentialUses++
	(*fakes)(c).record("mirror")
	return nil, nil
}

type alertFake fakes

func (*alertFake) Operational(context.Context, workspaces.Alert) error { return nil }

func (f *fakes) State(context.Context, string) (workspaces.GrandMasterState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.gmState, nil
}

func (f *fakes) NotifySystem(context.Context, string, string) error { return nil }

// sessionzero ports

func (f *fakes) Missing(context.Context, workspaces.Repo) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("apps")
	return slices.Clone(f.missing), nil
}

func (f *fakes) AddRepository(context.Context, workspaces.Repo, []string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.addCalls++
	if f.addErr != nil {
		return f.addErr
	}
	f.missing = nil
	return nil
}

func (f *fakes) InstallLink(_ context.Context, repo workspaces.Repo, app string) (string, error) {
	return fmt.Sprintf("https://github.example/apps/%s/installations/new?repo=%s", app, repo), nil
}

func (f *fakes) AskOwnerToInstall(_ context.Context, _ string, links []sessionzero.InstallLink) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ownerLinks = append(f.ownerLinks, links)
	return nil
}

func (f *fakes) Notify(_ context.Context, _ string, n sessionzero.Notice) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.notices = append(f.notices, n)
	return nil
}

func (f *fakes) AskVersioning(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.versioning++
	return nil
}

func (f *fakes) RequestAgentsMD(_ context.Context, _, text string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, text)
	f.requestIDs++
	return fmt.Sprintf("req-%d", f.requestIDs), nil
}

func (f *fakes) Raise(_ context.Context, item sessionzero.ChecklistItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checklist = append(f.checklist, item)
	return nil
}

func (f *fakes) Start(context.Context, string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started++
	f.record("files")
	return nil
}

func (f *fakes) Analyze(context.Context, sessionzero.AnalysisInput) (sessionzero.Analysis, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("analysis")
	return f.analysis, f.analyzeErr
}

func (f *fakes) Author(context.Context) (gitx.Signature, error) {
	return gitx.Signature{Name: "mestre-grao-mestre[bot]", Email: "1+mestre-grao-mestre[bot]@users.noreply.github.example", When: t0}, nil
}

// Publish reads what the branch of the pull request holds in the mirror, as
// the push would send it.
func (f *fakes) Publish(ctx context.Context, pr sessionzero.PullRequest) (string, error) {
	mirror, err := f.e.ws.OpenMirror(ctx, "meu-app")
	if err != nil {
		return "", err
	}
	files := map[string]string{}
	for _, p := range strings.Fields(f.e.gitOut(mirror.Dir(), "ls-tree", "-r", "--name-only", "refs/heads/"+pr.Branch)) {
		body, err := mirror.ReadFile(ctx, "refs/heads/"+pr.Branch, p)
		if err != nil {
			return "", err
		}
		files[p] = string(body)
	}
	log, err := mirror.Log(ctx, "refs/remotes/origin/"+pr.Base+"..refs/heads/"+pr.Branch)
	if err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.published = append(f.published, pr)
	f.branchFiles = append(f.branchFiles, files)
	f.branchLog = log
	return "https://github.example/lucas/Meu_App/pull/1", nil
}

func (f *fakes) ReadRules(context.Context, workspaces.Repo, string) (sessionzero.Rules, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.record("approval_test")
	return f.rules, nil
}

var errFake = errors.New("fake failure")

func (f *fakes) OpenTestPR(context.Context, workspaces.Repo, string) (sessionzero.TestPR, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.opened++
	pr := sessionzero.TestPR{Number: 7, Branch: "mestre/teste-aprovacao"}
	if f.failAt == "open" {
		return pr, errFake // failed after creating the branch
	}
	return pr, nil
}

func (f *fakes) ApproveAsGrandMaster(context.Context, workspaces.Repo, sessionzero.TestPR) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.approvals++
	if f.failAt == "approve" {
		return errFake
	}
	return nil
}

func (f *fakes) ReviewDecision(context.Context, workspaces.Repo, sessionzero.TestPR) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failAt == "decision" {
		return "", errFake
	}
	return f.decision, nil
}

func (f *fakes) CloseTestPR(context.Context, workspaces.Repo, sessionzero.TestPR) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

// walkFiles lists the files of a folder without .git, with slashes.
func walkFiles(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(out)
	return out
}
