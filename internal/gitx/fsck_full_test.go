//go:build full

package gitx_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
)

// A bundle comes from another machine, where agents write: the objects in it
// are checked as they are received, so a malformed one is refused instead of
// entering the repository of the engine.
func TestFetchBundleChecksTheObjectsItReceives(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	root := t.TempDir()
	src, err := g.Init(ctx, filepath.Join(root, "src"), gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, src, "a.txt", "1\n")
	head := commitAll(t, src, "feat: one")

	run := func(stdin string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = src.Dir()
		cmd.Stdin = strings.NewReader(stdin)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	tree := run("", "rev-parse", "HEAD^{tree}")
	// An author without an e-mail is something fsck refuses.
	bad := run("tree "+tree+"\nparent "+string(head)+"\nauthor broken\ncommitter broken\n\nbad\n", "hash-object", "-t", "commit", "-w", "--literally", "--stdin")
	run("", "update-ref", "refs/heads/bad", bad)
	file := filepath.Join(root, "bad.bundle")
	run("", "bundle", "create", "-q", file, "refs/heads/bad")

	dst, err := g.Init(ctx, filepath.Join(root, "dst"), gitx.InitOptions{Bare: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := dst.FetchBundle(ctx, file, "+refs/heads/bad:refs/heads/bad"); err == nil {
		t.Fatal("a bundle with a malformed object was taken")
	}
	if _, err := dst.ResolveRef(ctx, "refs/heads/bad"); err == nil {
		t.Error("the ref of the malformed commit was written")
	}
}
