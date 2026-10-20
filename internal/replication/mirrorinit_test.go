package replication

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
)

// initGit is a gitx.Git whose Init behaves like "git init" in one folder: it
// fails when another Init of it is running or when the repository is already
// there. The first Init waits (without sleeping) until the second caller has
// looked at the folder, so the two meet in the window of the race.
type initGit struct {
	gitx.Git
	opens   atomic.Int32
	active  atomic.Int32
	inits   atomic.Int32
	seen    chan struct{} // closed when three Opens were made
	once    sync.Once
	madeDir atomic.Bool
}

func (g *initGit) release() { g.once.Do(func() { close(g.seen) }) }

func (g *initGit) Open(_ context.Context, dir string) (gitx.Repo, error) {
	if g.opens.Add(1) == 3 {
		g.release()
	}
	if g.madeDir.Load() {
		return nil, nil
	}
	return nil, fmt.Errorf("open %s: %w", dir, gitx.ErrNotARepo)
}

func (g *initGit) Init(_ context.Context, dir string, _ gitx.InitOptions) (gitx.Repo, error) {
	g.inits.Add(1)
	if g.active.Add(1) > 1 {
		return nil, fmt.Errorf("init %s: another init is running", dir)
	}
	defer g.active.Add(-1)
	<-g.seen
	if g.madeDir.Swap(true) {
		return nil, fmt.Errorf("init %s: File exists", dir)
	}
	return nil, nil
}

// Two Opens of a mirror the Tower does not have yet both end well: the replica
// is made once and the other caller finds it.
func TestTwoOpensOfAMissingReplicaMakeItOnce(t *testing.T) {
	t.Parallel()
	g := &initGit{seen: make(chan struct{})}
	store := MirrorStore{Root: t.TempDir(), Git: g}
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = store.Open(t.Context(), "meu-app")
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("open %d: %v", i, err)
		}
	}
	if n := g.inits.Load(); n != 1 {
		t.Errorf("%d inits, want 1", n)
	}
}

// A folder that is not a repository and is not empty is not taken as a replica.
func TestAReplicaFolderOfAnotherKindIsRefused(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	dir := filepath.Join(root, "meu-app", "espelho.git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	g := &initGit{seen: make(chan struct{})}
	if _, err := (MirrorStore{Root: root, Git: g}).Open(t.Context(), "meu-app"); err == nil {
		t.Fatal("a folder with other things in it was taken as a replica")
	}
	if g.inits.Load() != 0 {
		t.Error("init ran in a foreign folder")
	}
	if err := os.Remove(filepath.Join(dir, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	g.release()
	if _, err := (MirrorStore{Root: root, Git: g}).Open(t.Context(), "meu-app"); err != nil {
		t.Errorf("an empty folder was refused: %v", err)
	}
}
