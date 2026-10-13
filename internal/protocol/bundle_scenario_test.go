package protocol_test

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/pkg/clock"
)

var bundleAuthor = gitx.Signature{Name: "Alice Dev", Email: "alice@example.invalid"}

// bundleResult is what the scenario of the bundle without a prerequisite
// observed.
type bundleResult struct {
	// finalized is the code of the error of each time the Citadel verified a
	// bundle ("" for success), in order.
	finalized []string
	// incrementalOpened and fullOpened count the bundles the Tower built.
	incrementalOpened, fullOpened int
	// tip is the commit the Citadel ended up with, and want the one the Tower
	// has; history is how many commits the Citadel can reach.
	tip, want gitx.Hash
	history   int
}

// bundleWithoutPrerequisite is the scenario of the criterion: the Tower has
// three commits and sends the incremental bundle "since the second"; the
// Citadel has none of them, so the verification says the prerequisite is
// missing and the Tower sends the whole branch. `write` puts a file in the
// working tree of a repository of g.
func bundleWithoutPrerequisite(t *testing.T, w *world, g gitx.Git, write func(testing.TB, gitx.Repo, string, string)) bundleResult {
	t.Helper()
	ctx, cancel := clock.WithTimeout(t.Context(), w.clk, 30*time.Second)
	defer cancel()
	root := t.TempDir()
	commit := func(r gitx.Repo, rel, body, msg string) gitx.Hash {
		write(t, r, rel, body)
		if err := r.Stage(ctx); err != nil {
			t.Fatal(err)
		}
		h, err := r.Commit(ctx, gitx.CommitOptions{Message: msg, Author: bundleAuthor})
		if err != nil {
			t.Fatal(err)
		}
		return h
	}
	tower, err := g.Init(ctx, filepath.Join(root, "tower"), gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	commit(tower, "a.txt", "1\n", "chore: one")
	c2 := commit(tower, "a.txt", "2\n", "feat: two")
	c3 := commit(tower, "a.txt", "3\n", "feat: three")
	citadel, err := g.Init(ctx, filepath.Join(root, "citadel.git"), gitx.InitOptions{Bare: true, InitialBranch: "main"})
	if err != nil {
		t.Fatal(err)
	}

	res := bundleResult{want: c3}
	var mu sync.Mutex
	verify := protocol.BundleReceiver{
		Repo: func(context.Context, protocol.Object) (protocol.BundleRepo, error) { return citadel, nil },
		Apply: func(ctx context.Context, repo protocol.BundleRepo, _ protocol.Object, file string) error {
			return repo.(gitx.Repo).FetchBundle(ctx, file, "refs/heads/main:refs/mestre/torres/t1/main")
		},
	}
	w.cfgC.Xfer = &protocol.XferConfig{Dir: filepath.Join(root, "xfer"), Finalizers: map[string]protocol.Finalizer{
		protocol.KindGitBundle: protocol.FinalizerFunc(func(ctx context.Context, b protocol.XferBegin, file string) error {
			err := verify.Finalize(ctx, b, file)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				res.finalized = append(res.finalized, "")
			} else {
				res.finalized = append(res.finalized, err.(*protocol.Error).Code)
			}
			return err
		}),
	}}
	w.start()
	defer w.stop()

	open := func(count *int, revs ...string) protocol.OpenSource {
		return func(ctx context.Context) (protocol.Source, func(), error) {
			*count++
			file := filepath.Join(root, "out.bundle")
			if err := tower.CreateBundle(ctx, file, revs...); err != nil {
				return protocol.Source{}, nil, err
			}
			f, err := os.Open(file)
			if err != nil {
				return protocol.Source{}, nil, err
			}
			st, err := f.Stat()
			if err != nil {
				_ = f.Close()
				return protocol.Source{}, nil, err
			}
			return protocol.Source{ReaderAt: f, Size: st.Size()}, func() { _ = f.Close() }, nil
		}
	}
	meta := protocol.Object{"table": []byte(`"t1"`), "branch": []byte(`"main"`)}
	// the link is connected once the Tower has a session; Transfer waits for it
	err = w.client.Link().SendBundle(ctx, meta, protocol.BundleSource{
		Incremental: open(&res.incrementalOpened, string(c2)+"..main"),
		Full:        open(&res.fullOpened, "main"),
	})
	if err != nil {
		t.Fatalf("SendBundle: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	res.tip, err = citadel.ResolveRef(ctx, "refs/mestre/torres/t1/main")
	if err != nil {
		t.Fatalf("the Citadel has no ref for the branch: %v", err)
	}
	log, err := citadel.Log(ctx, "refs/mestre/torres/t1/main")
	if err != nil {
		t.Fatal(err)
	}
	res.history = len(log)
	return res
}

func checkBundleResult(t *testing.T, r bundleResult) {
	t.Helper()
	if len(r.finalized) != 2 || r.finalized[0] != protocol.CodeMissingPrerequisite || r.finalized[1] != "" {
		t.Fatalf("verifications = %q, want missing_prerequisite and then success", r.finalized)
	}
	if r.incrementalOpened != 1 || r.fullOpened != 1 {
		t.Fatalf("bundles built: %d incremental, %d full; want one of each", r.incrementalOpened, r.fullOpened)
	}
	if r.tip != r.want || r.history != 3 {
		t.Fatalf("the Citadel has %s with %d commits, want %s with 3 (the whole branch)", r.tip, r.history, r.want)
	}
}
