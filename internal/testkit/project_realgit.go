//go:build full

package testkit

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
)

// initRepo initializes the copy and makes one commit with a fixed author and
// date through internal/gitx, so the hash is the same on every run. Only the
// full and heavy suites get here.
func initRepo(t testing.TB, p *Project, _ map[string]string) {
	t.Helper()
	ctx := t.Context()
	when, err := time.Parse(time.RFC3339, ProjectCommitDate)
	if err != nil {
		t.Fatalf("ProjectCommitDate: %v", err)
	}
	repo, err := gitx.New(gitx.Options{}).Init(ctx, p.Dir, gitx.InitOptions{InitialBranch: ProjectBranch})
	if err != nil {
		t.Fatalf("init training project: %v", err)
	}
	if err := repo.Stage(ctx); err != nil {
		t.Fatalf("stage training project: %v", err)
	}
	head, err := repo.Commit(ctx, gitx.CommitOptions{
		Message: ProjectCommitMessage,
		Author:  gitx.Signature{Name: ProjectAuthorName, Email: ProjectAuthorEmail, When: when},
	})
	if err != nil {
		t.Fatalf("commit training project: %v", err)
	}
	p.Head = string(head)
}

// ProjectGit runs the real git in dir, isolated from the user and system
// configuration and with the fixed identity, and returns its output.
func ProjectGit(t testing.TB, dir string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", args...)
	cmd.Dir = dir
	cmd.Env = projectGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func projectGitEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv) // drops GIT_DIR and friends set by a git hook
		}
	}
	return append(env,
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME="+ProjectAuthorName, "GIT_AUTHOR_EMAIL="+ProjectAuthorEmail, "GIT_AUTHOR_DATE="+ProjectCommitDate,
		"GIT_COMMITTER_NAME="+ProjectAuthorName, "GIT_COMMITTER_EMAIL="+ProjectAuthorEmail, "GIT_COMMITTER_DATE="+ProjectCommitDate,
	)
}
