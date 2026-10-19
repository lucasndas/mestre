//go:build full

package gitx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
)

// The refusals of a push are told apart by what the remote says: GitHub answers
// with these texts, and a pre-receive hook of a bare repository says the same.
func TestRealPushTellsTheRefusalsOfTheRemoteApart(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the hook of the remote is a shell script")
	}
	ctx := context.Background()
	g := newReal(gitx.Options{})
	for name, tc := range map[string]struct {
		said string
		want error
	}{
		"protected branch": {"remote: error: GH006: Protected branch update failed for refs/heads/mesa.", gitx.ErrPushProtected},
		"ruleset":          {"remote: error: GH013: Repository rule violations found for refs/heads/mesa.", gitx.ErrPushProtected},
		"workflows":        {"refusing to allow a GitHub App to create or update workflow `.github/workflows/ci.yml` without `workflows` permission", gitx.ErrPushWorkflow},
		"signatures":       {"remote: error: GH013: Commits must have verified signatures.", gitx.ErrPushSignature},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			work := initRepo(t, g)
			writeFile(t, work, "a.txt", "1\n")
			if err := work.Stage(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := work.Commit(ctx, gitx.CommitOptions{Message: "chore: seed", Author: gitx.Signature{Name: "A", Email: "a@example.invalid"}}); err != nil {
				t.Fatal(err)
			}
			remote, err := g.Clone(ctx, work.Dir(), filepath.Join(root, "remoto.git"), gitx.CloneOptions{Bare: true})
			if err != nil {
				t.Fatal(err)
			}
			hook := "#!/bin/sh\necho '" + tc.said + "' >&2\nexit 1\n"
			if err := os.WriteFile(filepath.Join(remote.Dir(), "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
				t.Fatal(err)
			}
			auth := gitx.BasicAuth("https://github.com/", "x-access-token", "ghs_pushRefusalToken0123456789abcdef")
			err = work.PushWithAuth(ctx, auth, remote.Dir(), "refs/heads/main:refs/heads/mesa", false)
			if !errors.Is(err, tc.want) {
				t.Errorf("push = %v, want %v", err, tc.want)
			}
		})
	}
}
