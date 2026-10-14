//go:build full

package classes_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CA-PERM-04
func TestCommitWithStagedFilesOutsideFilesWriteIsDenied(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, err := gitx.New(gitx.Options{Clock: clock.NewFake(now), Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// The table's worktree, out of the repository, with its record.
	main, err := g.Init(ctx, filepath.Join(root, "main"), gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := main.Commit(ctx, gitx.CommitOptions{Message: "base", AllowEmpty: true, Author: gitx.Signature{Name: "Dev", Email: "dev@example.invalid", When: now}}); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "mesa")
	repo, err := main.AddWorktree(ctx, dir, gitx.WorktreeOptions{Branch: "epico/42-login", Create: true})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := g.Record(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	write := func(rel string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(rel+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("README.md")
	if err := repo.Stage(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Commit(ctx, gitx.CommitOptions{Message: "init", Author: gitx.Signature{Name: "Dev", Email: "dev@example.invalid", When: now}}); err != nil {
		t.Fatal(err)
	}
	write("tests/a.test.ts")
	write("src/a.ts")
	if err := repo.Stage(ctx, "tests/a.test.ts", "src/a.ts"); err != nil {
		t.Fatal(err)
	}

	env := classes.Env{AgentID: agentID, Worktree: dir, Git: classes.GitxState{Git: g, Worktree: rec}, Exceptions: newLedger()}
	r := rules(t, `version: 1
files: {write: ["tests/**"]}
commands: {allow: ["git commit *"], default: deny}
git: {commit: true, branches: ["{mesa.branch}"]}
`)
	// The command arrives as a hook payload of the provider.
	stdin, err := json.Marshal(map[string]any{
		"hook_event_name": "PreToolUse", "session_id": "s1", "prompt_id": "p1", "cwd": dir,
		"tool_name": "Bash", "tool_input": map[string]any{"command": "git commit -m x"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ev, err := providers.NewClaudeCode(providers.Options{}).NormalizeHook(providers.HookInput{NativeEvent: "PreToolUse", Stdin: stdin, AgentID: agentID, ReceivedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	d := classes.Decide(ctx, env, r, ev)
	want(t, d, classes.Deny, classes.LayerClass, "src/a.ts")
	if want := "tests/a.test.ts"; strings.Contains(d.Reason, ": "+want) || strings.Contains(d.Reason, ", "+want) {
		t.Fatalf("reason %q lists %s, which files.write allows", d.Reason, want)
	}

	// Only the allowed file staged: the commit goes through.
	if err := os.Remove(filepath.Join(dir, "src", "a.ts")); err != nil {
		t.Fatal(err)
	}
	if err := repo.Stage(ctx, "src/a.ts"); err != nil {
		t.Fatal(err)
	}
	want(t, classes.Decide(ctx, env, r, ev), classes.Allow, "")
}
