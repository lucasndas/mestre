//go:build full

package sessionzero_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/sessionzero"
	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit"
	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
	"github.com/lucasndas/mestre/pkg/clock"
)

var sig = gitx.Signature{Name: "Teste", Email: "teste@example.invalid", When: time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)}

// mesaRig is the real git of a project and the mirrors of a Citadel and of a
// Tower, each with its worktrees opened by their record.
type mesaRig struct {
	t    *testing.T
	ctx  context.Context
	git  *gitx.Real
	root string
}

func newMesaRig(t *testing.T) *mesaRig {
	t.Helper()
	g, err := gitx.New(gitx.Options{Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	return &mesaRig{t: t, ctx: t.Context(), git: g, root: t.TempDir()}
}

// project makes the repository that stands for GitHub.
func (r *mesaRig) project(files map[string]string) gitx.Repo {
	r.t.Helper()
	repo, err := r.git.Init(r.ctx, filepath.Join(r.root, "github", "meu-app"), gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		r.t.Fatal(err)
	}
	for name, body := range mergeFiles(map[string]string{"README.md": "# Meu App\n"}, files) {
		write(r.t, filepath.Join(repo.Dir(), filepath.FromSlash(name)), body)
	}
	if err := repo.Stage(r.ctx); err != nil {
		r.t.Fatal(err)
	}
	if _, err := repo.Commit(r.ctx, gitx.CommitOptions{Message: "chore: initial commit", Author: sig}); err != nil {
		r.t.Fatal(err)
	}
	return repo
}

// mesa is a worktree of a table in the mirror of a machine.
type mesa struct {
	dir    string
	repo   gitx.Repo
	mirror string
}

// worktree makes the mirror of the machine (once) and the worktree of a table.
func (r *mesaRig) worktree(src gitx.Repo, machine, table string) mesa {
	r.t.Helper()
	mirrorDir := filepath.Join(r.root, machine, "espelho.git")
	mirror, err := r.git.Open(r.ctx, mirrorDir)
	if err != nil {
		mirror, err = r.git.Clone(r.ctx, src.Dir(), mirrorDir, gitx.CloneOptions{Bare: true})
		if err != nil {
			r.t.Fatal(err)
		}
	}
	dir := filepath.Join(r.root, machine, "mesas", table)
	if _, err := mirror.AddWorktree(r.ctx, dir, gitx.WorktreeOptions{Branch: "mesa/" + table, Create: true, StartPoint: "main"}); err != nil {
		r.t.Fatal(err)
	}
	rec, err := r.git.Record(r.ctx, dir)
	if err != nil {
		r.t.Fatal(err)
	}
	repo, err := r.git.OpenWorktree(r.ctx, rec)
	if err != nil {
		r.t.Fatal(err)
	}
	return mesa{dir: dir, repo: repo, mirror: mirrorDir}
}

func (r *mesaRig) table(binary string, opts func(*sessionzero.TableOptions)) *sessionzero.Table {
	r.t.Helper()
	o := sessionzero.TableOptions{Worktree: sessionzero.Worktree{Providers: providers.NewRegistry(providers.Options{}), Binary: binary}}
	if opts != nil {
		opts(&o)
	}
	tb, err := sessionzero.NewTable(o)
	if err != nil {
		r.t.Fatal(err)
	}
	return tb
}

func (r *mesaRig) profiles(machine string) []sessionzero.Profile {
	r.t.Helper()
	var out []sessionzero.Profile
	for _, id := range []providers.ID{providers.ClaudeCode, providers.Codex, providers.Antigravity} {
		dir := filepath.Join(r.root, machine, "perfis", string(id))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			r.t.Fatal(err)
		}
		out = append(out, sessionzero.Profile{Provider: id, Dir: dir})
	}
	return out
}

func (r *mesaRig) status(dir string) string {
	return strings.TrimSpace(testkit.ProjectGit(r.t, dir, "status", "--porcelain"))
}

// tableFiles are the files of the table in docs/sessao-zero/mesa.md, with a
// class and an attachment.
var tableFiles = []string{
	".agents/skills/mestre-comunicacao/SKILL.md",
	".claude/skills/mestre-comunicacao/SKILL.md",
	".claude/settings.local.json",
	".agents/hooks.json",
	".mestre/run/classes/revisor/CLASS.md",
	".mestre/anexos/print.png",
}

func (r *mesaRig) options(o *sessionzero.TableOptions) {
	o.Classes = &classFake{files: map[string]classes.Files{"revisor": {{Path: "CLASS.md", Data: []byte("# revisor\n")}}}}
	o.Attachments = &attachFake{files: []sessionzero.File{{Path: "print.png", Data: []byte{1, 2, 3}}}}
}

// CA-SZ-MESA-05
func TestRealTableOnTheCitadelAndOnATowerHasAllTheFilesAndAnEmptyStatus(t *testing.T) {
	t.Parallel()
	r := newMesaRig(t)
	src := r.project(nil)
	binary := filepath.Join(r.root, "bin", "mestre")
	for _, c := range []struct {
		machine       string
		excludeMestre bool
	}{{"cidadela", false}, {"torre", true}} {
		m := r.worktree(src, c.machine, "3-login")
		tb := r.table(binary, r.options)
		rep, err := tb.Prepare(r.ctx, sessionzero.TableRequest{
			Workspace: "meu-app", Table: "3-login", Dir: m.dir, Repo: m.repo, RepoRoot: m.mirror, ExcludeMestre: c.excludeMestre,
			Classes: []string{"revisor"}, Profiles: r.profiles(c.machine),
		})
		if err != nil || len(rep.Problems) != 0 {
			t.Fatalf("%s: %v, problems %v", c.machine, err, rep.Problems)
		}
		for _, rel := range tableFiles {
			if _, err := os.Stat(filepath.Join(m.dir, filepath.FromSlash(rel))); err != nil {
				t.Errorf("%s: %s: %v", c.machine, rel, err)
			}
		}
		if got := r.status(m.dir); got != "" {
			t.Errorf("%s: git status --porcelain =\n%s\nwant it empty", c.machine, got)
		}
		// The files of the table are also invisible to a status that sees
		// untracked folders whole: nothing of the Mestre but .mestre/ itself.
		if c.excludeMestre {
			if _, err := os.Stat(filepath.Join(m.dir, ".mestre")); err != nil {
				t.Errorf("%s: .mestre: %v", c.machine, err)
			}
		}
		// A second run leaves the status empty too.
		if _, err := tb.Prepare(r.ctx, sessionzero.TableRequest{Workspace: "meu-app", Table: "3-login", Dir: m.dir, Repo: m.repo, RepoRoot: m.mirror, ExcludeMestre: c.excludeMestre, Classes: []string{"revisor"}, Profiles: r.profiles(c.machine)}); err != nil || r.status(m.dir) != "" {
			t.Errorf("%s: second run: %v, status %q", c.machine, err, r.status(m.dir))
		}
	}
}

// Part of CA-SZ-MESA-04, with the real file: the lines of the person in
// info/exclude stay and there is one block, which is the one of the document.
func TestRealExcludeFileKeepsThePersonAndHasTheBlockOfTheDocument(t *testing.T) {
	t.Parallel()
	r := newMesaRig(t)
	m := r.worktree(r.project(nil), "cidadela", "3-login")
	file := filepath.Join(m.mirror, "info", "exclude")
	const person = "# minhas linhas\n*.log\n/build/\n"
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		t.Fatal(err)
	}
	write(t, file, person)
	tb := r.table(filepath.Join(r.root, "bin", "mestre"), nil)
	req := sessionzero.TableRequest{Workspace: "meu-app", Table: "3-login", Dir: m.dir, Repo: m.repo, RepoRoot: m.mirror, ExcludeMestre: true}
	for range 2 {
		if _, err := tb.Prepare(r.ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	req.ExcludeMestre = false
	if _, err := tb.Prepare(r.ctx, req); err != nil {
		t.Fatal(err)
	}
	req.ExcludeMestre = true
	if _, err := tb.Prepare(r.ctx, req); err != nil {
		t.Fatal(err)
	}
	got := read(t, file)
	if !strings.HasPrefix(got, person) || strings.Count(got, "# >>> mestre:") != 1 || strings.Count(got, "# <<< mestre:") != 1 {
		t.Fatalf("info/exclude:\n%s", got)
	}
	doc := read(t, filepath.Join("..", "..", "docs", "sessao-zero", "mesa.md"))
	_, sec, _ := strings.Cut(doc, "### `.git/info/exclude`")
	m2 := regexp.MustCompile("(?s)```\n(.*?)```").FindStringSubmatch(sec)
	if m2 == nil || strings.TrimPrefix(got, person) != m2[1] {
		t.Fatalf("the block in the file is not the one of the document:\n%s", strings.TrimPrefix(got, person))
	}
}

// CA-SZ-MESA-06
func TestRealVersionedHookFileIsMergedButNeverCommittedAndHarmlessWithoutTheMestre(t *testing.T) { //nolint:paralleltest // the fake agent runs in a synctest bubble
	r := newMesaRig(t)
	const project = `{"projeto": {"enabled": true, "Stop": [{"type": "command", "command": "./checar.sh", "timeout": 5}]}}`
	src := r.project(map[string]string{".agents/hooks.json": project})
	m := r.worktree(src, "cidadela", "3-login")
	binary := filepath.Join(r.root, "bin", "mestre") // where nothing is installed
	tb := r.table(binary, nil)
	req := sessionzero.TableRequest{Workspace: "meu-app", Table: "3-login", Dir: m.dir, Repo: m.repo, RepoRoot: m.mirror, ExcludeMestre: true}
	if _, err := tb.Prepare(r.ctx, req); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(m.dir, ".agents", "hooks.json")
	merged := read(t, hooks)
	if !strings.Contains(merged, `"mestre"`) || !strings.Contains(merged, "projeto") {
		t.Fatalf("hooks.json was not merged: %s", merged)
	}
	if got := r.status(m.dir); got != "" {
		t.Fatalf("git status after the merge =\n%s\nwant it empty (skip-worktree)", got)
	}

	// An agent commits everything.
	write(t, filepath.Join(m.dir, "feature.txt"), "feito pelo agente\n")
	if err := m.repo.Stage(r.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.repo.Commit(r.ctx, gitx.CommitOptions{Message: "feat: from the agent", Author: sig}); err != nil {
		t.Fatal(err)
	}
	if body, err := m.repo.ReadFile(r.ctx, "HEAD", ".agents/hooks.json"); err != nil || string(body) != project {
		t.Fatalf("hooks.json in the commit = %q, %v; want the project's, without the hook mestre", body, err)
	}
	files := strings.Fields(testkit.ProjectGit(t, m.dir, "ls-tree", "-r", "--name-only", "HEAD"))
	for _, f := range files {
		if strings.HasPrefix(f, ".mestre/") || strings.Contains(f, "mestre-") || f == ".claude/settings.local.json" {
			t.Errorf("a file of the Mestre is in the commit: %s", f)
		}
	}
	if !slices.Contains(files, "feature.txt") {
		t.Fatalf("the commit does not hold the agent's work: %v", files)
	}
	if strings.Contains(testkit.ProjectGit(t, m.dir, "show", "HEAD"), "mestre hook") {
		t.Fatal("the commit has the hook of the Mestre")
	}

	// If the file with the hook of the Mestre reaches the repository anyway,
	// in a clone without the Mestre the fake Antigravity agent has the
	// PreToolUse allowed.
	clone := filepath.Join(r.root, "outra-maquina", "clone")
	testkit.WriteFiles(t, clone, map[string]string{
		".git/HEAD":   "ref: refs/heads/main\n",
		"script.yaml": "prompts:\n  - match: write\n    steps:\n      - edit: {path: out.txt, content: hi}\n",
	})
	write(t, filepath.Join(clone, ".agents", "hooks.json"), merged)
	home := filepath.Join(r.root, "outra-maquina", "home")
	testkit.WriteFiles(t, home, map[string]string{".gemini/antigravity-cli/antigravity-oauth-token": "{}"})
	if _, err := providers.NewAntigravity(providers.Options{}).PreTrust(providers.PreTrustRequest{Profile: home, Worktree: clone, RepoRoot: clone}); err != nil {
		t.Fatal(err)
	}

	synctest.Test(t, func(t *testing.T) {
		h := &shellHooks{}
		launcher := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: h, Reporter: t}
		proc, err := launcher.Start(t.Context(), terminal.Spec{Path: "agy", Env: []string{"HOME=" + home, "FAKEAGENT_SCRIPT=script.yaml"}, Dir: clone})
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = proc.Kill() }()
		mp := proc.(*fakeagent.MemProcess)
		synctest.Wait()
		if _, err := mp.PTY().Write([]byte("\x1b[200~write it\x1b[201~\r")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		if out := mp.Output(); strings.Contains(out, "blocked") {
			t.Fatalf("a tool was blocked in a clone without the Mestre:\n%s", out)
		}
		if b, err := os.ReadFile(filepath.Join(clone, "out.txt")); err != nil || string(b) != "hi" {
			t.Fatalf("the tool did not run: %q, %v\n%s", b, err, mp.Output())
		}
		h.mu.Lock()
		defer h.mu.Unlock()
		if !slices.Contains(h.events, "PreToolUse") {
			t.Fatalf("the PreToolUse hook did not run: %v", h.events)
		}
		for event, out := range h.out {
			want := "{}"
			if event == "PreToolUse" {
				want = `{"decision":"allow"}`
			}
			if strings.TrimSpace(out) != want {
				t.Errorf("%s answered %q without the Mestre; want the neutral %s", event, out, want)
			}
		}
	})
}

// CA-SZ-MESA-08
func TestHookLineWithoutTheAgentIDAnswersNeutrallyAndDoesNotRunTheMestre(t *testing.T) { //nolint:paralleltest // the stub of the mestre is a script written and run here: a fork of another test in the window would make it "text file busy"
	e := newTableEnv(t, nil, nil)
	binary := stubMestre(t)
	e.opts.Worktree.Binary = binary
	e.rebuild()
	e.prepare()
	claude, agy := hookLines(t, e.dir)
	marker := binary + ".ran"

	for _, c := range []struct {
		name, line, want string
		shell            func(*testing.T, string) *exec.Cmd
	}{
		{"claude-code", claude, "", claudeShell},
		{"antigravity", agy, `{"decision":"allow"}`, antigravityShell},
	} {
		cmd := c.shell(t, c.line)
		if cmd == nil {
			continue
		}
		cmd.Env = envWithout("MESTRE_")
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil || strings.TrimSpace(out.String()) != c.want {
			t.Errorf("%s without MESTRE_AGENT_ID: stdout %q, stderr %q, %v; want the neutral %q and exit 0\nline: %s", c.name, out.String(), errb.String(), err, c.want, c.line)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Errorf("%s ran the mestre without MESTRE_AGENT_ID", c.name)
		}
	}
	if runtime.GOOS == "windows" {
		return
	}
	// With a character the line does call it (the guard is not a stub that always answers).
	cmd := antigravityShell(t, agy)
	cmd.Env = append(envWithout("MESTRE_"), "MESTRE_AGENT_ID=a1")
	if out, err := cmd.Output(); err != nil || !strings.Contains(string(out), "RAN") {
		t.Fatalf("with MESTRE_AGENT_ID: stdout %q, %v; want the mestre to run", out, err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the mestre did not run with MESTRE_AGENT_ID: %v", err)
	}
}

func envWithout(prefix string) []string {
	return slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, prefix) })
}

