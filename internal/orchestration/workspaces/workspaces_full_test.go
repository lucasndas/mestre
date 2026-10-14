//go:build full

package workspaces_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/pkg/clock"
)

// realRig is a Service over the real git, with a local repository standing
// for the one on GitHub.
type realRig struct {
	t      *testing.T
	ctx    context.Context
	clk    *clock.Fake
	git    *gitx.Real
	remote gitx.Repo
	svc    *workspaces.Service
	fake   *rig // carries the state the fakes of the ports share
}

func newRealRig(t *testing.T) *realRig {
	t.Helper()
	g, err := gitx.New(gitx.Options{Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	// The shared state of the fakes (alerts, Grand Master) is a rig without
	// the fake git; its Service is not used.
	shared := &rig{t: t, ctx: t.Context(), clk: clock.NewFake(t0), root: t.TempDir(), gmState: workspaces.GMIdle, fetched: make(chan struct{}, 64)}
	rr := &realRig{t: t, ctx: t.Context(), clk: shared.clk, git: g, fake: shared}
	remote, err := g.Init(rr.ctx, filepath.Join(shared.root, "remote", "meu-app"), gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	rr.remote = remote
	rr.commit("chore: initial commit", map[string]string{"README.md": "# Meu App\n"})
	db, err := guilddb.Open(rr.ctx, filepath.Join(shared.root, "mestre.db"), store.Options{Synchronous: "OFF", Clock: shared.clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	gh := &remoteGitHub{url: remote.Dir()}
	rr.svc, err = workspaces.New(workspaces.Options{
		DB: db, Git: g, Layout: workspaces.Layout{Root: filepath.Join(shared.root, "workspaces")}, Clock: shared.clk,
		GitHub: gh, Alerts: (*alertFake)(shared), GrandMaster: (*gmFake)(shared),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := rr.svc.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	return rr
}

type remoteGitHub struct{ url string }

func (g *remoteGitHub) Repository(_ context.Context, repo workspaces.Repo) (workspaces.RepoInfo, error) {
	return workspaces.RepoInfo{FullName: repo.String(), CloneURL: g.url, DefaultBranch: "main"}, nil
}

// commit makes a commit in the repository that stands for GitHub.
func (rr *realRig) commit(msg string, files map[string]string) gitx.Hash {
	rr.t.Helper()
	for name, body := range files {
		p := filepath.Join(rr.remote.Dir(), name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			rr.t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			rr.t.Fatal(err)
		}
	}
	if err := rr.remote.Stage(rr.ctx); err != nil {
		rr.t.Fatal(err)
	}
	sig := gitx.Signature{Name: "Teste", Email: "teste@example.invalid", When: t0}
	h, err := rr.remote.Commit(rr.ctx, gitx.CommitOptions{Message: msg, Author: sig})
	if err != nil {
		rr.t.Fatal(err)
	}
	return h
}

func (rr *realRig) create() workspaces.Workspace {
	rr.t.Helper()
	w, err := rr.svc.Create(rr.ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		rr.t.Fatal(err)
	}
	return w
}

func (rr *realRig) head(w workspaces.Workspace) string {
	rr.t.Helper()
	return strings.TrimSpace(testkit.ProjectGit(rr.t, rr.svc.Layout().Principal(w.Slug), "rev-parse", "HEAD"))
}

// Part of CA-WS-02: the workspace of a local repository, with the real git:
// the mirror is bare and has the refspec, the main clone is on the main
// branch, and the workspace is active only after the last stage; a table does
// not open before that. The stages other than the mirror are the session
// zero's (their own runners close the criterion), and `table open` is a later
// stage of milestone 4: here it is the gate the tables will ask.
func TestRealWorkspaceHasTheMirrorAndTheMainCloneAndIsActiveAfterTheStages(t *testing.T) {
	t.Parallel()
	rr := newRealRig(t)
	w := rr.create()
	if err := rr.svc.CheckOpenTable(rr.ctx, w.Slug); !errors.Is(err, workspaces.ErrNotActive) {
		t.Fatalf("table open before the stages = %v; want it refused", err)
	}
	if err := rr.svc.Prepare(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if got, _ := rr.svc.Get(rr.ctx, w.Slug); got.State != workspaces.StateActive {
		t.Fatalf("state = %s; want active", got.State)
	}
	layout := rr.svc.Layout()
	mirror, principal := layout.Mirror(w.Slug), layout.Principal(w.Slug)
	if got := strings.TrimSpace(testkit.ProjectGit(t, mirror, "rev-parse", "--is-bare-repository")); got != "true" {
		t.Fatalf("the mirror is not bare: %q", got)
	}
	if got := strings.TrimSpace(testkit.ProjectGit(t, mirror, "config", "--get", "remote.origin.fetch")); got != "+refs/heads/*:refs/remotes/origin/*" {
		t.Fatalf("remote.origin.fetch = %q", got)
	}
	if got := strings.TrimSpace(testkit.ProjectGit(t, mirror, "rev-parse", "refs/remotes/origin/main")); got != rr.head(w) {
		t.Fatalf("origin/main = %s; the main clone is at %s", got, rr.head(w))
	}
	if got := strings.TrimSpace(testkit.ProjectGit(t, principal, "symbolic-ref", "--short", "HEAD")); got != "main" {
		t.Fatalf("the main clone is on %q; want main", got)
	}
	if body, err := os.ReadFile(filepath.Join(principal, "README.md")); err != nil || string(body) != "# Meu App\n" {
		t.Fatalf("README in the main clone = %q, %v", body, err)
	}
	if fi, err := os.Stat(filepath.Join(principal, ".git")); err != nil || fi.IsDir() {
		t.Fatalf(".git of the main clone = %v, %v; want the file of a worktree", fi, err)
	}
	// The record the engine derives from the layout is the one git reports.
	rec, err := rr.git.Record(rr.ctx, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rr.git.OpenWorktree(rr.ctx, rec); err != nil {
		t.Fatalf("the main clone does not open by its record: %v", err)
	}
	// The creation can be repeated after a failure: what exists is reused.
	if err := rr.svc.CreateMirror(rr.ctx, w); err != nil {
		t.Fatalf("CreateMirror on an existing mirror: %v", err)
	}
}

// The scenario of CA-WS-06 with the real git: the fast-forward waits for the
// Grand Master and for a clean main clone.
func TestRealMainCloneAdvancesWithFFOnlyWhenTheGrandMasterIsIdleAndTheCloneIsClean(t *testing.T) {
	t.Parallel()
	rr := newRealRig(t)
	w := rr.create()
	if err := rr.svc.Prepare(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	before := rr.head(w)
	tip := rr.commit("feat: ahead", map[string]string{"a.txt": "a\n"})

	rr.fake.setGM(workspaces.GMBusy)
	if err := rr.svc.Fetch(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if got := rr.head(w); got != before {
		t.Fatalf("busy Grand Master: head %s, want %s", got, before)
	}
	rr.fake.setGM(workspaces.GMIdle)
	if err := os.WriteFile(filepath.Join(rr.svc.Layout().Principal(w.Slug), "wip.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rr.svc.Fetch(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if got := rr.head(w); got != before {
		t.Fatalf("dirty main clone: head %s, want %s", got, before)
	}
	if a, _ := rr.svc.Get(rr.ctx, w.Slug); !strings.Contains(a.Advance.Reason, "alterações locais") {
		t.Fatalf("advance status = %+v", a.Advance)
	}
	if err := os.Remove(filepath.Join(rr.svc.Layout().Principal(w.Slug), "wip.txt")); err != nil {
		t.Fatal(err)
	}
	if err := rr.svc.Fetch(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if got := rr.head(w); got != string(tip) {
		t.Fatalf("idle and clean: head %s, want %s", got, tip)
	}
	if body, err := os.ReadFile(filepath.Join(rr.svc.Layout().Principal(w.Slug), "a.txt")); err != nil || string(body) != "a\n" {
		t.Fatalf("a.txt in the main clone = %q, %v", body, err)
	}
}

// An agent that rewrites the .git file of the main clone (the folder of the
// Grand Master) does not make the engine run git on a repository of its own.
func TestRealMainCloneRewrittenByAnAgentIsNotUsed(t *testing.T) {
	t.Parallel()
	rr := newRealRig(t)
	w := rr.create()
	if err := rr.svc.Prepare(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	before := rr.head(w)
	evil := filepath.Join(rr.fake.root, "evil")
	testkit.ProjectGit(t, rr.fake.root, "init", "--bare", "-b", "main", evil)
	rr.commit("feat: ahead", map[string]string{"a.txt": "a\n"})
	dotgit := filepath.Join(rr.svc.Layout().Principal(w.Slug), ".git")
	if err := os.WriteFile(dotgit, []byte("gitdir: "+evil+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rr.svc.Fetch(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if got := rr.fake.alerts(); len(got) != 1 || got[0].Kind != workspaces.AlertWorktreeMoved {
		t.Fatalf("alerts = %+v; want one about the main clone that moved", got)
	}
	if got := strings.TrimSpace(testkit.ProjectGit(t, rr.svc.Layout().Mirror(w.Slug), "rev-parse", "refs/heads/main")); got != before {
		t.Fatalf("the branch of the mirror moved to %s", got)
	}
}

func TestRealFetchFailureIsRecordedAndTheMirrorKeepsWorking(t *testing.T) {
	t.Parallel()
	rr := newRealRig(t)
	w := rr.create()
	if err := rr.svc.Prepare(rr.ctx, w.Slug); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rr.remote.Dir(), rr.remote.Dir()+".gone"); err != nil {
		t.Fatal(err)
	}
	rr.clk.Advance(time.Minute)
	if err := rr.svc.Fetch(rr.ctx, w.Slug); err == nil {
		t.Fatal("Fetch of a remote that is gone = nil")
	}
	got, _ := rr.svc.Get(rr.ctx, w.Slug)
	if got.Fetch.FailingSince.IsZero() || got.Fetch.Error == "" {
		t.Fatalf("fetch status = %+v", got.Fetch)
	}
	if _, err := rr.git.Open(rr.ctx, rr.svc.Layout().Mirror(w.Slug)); err != nil {
		t.Fatalf("the mirror does not open: %v", err)
	}
}
