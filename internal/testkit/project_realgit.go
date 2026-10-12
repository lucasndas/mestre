//go:build full

package testkit

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// initRepo runs git init and one commit with a fixed author and date, so the
// hash is the same on every run. Until internal/gitx exists (M1) the test
// kit calls the git binary directly; only the full and heavy suites get here.
func initRepo(t testing.TB, p *Project, _ map[string]string) {
	t.Helper()
	ProjectGit(t, p.Dir, "init", "-q", "-b", ProjectBranch)
	ProjectGit(t, p.Dir, "add", "-A")
	ProjectGit(t, p.Dir, "commit", "-q", "-m", ProjectCommitMessage)
	p.Head = strings.TrimSpace(ProjectGit(t, p.Dir, "rev-parse", "HEAD"))
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
