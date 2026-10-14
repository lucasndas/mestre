//go:build full

package gitx_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
)

// N-A of the M3 audit, below the record: a repository whose configuration
// has a promisor remote would have git fetch a missing object from it on
// its own, running the remote's upload-pack as the engine. gitx sets
// GIT_NO_LAZY_FETCH, and on a git that does not know it refuses the
// repository (ErrUnsafeRepo). Nothing runs either way, even through Open.
func TestRealPromisorRemoteRunsNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	other := initRepo(t, g)
	writeFile(t, other, "o.txt", "o\n")
	commitAll(t, other, "chore: o")
	r := initRepo(t, g)
	writeFile(t, r, "a.txt", "a\n")
	commitAll(t, r, "chore: a")

	mark := filepath.Join(t.TempDir(), "ran")
	for _, kv := range [][2]string{
		{"core.repositoryformatversion", "1"}, {"extensions.partialClone", "x"},
		{"remote.x.url", filepath.ToSlash(other.Dir())}, {"remote.x.promisor", "true"},
		{"remote.x.uploadpack", "touch '" + filepath.ToSlash(mark) + "'; false"},
	} {
		if out, err := rawGit(t, r.Dir(), "", "config", kv[0], kv[1]); err != nil {
			t.Fatal(out, err)
		}
	}
	ghost := strings.Repeat("1", 40)
	if err := os.WriteFile(filepath.Join(r.Dir(), ".git", "refs", "heads", "ghost"), []byte(ghost+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Control: plain git runs the command.
	_, _ = rawGit(t, r.Dir(), "", "log", "ghost")
	if _, err := os.Stat(mark); err != nil {
		t.Fatalf("plain git ran nothing (%v); the test proves nothing", err)
	}
	if err := os.Remove(mark); err != nil {
		t.Fatal(err)
	}

	v, err := g.Version(ctx)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := g.Open(ctx, r.Dir())
	if err != nil {
		t.Fatal(err)
	}
	_, logErr := opened.Log(ctx, "ghost")
	_, readErr := opened.ReadFile(ctx, "ghost", "a.txt")
	_, statusErr := opened.Status(ctx)
	for name, err := range map[string]error{"Log": logErr, "ReadFile": readErr} {
		if err == nil {
			t.Errorf("%s of a missing object succeeded", name)
		}
	}
	if v.Less(gitx.NoLazyFetchVersion) {
		for name, err := range map[string]error{"Log": logErr, "ReadFile": readErr, "Status": statusErr} {
			if !errors.Is(err, gitx.ErrUnsafeRepo) {
				t.Errorf("git %s: %s = %v; want ErrUnsafeRepo", v, name, err)
			}
		}
	}
	if _, err := os.Stat(mark); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("gitx ran the upload-pack of a promisor remote (%v)", err)
	}

	// A promisor turned off is no promisor.
	if out, err := rawGit(t, r.Dir(), "", "config", "--unset", "extensions.partialClone"); err != nil {
		t.Fatal(out, err)
	}
	if out, err := rawGit(t, r.Dir(), "", "config", "remote.x.promisor", "false"); err != nil {
		t.Fatal(out, err)
	}
	if _, err := opened.Status(ctx); err != nil {
		t.Errorf("Status with remote.x.promisor=false: %v", err)
	}
}

// The folder of a mirror is writable by mestre-agente; git looks for a .git
// entry inside a folder before taking it as a bare repository. Open takes a
// bare repository as one (GIT_DIR), so a .git file planted in the mirror
// does not hand the engine another repository.
func TestRealBareRepoIgnoresAPlantedGitFile(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	g := newReal(gitx.Options{})
	origin := initRepo(t, g)
	writeFile(t, origin, "a.txt", "a\n")
	want := commitAll(t, origin, "chore: a")
	mirror, err := g.Clone(ctx, origin.Dir(), filepath.Join(t.TempDir(), "espelho.git"), gitx.CloneOptions{Bare: true})
	if err != nil {
		t.Fatal(err)
	}
	other := initRepo(t, g)
	writeFile(t, other, "x.txt", "x\n")
	commitAll(t, other, "chore: x")
	planted := "gitdir: " + filepath.ToSlash(filepath.Join(other.Dir(), ".git")) + "\n"
	if err := os.WriteFile(filepath.Join(mirror.Dir(), ".git"), []byte(planted), 0o644); err != nil {
		t.Fatal(err)
	}
	// Control: plain git in the folder follows the .git file.
	if out, err := rawGit(t, mirror.Dir(), "", "rev-parse", "main"); err != nil || gitx.Hash(out) == want {
		t.Fatalf("plain git read %s, %v: the test proves nothing", out, err)
	}

	opened, err := g.Open(ctx, mirror.Dir())
	if err != nil {
		t.Fatal(err)
	}
	if got, err := opened.ResolveRef(ctx, "main"); err != nil || got != want {
		t.Fatalf("main of the mirror = %s, %v; want %s", got, err, want)
	}
	if _, err := opened.Status(ctx); !errors.Is(err, gitx.ErrBareRepo) {
		t.Errorf("Status of the mirror: %v; want ErrBareRepo (opened with a working tree)", err)
	}
}
