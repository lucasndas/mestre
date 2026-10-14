//go:build full

package classes_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/providers"
)

// plainGit runs the system git outside gitx, only to build what gitx never
// would (the agent's repository) and the control.
func plainGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(strings.ToUpper(kv), "GIT_") {
			c.Env = append(c.Env, kv)
		}
	}
	c.Env = append(c.Env, "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1", "LC_ALL=C")
	out, err := c.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := plainGit(t, dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return out
}

// N-A of the M3 audit: the commondir of a worktree's git folder is written
// by the account of the agents (and is a Codex writable root). An agent that
// points it at a repository of its own, with a promisor remote whose
// upload-pack is a command, had the engine's git run that command when the
// risk of a script or a commit was checked. The git state opens the
// worktree only by the engine's record: the change is ErrWorktreeMoved, an
// alert; the checks fail closed; and nothing runs.
func TestGitxStateOpensTheWorktreeOnlyByItsRecord(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g, err := gitx.New(gitx.Options{Suite: "full", Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seed, mirror, wt := filepath.Join(base, "seed"), filepath.Join(base, "espelho.git"), filepath.Join(base, "mesa")
	mustGit(t, base, "init", "-q", "-b", "main", seed)
	for name, body := range map[string]string{"build.sh": "#!/bin/sh\necho build\n", "ok.sh": "#!/bin/sh\necho ok\n"} {
		if err := os.WriteFile(filepath.Join(seed, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustGit(t, seed, "add", ".")
	mustGit(t, seed, "-c", "user.email=a@example.invalid", "-c", "user.name=a", "commit", "-qm", "seed")
	mustGit(t, base, "clone", "-q", "--bare", seed, mirror)
	mustGit(t, mirror, "worktree", "add", "-q", "-b", "mesa", wt)

	// The engine's record, right after the worktree was created.
	rec, err := g.Record(ctx, wt)
	if err != nil {
		t.Fatal(err)
	}
	var moved []error
	gs := classes.GitxState{Git: g, Worktree: rec, Main: "main", Moved: func(err error) { moved = append(moved, err) }}
	if c, err := gs.Changed(ctx, wt, filepath.Join(wt, "ok.sh")); c || err != nil {
		t.Fatalf("ok.sh untouched, by the record: changed %v, %v", c, err)
	}
	if b, err := gs.Branch(ctx, wt); b != "mesa" || err != nil {
		t.Fatalf("branch by the record = %q, %v", b, err)
	}
	if _, err := gs.Changed(ctx, filepath.Join(base, "seed"), filepath.Join(seed, "ok.sh")); err == nil {
		t.Fatal("a folder other than the recorded worktree was answered")
	}

	// The agent: its own repository with a promisor remote that runs a
	// command, the commondir pointed at it and HEAD at a missing object.
	evilDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	evil, mark := filepath.Join(evilDir, "evil.git"), filepath.Join(evilDir, "ran")
	mustGit(t, evilDir, "clone", "-q", "--bare", mirror, evil)
	for _, kv := range [][2]string{
		{"core.repositoryformatversion", "1"}, {"extensions.partialClone", "x"},
		{"remote.x.url", filepath.ToSlash(mirror)}, {"remote.x.promisor", "true"},
		{"remote.x.uploadpack", "touch '" + filepath.ToSlash(mark) + "'; false"},
	} {
		mustGit(t, evil, "config", kv[0], kv[1])
	}
	if err := os.WriteFile(filepath.Join(rec.GitDir, "commondir"), []byte(evil+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rec.GitDir, "HEAD"), []byte(strings.Repeat("1", 40)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Control: plain git in the worktree does run the command.
	_, _ = plainGit(t, wt, "log", "main..HEAD")
	if _, err := os.Stat(mark); err != nil {
		t.Fatalf("plain git ran nothing (%v); the test proves nothing", err)
	}
	if err := os.Remove(mark); err != nil {
		t.Fatal(err)
	}

	// The git state: every answer is the alert, and fails closed.
	if c, err := gs.Changed(ctx, wt, filepath.Join(wt, "ok.sh")); !c || !errors.Is(err, gitx.ErrWorktreeMoved) {
		t.Errorf("Changed = %v, %v; want changed with ErrWorktreeMoved", c, err)
	}
	if _, err := gs.Branch(ctx, wt); !errors.Is(err, gitx.ErrWorktreeMoved) {
		t.Errorf("Branch: %v; want ErrWorktreeMoved", err)
	}
	if _, err := gs.Committed(ctx, wt, true); !errors.Is(err, gitx.ErrWorktreeMoved) {
		t.Errorf("Committed: %v; want ErrWorktreeMoved", err)
	}
	if len(moved) != 3 {
		t.Errorf("Moved heard %d alerts, want 3: %v", len(moved), moved)
	}

	// The decisions that needed it: the script is medio, the commit denied.
	env := classes.Env{AgentID: agentID, Worktree: wt, Git: gs, Exceptions: newLedger()}
	r := rules(t, "version: 1\ncommands: {allow: [\"git commit *\", \"bash *\"], default: deny}\ngit: {commit: true, branches: [\"mesa\"]}\n")
	shell := func(cmd string) providers.Event {
		stdin, err := json.Marshal(map[string]any{
			"hook_event_name": "PreToolUse", "session_id": "s1", "prompt_id": "p1", "cwd": wt,
			"tool_name": "Bash", "tool_input": map[string]any{"command": cmd},
		})
		if err != nil {
			t.Fatal(err)
		}
		ev, err := providers.NewClaudeCode(providers.Options{}).NormalizeHook(providers.HookInput{NativeEvent: "PreToolUse", Stdin: stdin, AgentID: agentID, ReceivedAt: now})
		if err != nil {
			t.Fatal(err)
		}
		return ev
	}
	if risk := classes.ClassifyRisk(env, r, classes.RiskRequest{Tool: shell("bash ok.sh").Tool}); risk.Risk != classes.RiskMedium || !strings.Contains(risk.Rule, "script alterado na mesa") {
		t.Errorf("bash ok.sh: risk %s (%s); want medio by the changed script", risk.Risk, risk.Rule)
	}
	want(t, classes.Decide(ctx, env, r, shell("git commit -m x")), classes.Deny, classes.LayerFixed)

	if _, err := os.Stat(mark); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the engine ran the agent's upload-pack command (%v)", err)
	}
}
