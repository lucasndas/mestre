package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/quests"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
	"github.com/lucasndas/mestre/internal/orchestration/verification"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/testkit/orcfakes"
	"github.com/lucasndas/mestre/pkg/clock"
)

func TestTheShellRunsOneCommandLineOnEachSystem(t *testing.T) {
	t.Parallel()
	if p, a := shellFor("linux", "make test"); p != "/bin/sh" || len(a) != 2 || a[0] != "-c" || a[1] != "make test" {
		t.Errorf("linux = %q %q", p, a)
	}
	if p, a := shellFor("windows", "make test"); !strings.HasSuffix(p, `\cmd.exe`) || !strings.Contains(p, `:\`) || a[len(a)-1] != "make test" || a[len(a)-2] != "/c" {
		t.Errorf("windows = %q %q", p, a)
	}
}

// The environment of a command of the verification is made from nothing: only
// the search path, the scratch folder and, on Windows, what a shell needs. A
// secret of the engine, the vault or the shell that started it never reaches it.
func TestTheCommandsOfTheVerificationGetNoVariableOfTheEngine(t *testing.T) {
	t.Parallel()
	engine := map[string]string{
		"PATH": "/usr/bin", "HOME": "/home/engine", "GITHUB_TOKEN": "ghs_x", "MESTRE_VAULT_KEY": "k", "ANTHROPIC_API_KEY": "sk-x",
		"SystemRoot": `C:\Windows`, "ComSpec": `C:\Windows\system32\cmd.exe`, "AWS_SECRET_ACCESS_KEY": "s",
	}
	for _, goos := range []string{"linux", "windows"} {
		env := verifyEnv(goos, "/scratch", func(k string) string { return engine[k] })
		for _, kv := range env {
			k, v, _ := strings.Cut(kv, "=")
			switch k {
			case "GITHUB_TOKEN", "MESTRE_VAULT_KEY", "ANTHROPIC_API_KEY", "AWS_SECRET_ACCESS_KEY":
				t.Errorf("%s: %s reached the command", goos, k)
			case "HOME", "TMPDIR":
				if v != "/scratch" {
					t.Errorf("%s: %s = %q, want the scratch folder", goos, k, v)
				}
			}
		}
		if !slices.Contains(env, "PATH=/usr/bin") || !slices.Contains(env, "CI=1") {
			t.Errorf("%s: env = %v", goos, env)
		}
		if has := slices.Contains(env, `SystemRoot=C:\Windows`); has != (goos == "windows") {
			t.Errorf("%s: SystemRoot passed = %v", goos, has)
		}
	}
	if env := verifyEnv("linux", "", func(string) string { return "" }); slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "HOME=") }) {
		t.Errorf("a HOME of nothing: %v", env)
	}
}

type verifyRig struct {
	orc    *Orchestration
	ws     workspaces.Workspace
	opened tables.OpenResult
	w      mirrorWorktrees
	fg     *fakegit.Git
	db     *store.DB
	clk    clock.Clock
}

// newVerifyRig is a workspace with a table open, over the fake git.
func newVerifyRig(t *testing.T) *verifyRig {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC))
	root := t.TempDir()
	db, err := guildtpl.Open(ctx, filepath.Join(root, "stack.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	fg := fakegit.NewGit(clk)
	remote, err := fg.Init(ctx, filepath.Join(root, "remote", "meu-app"), gitx.InitOptions{})
	if err != nil {
		t.Fatal(err)
	}
	fg.Handle(remote.Dir()).Data().Commit("main", "chore: initial commit", map[string]string{"README.md": "# Meu App\n"})
	machines := orcfakes.NewMachines(clk)
	var orc *Orchestration
	machines.Chars = func() *characters.Service { return orc.Characters }
	orc, err = NewOrchestration(OrchestrationOptions{Guild: db, Machine: "cidadela", Clock: clk, WorkspacesDir: filepath.Join(root, "workspaces"), TempDir: filepath.Join(root, "tmp"),
		Git: fg, Machines: machines, Presets: orcfakes.Presets{}, Builder: orcfakes.Builder{}, GitHub: orcfakes.Hub{URL: remote.Dir()}, Epics: orcfakes.Epics{}, Alerts: orcfakes.Alerts{},
		Files: orcfakes.Files{}, Messages: orcfakes.Messages{}, Peers: &orcfakes.Peers{Root: filepath.Join(root, "towers")}})
	if err != nil {
		t.Fatal(err)
	}
	machines.Confirm = orc.Inbox.Confirmed
	ws, err := orc.Workspaces.Create(ctx, workspaces.CreateRequest{Repo: "lucas/Meu_App"})
	if err != nil {
		t.Fatal(err)
	}
	for _, st := range workspaces.Stages {
		if st != workspaces.StageMirror {
			if err := orc.Workspaces.Register(st, func(context.Context, workspaces.Workspace) error { return nil }); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := orc.Workspaces.Prepare(ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	gm, err := orc.Characters.Create(ctx, characters.CreateRequest{Sheet: "Grão-Mestre", Workspace: ws.Slug, Role: characters.RoleGrandMaster, Creator: characters.Creator{Kind: characters.CreatedByEngine}})
	if err != nil {
		t.Fatal(err)
	}
	opened, err := orc.Tables.Open(ctx, tables.OpenRequest{By: gm.Character, Epic: 42})
	if err != nil {
		t.Fatal(err)
	}
	return &verifyRig{orc: orc, ws: ws, opened: opened, w: mirrorWorktrees{orc: func() *Orchestration { return orc }, git: fg, clk: clk, log: slog.New(slog.DiscardHandler)}, fg: fg, db: db, clk: clk}
}

func (r *verifyRig) mirror(t *testing.T) gitx.Repo {
	t.Helper()
	m, err := r.orc.Workspaces.OpenMirror(t.Context(), r.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (r *verifyRig) req() verification.TreeRequest {
	return verification.TreeRequest{Workspace: r.ws.Slug, Table: r.opened.Table.ID, Branch: r.opened.Table.Branch}
}

// The ephemeral worktree of the verification is made on a branch of its own at
// the tip of the branch of the table, in the folder of the tables, and takes
// both away when it is removed; the branch of the table is never checked out
// there (the worktree of the table has it).
func TestTheEphemeralWorktreeOfTheVerificationIsMadeAtTheTipOfTheBranchAndRemoved(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rg := newVerifyRig(t)
	tree, remove, err := rg.w.Add(ctx, rg.req())
	if err != nil {
		t.Fatal(err)
	}
	if want := rg.orc.Workspaces.Layout().Verifications(rg.ws.Slug); filepath.Dir(tree.Dir) != want || !strings.HasPrefix(filepath.Base(tree.Dir), "verifica-") || tree.Home != tree.Dir+".home" {
		t.Errorf("the worktree is %+v, want a verifica-* folder in %s", tree, want)
	}
	if st, err := os.Stat(tree.Home); err != nil || !st.IsDir() {
		t.Errorf("the scratch folder of the commands: %v", err)
	}
	mirror := rg.mirror(t)
	refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre")
	if len(refs) != 1 {
		t.Fatalf("branches of the verification = %v", refs)
	}
	if tip, _ := mirror.ResolveRef(ctx, "refs/heads/"+rg.opened.Table.Branch); refs[0].Hash != tip {
		t.Errorf("the worktree is at %s, the branch of the table at %s", refs[0].Hash, tip)
	}
	remove()
	if refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre"); len(refs) != 0 {
		t.Errorf("the branch of the verification stayed: %v", refs)
	}
	if _, err := os.Stat(tree.Home); err == nil {
		t.Error("the scratch folder stayed")
	}
	if _, _, err := rg.w.Add(ctx, verification.TreeRequest{Workspace: rg.ws.Slug, Table: "x", Branch: "does-not-exist"}); err == nil {
		t.Error("a worktree was made on a branch that is not in the mirror")
	}
}

// The gate proves the head of the PR: a branch that is not at the commit asked
// for is a red result (ErrHeadMoved), and nothing is left behind.
func TestAWorktreeIsNotMadeForAHeadTheBranchIsNotAt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rg := newVerifyRig(t)
	mirror := rg.mirror(t)
	tip, _ := mirror.ResolveRef(ctx, "refs/heads/"+rg.opened.Table.Branch)
	r := rg.req()
	r.Head = string(tip)
	_, remove, err := rg.w.Add(ctx, r)
	if err != nil {
		t.Fatalf("the head the branch is at: %v", err)
	}
	remove()
	r.Head = strings.Repeat("a", 40)
	if _, _, err := rg.w.Add(ctx, r); !errors.Is(err, verification.ErrHeadMoved) {
		t.Fatalf("a head the branch is not at: %v", err)
	}
	r.Head = "not-a-commit"
	if _, _, err := rg.w.Add(ctx, r); err == nil || errors.Is(err, verification.ErrHeadMoved) {
		t.Fatalf("a head that is not a commit: %v", err)
	}
	if refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre"); len(refs) != 0 {
		t.Errorf("branches of the verification stayed: %v", refs)
	}
}

// The base is merged into the ephemeral worktree only: the merge is on the
// branch of the verification, which goes away, and the branch of the table does
// not move. A base that conflicts is ErrBaseConflict and leaves nothing.
func TestTheBaseIsMergedIntoTheEphemeralWorktreeOnlyAndAConflictIsRed(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rg := newVerifyRig(t)
	mirror := rg.mirror(t)
	branch := rg.opened.Table.Branch
	tip := rg.commitOn(t, branch, "feature.txt", "table\n")

	// a commit of the base that the branch does not have, on another file
	base := rg.commitOn(t, "main", "base.txt", "base\n")
	r := rg.req()
	r.Head, r.Base = string(tip), string(base)
	tree, remove, err := rg.w.Add(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre")
	if len(refs) != 1 {
		t.Fatalf("branches of the verification = %v", refs)
	}
	for f, want := range map[string]string{"base.txt": "base\n", "feature.txt": "table\n"} {
		if got, err := mirror.ReadFile(ctx, refs[0].Name, f); err != nil || string(got) != want {
			t.Errorf("%s in the worktree = %q, %v; want %q", f, got, err, want)
		}
	}
	if now, _ := mirror.ResolveRef(ctx, "refs/heads/"+branch); now != tip {
		t.Errorf("the branch of the table moved from %s to %s", tip, now)
	}
	_ = tree
	remove()

	// the same file changed on both sides
	conflict := rg.commitOn(t, "main", "feature.txt", "base edit\n")
	r.Base = string(conflict)
	if _, _, err := rg.w.Add(ctx, r); !errors.Is(err, verification.ErrBaseConflict) {
		t.Fatalf("a base that conflicts: %v", err)
	}
	if refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre"); len(refs) != 0 {
		t.Errorf("branches of the verification stayed after a conflict: %v", refs)
	}
	r.Base = strings.Repeat("b", 40)
	if _, _, err := rg.w.Add(ctx, r); err == nil || errors.Is(err, verification.ErrBaseConflict) {
		t.Fatalf("a base that is not in the mirror: %v", err)
	}
}

// commitOn adds a commit to a branch of the mirror, as an agent that pushed
// into it would, and returns it.
func (r *verifyRig) commitOn(t *testing.T, branch, file, content string) gitx.Hash {
	t.Helper()
	m := r.mirror(t)
	r.fg.Handle(m.Dir()).Data().Commit(branch, "feat: "+file, map[string]string{file: content})
	got, err := m.ResolveRef(t.Context(), "refs/heads/"+branch)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

type recordingRunner struct {
	mu   sync.Mutex
	ran  []verification.Run
	code int
	// seen is read when a command runs: what the worktree of the verification holds.
	seen func(verification.Run)
}

func (r *recordingRunner) Run(_ context.Context, run verification.Run) (verification.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ran = append(r.ran, run)
	if r.seen != nil {
		r.seen(run)
	}
	return verification.Result{ExitCode: r.code, Output: "feito"}, nil
}

// The executor, over the worktrees of the mirror, honors the Base and the Head
// of the request (the gate of freshness depends on it): the commands run in a
// worktree that holds the base merged into the head, the commands are the ones
// confirmed for the workspace whatever the branch carries, and a head that
// moved, or a base that conflicts, is red without a command run.
func TestTheExecutorHonorsTheBaseAndTheHeadOfTheRequest(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rg := newVerifyRig(t)
	mirror := rg.mirror(t)
	branch := rg.opened.Table.Branch
	// the branch of the table carries its own verificacao.yaml: it is never read
	head := rg.commitOn(t, branch, ".mestre/regras/verificacao.yaml", "version: 1\ncomandos: [\"true\"]\n")
	base := rg.commitOn(t, "main", "base.txt", "base\n")
	store := verification.Store{DB: rg.db, Clock: rg.clk}
	if _, err := store.Confirm(ctx, rg.ws.Slug, "u1", []byte("version: 1\ncomandos: [\"make check\"]\n")); err != nil {
		t.Fatal(err)
	}
	var inTree bool
	run := &recordingRunner{}
	run.seen = func(verification.Run) {
		refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre")
		if len(refs) == 1 {
			b, err := mirror.ReadFile(ctx, refs[0].Name, "base.txt")
			inTree = err == nil && string(b) == "base\n"
		}
	}
	ex := &verification.Executor{Store: store, Worktrees: rg.w, Runner: run}
	var _ quests.Verifier = ex
	res, err := ex.Verify(ctx, quests.VerifyRequest{Workspace: rg.ws.Slug, Table: rg.opened.Table.ID, Branch: branch, Head: string(head), Base: string(base)})
	if err != nil || !res.OK {
		t.Fatalf("Verify = %+v, %v", res, err)
	}
	if len(run.ran) != 1 || run.ran[0].Command != "make check" {
		t.Errorf("ran %+v, want only the confirmed command", run.ran)
	}
	if !inTree {
		t.Error("the base was not in the worktree the command ran in")
	}
	if refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre"); len(refs) != 0 {
		t.Errorf("the worktree stayed: %v", refs)
	}

	// the branch moved after the head was read
	rg.commitOn(t, branch, "mais.txt", "x\n")
	res, err = ex.Verify(ctx, quests.VerifyRequest{Workspace: rg.ws.Slug, Table: rg.opened.Table.ID, Branch: branch, Head: string(head), Base: string(base)})
	if err != nil || res.OK || len(run.ran) != 1 {
		t.Fatalf("a head that moved: %+v, %v, ran %d", res, err, len(run.ran))
	}
	// a base that conflicts with the head
	moved := rg.commitOn(t, branch, "base.txt", "branch edit\n")
	res, err = ex.Verify(ctx, quests.VerifyRequest{Workspace: rg.ws.Slug, Table: rg.opened.Table.ID, Branch: branch, Head: string(moved), Base: string(base)})
	if err != nil || res.OK || len(run.ran) != 1 {
		t.Fatalf("a base that conflicts: %+v, %v, ran %d", res, err, len(run.ran))
	}
	// without Head and Base the tip is proved
	res, err = ex.Verify(ctx, quests.VerifyRequest{Workspace: rg.ws.Slug, Table: rg.opened.Table.ID, Branch: branch})
	if err != nil || !res.OK || len(run.ran) != 2 {
		t.Fatalf("the tip: %+v, %v, ran %d", res, err, len(run.ran))
	}
	// a red command is red
	run.code = 2
	res, err = ex.Verify(ctx, quests.VerifyRequest{Workspace: rg.ws.Slug, Table: rg.opened.Table.ID, Branch: branch})
	if err != nil || res.OK || res.Command != "make check" {
		t.Fatalf("a red command: %+v, %v", res, err)
	}
}

// What a fall of the engine left of the verifications (the worktree and its
// folders, the branches of the mirror) is removed when the period starts; a
// folder or a branch of anything else is left alone.
func TestTheSweepRemovesWhatAFallLeftOfTheVerificationsAndNothingElse(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	rg := newVerifyRig(t)
	rg.orc.log = slog.New(slog.DiscardHandler)
	mirror := rg.mirror(t)
	// a verification of this run of the engine, under way: the sweep leaves it alone
	live, remove, err := rg.w.Add(ctx, rg.req())
	if err != nil {
		t.Fatal(err)
	}
	// what an earlier run left: a worktree with its branch, a folder, a branch
	dir := rg.orc.Workspaces.Layout().Verifications(rg.ws.Slug)
	old := filepath.Join(dir, "verifica-deadbeef-1")
	if _, err := mirror.AddWorktree(ctx, old, gitx.WorktreeOptions{Branch: "mestre/verifica-deadbeef-1", Create: true, StartPoint: "refs/heads/" + rg.opened.Table.Branch}); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(dir, "verifica-deadbeef-2.home")
	if err := os.MkdirAll(filepath.Join(stray, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "not-a-verification")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, b := range []string{"mestre/verifica-orphan", "mestre/other"} {
		if err := mirror.CreateBranch(ctx, b, "refs/heads/"+rg.opened.Table.Branch); err != nil {
			t.Fatal(err)
		}
	}
	rg.orc.sweepVerifications(ctx)
	for _, p := range []string{stray} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was left", p)
		}
	}
	for _, p := range []string{live.Home, other} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s was removed: %v", p, err)
		}
	}
	var names []string
	refs, _ := mirror.ListRefs(ctx, "refs/heads/mestre")
	for _, r := range refs {
		names = append(names, strings.TrimPrefix(r.Name, "refs/heads/mestre/"))
	}
	if len(names) != 2 || !slices.Contains(names, "other") || !slices.ContainsFunc(names, func(n string) bool { return strings.HasPrefix(n, verifyBoot) }) {
		t.Errorf("branches after the sweep: %v", names)
	}
	wts, _ := mirror.ListWorktrees(ctx)
	if slices.ContainsFunc(wts, func(w gitx.Worktree) bool { return filepath.Base(w.Path) == "verifica-deadbeef-1" }) {
		t.Errorf("the worktree of an earlier run stayed: %v", wts)
	}
	if !slices.ContainsFunc(wts, func(w gitx.Worktree) bool { return strings.HasPrefix(filepath.Base(w.Path), verifyBoot) }) {
		t.Errorf("the worktree of this run was removed: %v", wts)
	}
	remove()
	if tip, err := mirror.ResolveRef(ctx, "refs/heads/"+rg.opened.Table.Branch); err != nil || tip == "" {
		t.Errorf("the branch of the table: %v", err)
	}
}