// shellHooks runs the commands of the hook files for real, in a shell, with
// the environment of a machine without the Mestre.
type shellHooks struct {
	mu     sync.Mutex
	events []string
	out    map[string]string
}

func (h *shellHooks) RunHook(ctx context.Context, c fakeagent.HookCall) (fakeagent.HookResult, error) {
	cmd := antigravityShell(nil, c.Command)
	cmd.Dir = c.Dir
	cmd.Env = envWithout("MESTRE_")
	cmd.Stdin = bytes.NewReader(c.Stdin)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code, err = ee.ExitCode(), nil
	}
	if !strings.Contains(c.Command, "--provider antigravity") {
		return fakeagent.HookResult{Stdout: out.Bytes(), Stderr: errb.Bytes(), ExitCode: code}, err // a hook of the project
	}
	h.mu.Lock()
	h.events = append(h.events, c.Event)
	if h.out == nil {
		h.out = map[string]string{}
	}
	h.out[c.Event] = out.String()
	h.mu.Unlock()
	return fakeagent.HookResult{Stdout: out.Bytes(), Stderr: errb.Bytes(), ExitCode: code}, err
}

// CA-SZ-MESA-07
func TestRealTrustOfTheThreeProvidersGoesWhenTheTableEndsAndTheWorktreeIsRemoved(t *testing.T) {
	t.Parallel()
	r := newMesaRig(t)
	src := r.project(nil)
	m := r.worktree(src, "cidadela", "3-login")
	other := r.worktree(src, "cidadela", "4-pagamento") // trust of another table must stay
	profiles := r.profiles("cidadela")
	tb := r.table(filepath.Join(r.root, "bin", "mestre"), nil)
	req := func(x mesa, table string) sessionzero.TableRequest {
		return sessionzero.TableRequest{Workspace: "meu-app", Table: table, Dir: x.dir, Repo: x.repo, RepoRoot: x.mirror, ExcludeMestre: true, Profiles: profiles}
	}
	for _, p := range []struct {
		m mesa
		t string
	}{{m, "3-login"}, {other, "4-pagamento"}} {
		if rep, err := tb.Prepare(r.ctx, req(p.m, p.t)); err != nil || len(rep.Problems) != 0 {
			t.Fatalf("%v, %v", err, rep.Problems)
		}
	}
	trust := []string{
		filepath.Join(profiles[0].Dir, ".claude.json"),
		filepath.Join(profiles[1].Dir, "config.toml"),
		filepath.Join(profiles[2].Dir, ".gemini", "antigravity-cli", "settings.json"),
	}
	has := func(file, dir string) bool {
		b, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		return holds(string(b), dir)
	}
	for _, f := range trust {
		if !has(f, m.dir) || !has(f, other.dir) {
			t.Fatalf("%s does not hold the trust of both worktrees:\n%s", f, read(t, f))
		}
	}

	// The table ends: the trust goes, then the worktree.
	if err := tb.Release(req(m, "3-login")); err != nil {
		t.Fatal(err)
	}
	if err := r.mirrorOf(m).RemoveWorktree(r.ctx, m.dir, true); err != nil {
		t.Fatal(err)
	}
	for _, f := range trust {
		if has(f, m.dir) {
			t.Errorf("%s still holds the path of the removed worktree:\n%s", f, read(t, f))
		}
		if !has(f, other.dir) {
			t.Errorf("%s lost the trust of another table:\n%s", f, read(t, f))
		}
	}
	if _, err := os.Stat(m.dir); !os.IsNotExist(err) {
		t.Fatalf("the worktree is still there: %v", err)
	}
}

