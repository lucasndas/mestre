//go:build full

package sessionzero_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/mestre-ia/mestre/internal/orchestration/presets"
	"github.com/mestre-ia/mestre/internal/sessionzero"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/testkit/fakeagent"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// gitCommands runs the `git …` commands of the steps of a fake agent with the
// real git, in the environment of the character (the gitconfig of its plan),
// the way the shell tool of the agent would.
type gitCommands struct{}

func (gitCommands) RunCommand(ctx context.Context, command, dir string, env []string) (string, int, error) {
	args := strings.Fields(command)
	if len(args) == 0 || args[0] != "git" {
		return "", 1, errors.New("only git commands: " + command)
	}
	cmd := exec.CommandContext(ctx, "git", args[1:]...)
	cmd.Dir = dir
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GIT_") || strings.HasPrefix(kv, "GIT_CONFIG_GLOBAL=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			return "", 1, err
		}
		code = ee.ExitCode()
	}
	return string(out), code, nil
}

// gitIn runs the real git in a folder with no gitconfig of the machine.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// CA-WIN-01
func TestTheGitconfigTrustsTheMainCloneAndTheWorktreeOfTheCharacterAndItsFakeAgentCommits(t *testing.T) { //nolint:paralleltest // the fake agent runs in a synctest bubble
	r := newCharRig(t, nil)
	r.login()
	main := filepath.Join(t.TempDir(), "meu-app")
	gitIn(t, "", "init", "-q", "-b", "main", main)
	gitIn(t, main, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "initial", "--no-gpg-sign")
	wt := filepath.Join(t.TempDir(), "worktrees", "3-login")
	gitIn(t, main, "worktree", "add", "-q", "-b", "mestre/3-login", wt)
	q := r.req("dev", presets.AgentClaudeCode, sessionzero.RoleCharacter)
	q.Dir, q.Vars.Workspace = wt, main
	trusted := func(plan sessionzero.CharacterPlan) []string {
		t.Helper()
		return strings.Fields(gitIn(t, "", "config", "--file", plan.GitConfig, "--get-all", "safe.directory"))
	}
	plan := r.prepare(q)
	want := []string{filepath.ToSlash(main), filepath.ToSlash(wt)}
	if got := trusted(plan); !slices.Equal(got, want) {
		t.Fatalf("safe.directory = %q; want the main clone and the worktree, %q", got, want)
	}
	// Starting again (the engine restarted, the character resumed) writes the same.
	r.rebuildChar()
	if got := trusted(r.prepare(q)); !slices.Equal(got, want) {
		t.Errorf("safe.directory after a second start = %q; want %q, with no entry repeated", got, want)
	}
	// An update of the author keeps the trusted folders.
	next := sessionzero.Identity{Name: "other[bot]", Email: "9+other[bot]@users.noreply.github.com"}
	if _, err := r.ch.UpdateIdentity(t.Context(), "dev", next); err != nil {
		t.Fatal(err)
	}
	if got := trusted(plan); !slices.Equal(got, want) {
		t.Errorf("safe.directory after the identity changed = %q; want %q", got, want)
	}
	if c := read(t, plan.GitConfig); strings.Contains(c, "*") {
		t.Errorf("the gitconfig has a wildcard:\n%s", c)
	}

	script := filepath.Join(t.TempDir(), "script.yaml")
	write(t, script, "prompts:\n  - match: '^commit'\n    steps:\n      - edit: {path: 'src/a.txt', content: 'a'}\n      - run: 'git add -A'\n      - run: 'git commit -q -m feature --no-gpg-sign'\n")
	synctest.Test(t, func(t *testing.T) {
		launcher := &fakeagent.MemLauncher{Clock: clock.Real{}, Hooks: &recordHooks{}, Commands: gitCommands{}, Reporter: t}
		env := append(slices.Clone(plan.Launch.Env), "FAKEAGENT_SCRIPT="+script)
		proc, err := launcher.Start(t.Context(), terminal.Spec{Path: "claude", Args: plan.Launch.Args, Env: env, Dir: plan.Launch.Dir})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = proc.Kill() })
		ad, _ := r.opts.Providers.For(plan.Provider)
		c := &fakeChar{mp: proc.(*fakeagent.MemProcess), hooks: &recordHooks{}, ad: ad}
		synctest.Wait()
		// The worktree is new to the fake agent: it asks to trust the folder.
		if _, err := c.mp.PTY().Write([]byte("\r")); err != nil {
			t.Fatal(err)
		}
		synctest.Wait()
		c.type_(t, "commit the feature")

	})
	if got := gitIn(t, wt, "log", "-1", "--format=%s|%an"); got != "feature|other[bot]" {
		t.Errorf("the last commit of the worktree = %q; want the one of the fake agent, by the identity of its gitconfig", got)
	}
}
