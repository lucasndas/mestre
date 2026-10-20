//go:build full

package classes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// commitRig is a table worktree with real git, a counter of the git
// processes the engine starts and the hook event of a git commit.
type commitRig struct {
	ctx   context.Context
	dir   string
	repo  gitx.Repo
	procs atomic.Int64
	mu    sync.Mutex
	last  []string
	calls []string // the arguments of every git process, to name them in a failure
	env   classes.Env
	rules *classes.Rules
	ev    providers.Event
}

func newCommitRig(t testing.TB, yaml string) *commitRig {
	t.Helper()
	rg := &commitRig{ctx: context.Background()}
	g, err := gitx.New(gitx.Options{Clock: clock.NewFake(now), Masker: logging.NewMasker(), Trace: func(args []string) {
		rg.procs.Add(1)
		rg.mu.Lock()
		rg.calls = append(rg.calls, strings.Join(args, " "))
		rg.mu.Unlock()
	}})
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sig := gitx.Signature{Name: "Dev", Email: "dev@example.invalid", When: now}
	main, err := g.Init(rg.ctx, filepath.Join(root, "main"), gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := main.Commit(rg.ctx, gitx.CommitOptions{Message: "base", AllowEmpty: true, Author: sig}); err != nil {
		t.Fatal(err)
	}
	rg.dir = filepath.Join(root, "mesa")
	if rg.repo, err = main.AddWorktree(rg.ctx, rg.dir, gitx.WorktreeOptions{Branch: "epico/42-login", Create: true}); err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record(rg.ctx, rg.dir)
	if err != nil {
		t.Fatal(err)
	}
	rg.write(t, "README.md")
	if err := rg.repo.Stage(rg.ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := rg.repo.Commit(rg.ctx, gitx.CommitOptions{Message: "init", Author: sig}); err != nil {
		t.Fatal(err)
	}
	rg.env = classes.Env{AgentID: agentID, Worktree: rg.dir, Git: classes.GitxState{Git: g, Worktree: rec}, Exceptions: newLedger()}
	rg.rules = rules(t, yaml)
	stdin, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "session_id": "s1", "prompt_id": "p1", "cwd": rg.dir,
		"tool_name": "Bash", "tool_input": map[string]any{"command": "git commit -m x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if rg.ev, err = providers.NewClaudeCode(providers.Options{}).NormalizeHook(providers.HookInput{NativeEvent: "PreToolUse", Stdin: stdin, AgentID: agentID, ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	return rg
}

func (rg *commitRig) write(t testing.TB, rel string) {
	t.Helper()
	p := filepath.Join(rg.dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(rel+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// decide runs one decision and returns it with the git processes it started.
func (rg *commitRig) decide() (classes.Decision, int64) {
	before := rg.procs.Load()
	d := classes.Decide(rg.ctx, rg.env, rg.rules, rg.ev)
	n := rg.procs.Load() - before
	rg.mu.Lock()
	rg.last = append([]string(nil), rg.calls[max(0, len(rg.calls)-int(n)):]...)
	rg.mu.Unlock()
	return d, n
}

const commitRules = `version: 1
files: {write: ["tests/**"]}
commands: {allow: ["git commit *"], default: deny}
git: {commit: true, branches: ["{mesa.branch}"]}
`

// maxGitProcsPerCommitDecision is the budget of a decision on a git commit
// (docs/provedores/despachante-de-hooks.md: the pre_tool answers in 800 ms,
// and on a busy Windows every process counts).
const maxGitProcsPerCommitDecision = 2

// The decision on a git commit starts at most maxGitProcsPerCommitDecision git
// processes, and reads the real index every time: a file staged after a
// decision changes the next one, so nothing the status says is kept.
func TestCommitDecisionStartsFewGitProcessesAndReadsTheRealIndex(t *testing.T) {
	t.Parallel()
	for _, goos := range []string{"", "windows", "linux"} {
		t.Run("os="+goos, func(t *testing.T) {
			t.Parallel()
			rg := newCommitRig(t, commitRules)
			rg.env.OS = goos
			checkCommitDecision(t, rg)
		})
	}
}

func checkCommitDecision(t *testing.T, rg *commitRig) {
	t.Helper()
	rg.write(t, "tests/a.test.ts")
	if err := rg.repo.Stage(rg.ctx, "tests/a.test.ts"); err != nil {
		t.Fatal(err)
	}

	d, n := rg.decide()
	want(t, d, classes.Allow, "")
	if n > maxGitProcsPerCommitDecision+1 {
		t.Fatalf("the first decision (worktree not checked yet) started %d git processes, want at most %d: %q", n, maxGitProcsPerCommitDecision+1, rg.last)
	}
	for range 3 {
		d, n = rg.decide()
		want(t, d, classes.Allow, "")
		if n > maxGitProcsPerCommitDecision {
			t.Fatalf("a decision on git commit started %d git processes, want at most %d: %q", n, maxGitProcsPerCommitDecision, rg.last)
		}
	}

	// A file outside files.write staged after those decisions: denied at once.
	rg.write(t, "src/a.ts")
	if err := rg.repo.Stage(rg.ctx, "src/a.ts"); err != nil {
		t.Fatal(err)
	}
	d, n = rg.decide()
	want(t, d, classes.Deny, classes.LayerClass, "src/a.ts")
	if n > maxGitProcsPerCommitDecision {
		t.Fatalf("the denying decision started %d git processes, want at most %d: %q", n, maxGitProcsPerCommitDecision, rg.last)
	}

	// Taken out of the index again: allowed again.
	if err := os.Remove(filepath.Join(rg.dir, "src", "a.ts")); err != nil {
		t.Fatal(err)
	}
	if err := rg.repo.Stage(rg.ctx, "src/a.ts"); err != nil {
		t.Fatal(err)
	}
	d, _ = rg.decide()
	want(t, d, classes.Allow, "")
}

// BenchmarkDecideGitCommit is the cost of the decision on a git commit with
// real git: go test -tags full -run '^$' -bench DecideGitCommit ./internal/classes
func BenchmarkDecideGitCommit(b *testing.B) {
	rg := newCommitRig(b, commitRules)
	rg.write(b, "tests/a.test.ts")
	if err := rg.repo.Stage(rg.ctx, "tests/a.test.ts"); err != nil {
		b.Fatal(err)
	}
	if d, _ := rg.decide(); d.Verdict != classes.Allow {
		b.Fatalf("setup: %v %s", d.Verdict, d.Reason)
	}
	before := rg.procs.Load()
	b.ResetTimer()
	for range b.N {
		rg.decide()
	}
	b.ReportMetric(float64(rg.procs.Load()-before)/float64(b.N), "git-procs/op")
}