func (r *mesaRig) mirrorOf(m mesa) gitx.Repo {
	r.t.Helper()
	mirror, err := r.git.Open(r.ctx, m.mirror)
	if err != nil {
		r.t.Fatal(err)
	}
	return mirror
}

// The update of the branch (here, the rebase onto a main branch where the
// project changed the hook file itself) works with the real git because the
// flag skip-worktree is off while it runs.
func TestRealRebaseOverAProjectThatChangedTheVersionedHookFile(t *testing.T) {
	t.Parallel()
	r := newMesaRig(t)
	const v1, v2 = `{"projeto": {"enabled": true}}` + "\n", `{"projeto": {"enabled": true}, "novo": {"enabled": false}}` + "\n"
	src := r.project(map[string]string{".agents/hooks.json": v1})
	m := r.worktree(src, "cidadela", "3-login")
	tb := r.table(filepath.Join(r.root, "bin", "mestre"), nil)
	req := sessionzero.TableRequest{Workspace: "meu-app", Table: "3-login", Dir: m.dir, Repo: m.repo, RepoRoot: m.mirror, ExcludeMestre: true}
	if _, err := tb.Prepare(r.ctx, req); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(m.dir, "feature.txt"), "x\n")
	if err := m.repo.Stage(r.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := m.repo.Commit(r.ctx, gitx.CommitOptions{Message: "feat: x", Author: sig}); err != nil {
		t.Fatal(err)
	}
	// The project moves on: main gets a new version of the file.
	write(t, filepath.Join(src.Dir(), ".agents", "hooks.json"), v2)
	if err := src.Stage(r.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Commit(r.ctx, gitx.CommitOptions{Message: "chore: more hooks", Author: sig}); err != nil {
		t.Fatal(err)
	}
	mirror := r.mirrorOf(m)
	if err := mirror.Fetch(r.ctx, src.Dir(), "+refs/heads/main:refs/heads/main"); err != nil {
		t.Fatal(err)
	}

	err := tb.Sync(r.ctx, req, func(context.Context) error {
		testkit.ProjectGit(t, m.dir, "-c", "user.name=Teste", "-c", "user.email=teste@example.invalid", "rebase", "main")
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got := read(t, filepath.Join(m.dir, ".agents", "hooks.json"))
	if !strings.Contains(got, `"mestre"`) || !strings.Contains(got, "novo") {
		t.Fatalf("hooks.json after the rebase: %s; want the project's new version with the Mestre's part", got)
	}
	if st := r.status(m.dir); st != "" {
		t.Fatalf("git status after the rebase =\n%s", st)
	}
	if body, _ := m.repo.ReadFile(r.ctx, "HEAD", ".agents/hooks.json"); string(body) != v2 {
		t.Fatalf("hooks.json in HEAD = %q; want the project's v2", body)
	}
}
