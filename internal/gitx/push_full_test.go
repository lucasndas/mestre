//go:build full

package gitx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/gitx"
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
		"protected branch": {"remote: error: GH006: Protected branch update failed for refs/heads/main.", gitx.ErrPushProtected},
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
			writeFile(t, work, "a.txt", "2\n")
			if err := work.Stage(ctx); err != nil {
				t.Fatal(err)
			}
			if _, err := work.Commit(ctx, gitx.CommitOptions{Message: "feat: two", Author: gitx.Signature{Name: "A", Email: "a@example.invalid"}}); err != nil {
				t.Fatal(err)
			}
			hook := "#!/bin/sh\necho '" + tc.said + "' >&2\nexit 1\n"
			if err := os.WriteFile(filepath.Join(remote.Dir(), "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
				t.Fatal(err)
			}
			auth := gitx.BasicAuth("https://github.com/", "x-access-token", "ghs_pushRefusalToken0123456789abcdef")
			head, rerr := work.ResolveRef(ctx, "main")
			if rerr != nil {
				t.Fatal(rerr)
			}
			err = work.PushWithAuth(ctx, auth, remote.Dir(), "main", string(head), false)
			if !errors.Is(err, tc.want) {
				t.Errorf("push = %v, want %v", err, tc.want)
			}
		})
	}
}

// An agent writes the files and the attributes of its worktree: neither a NUL
// byte nor `-diff` hides a line from the scan.
func TestRealAddedLinesAreReadWhateverTheFileOrItsAttributesSay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	r := initRepo(t, g)
	writeFile(t, r, "a.txt", "1\n")
	commitAll(t, r, "chore: seed")
	writeFile(t, r, "zero-byte.txt", "ok\x00\nghp_"+strings.Repeat("a1B2", 9)+"\n")
	writeFile(t, r, "hidden.js", "const t = 'ghp_"+strings.Repeat("c3D4", 9)+"'\n")
	writeFile(t, r, ".gitattributes", "hidden.js -diff\n*.bin binary\n")
	writeFile(t, r, "x.bin", "token\x00ghp_"+strings.Repeat("e5F6", 9)+"\n")
	// Windows does not accept a TAB (nor quotes, `*`, `?`, `:`) in a file name,
	// so the odd name is only written where the system allows it.
	odd := "sub/tab\tname.txt"
	if runtime.GOOS == "windows" {
		odd = "sub/plain name.txt"
	}
	writeFile(t, r, odd, "secret = 'AKIA"+strings.Repeat("A1", 8)+"'\n")
	commitAll(t, r, "feat: sneaky")
	lines, err := r.AddedLines(ctx, "HEAD~1", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, l := range lines {
		seen[l.Path] = true
	}
	for _, p := range []string{"zero-byte.txt", "hidden.js", "x.bin", odd} {
		if !seen[p] {
			t.Errorf("the lines of %q were hidden: %+v", p, lines)
		}
	}
}

func TestRealAChangeTooLargeIsNotRead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	r := initRepo(t, g)
	writeFile(t, r, "a.txt", "1\n")
	commitAll(t, r, "chore: seed")
	writeFile(t, r, "big.txt", strings.Repeat("a\n", gitx.MaxAddedLines+1))
	commitAll(t, r, "feat: big")
	if _, err := r.AddedLines(ctx, "HEAD~1", "HEAD"); !errors.Is(err, gitx.ErrDiffTooLarge) {
		t.Fatalf("a change of %d lines: %v", gitx.MaxAddedLines+1, err)
	}
}
