//go:build full

package replication_test

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/replication"
	"github.com/lucasndas/mestre/internal/store"
)

var mirrorAuthor = gitx.Signature{Name: "Alice Dev", Email: "alice@example.invalid", When: time.Unix(1767225600, 0)}

// mirrorSrc is the set of mirrors of the Citadel in the test: repositories in
// folders, with a record of the bundles asked for.
type mirrorSrc struct {
	g    gitx.Git
	dirs map[string]string

	mu      sync.Mutex
	bundles [][]string
}

func (s *mirrorSrc) IDs(context.Context) ([]string, error) {
	ids := make([]string, 0, len(s.dirs))
	for id := range s.dirs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

func (s *mirrorSrc) Open(ctx context.Context, id string) (replication.MirrorRepo, error) {
	repo, err := s.g.Open(ctx, s.dirs[id])
	if err != nil {
		return nil, err
	}
	return recordingRepo{Repo: repo, s: s}, nil
}

func (s *mirrorSrc) built() [][]string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.bundles)
}

type recordingRepo struct {
	gitx.Repo
	s *mirrorSrc
}

func (r recordingRepo) CreateBundle(ctx context.Context, file string, revs ...string) error {
	r.s.mu.Lock()
	r.s.bundles = append(r.s.bundles, slices.Clone(revs))
	r.s.mu.Unlock()
	return r.Repo.CreateBundle(ctx, file, revs...)
}

// mirrorScenario is CA-REPL-08 for the mirrors, as far as milestone 2 goes:
// the mirror of a workspace follows the refs of the Citadel by bundle, the
// first time with the whole of it, then with what moved, and a Tower that
// already has it is not sent it again. `write` puts a file in the working tree
// of a repository of g.
func mirrorScenario(t *testing.T, g gitx.Git, write func(testing.TB, gitx.Repo, string, string)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		root := t.TempDir()
		ws, err := g.Init(ctx, filepath.Join(root, "ws1"), gitx.InitOptions{InitialBranch: "main"})
		if err != nil {
			t.Fatal(err)
		}
		commit := func(rel, body, msg string) gitx.Hash {
			write(t, ws, rel, body)
			if err := ws.Stage(ctx); err != nil {
				t.Fatal(err)
			}
			h, err := ws.Commit(ctx, gitx.CommitOptions{Message: msg, Author: mirrorAuthor})
			if err != nil {
				t.Fatal(err)
			}
			return h
		}
		commit("a.txt", "1\n", "chore: one")
		c2 := commit("a.txt", "2\n", "feat: two")
		src := &mirrorSrc{g: g, dirs: map[string]string{"ws1": filepath.Join(root, "ws1")}}

		// A row of machines with an instance cites the mirror of that name.
		cites := func(c store.Change) []string {
			if c.Table == "machines" && c.Op == store.OpInsert && c.New[2] != "" {
				return []string{c.New[2].(string)}
			}
			return nil
		}
		r := newRig(t, rigOpts{mirrors: src, git: g, citesMirrors: cites})
		tw := r.addTower("t1", true)

		// main on the Tower, or "" while the Tower has no mirror.
		tip := func() gitx.Hash {
			repo, err := g.Open(ctx, filepath.Join(tw.mirrors, "ws1", "espelho.git"))
			if err != nil {
				return ""
			}
			list, err := repo.ListRefs(ctx, replication.ReplicaPrefix)
			if err != nil {
				return ""
			}
			for _, ref := range list {
				if ref.Name == replication.ReplicaRef("refs/heads/main") {
					return ref.Hash
				}
			}
			return ""
		}
		atTip := func(want gitx.Hash) func() bool { return func() bool { return tip() == want } }
		eventually(t, "the mirror on the Tower", atTip(c2))
		if b := src.built(); len(b) != 1 || len(b[0]) != 1 || strings.Contains(b[0][0], "..") {
			t.Fatalf("bundles built = %v, want one with the whole mirror", b)
		}

		// A ref moves: only what is new goes.
		c3 := commit("a.txt", "3\n", "feat: three")
		r.primary.MirrorChanged("ws1")
		eventually(t, "the move", atTip(c3))
		b := src.built()
		if len(b) != 2 || len(b[1]) != 1 || b[1][0] != string(c2)+"..refs/heads/main" {
			t.Fatalf("bundles built = %v, want the second to be %s..refs/heads/main", b, c2)
		}

		// Nothing moved: nothing is sent.
		r.primary.MirrorChanged("ws1")
		settle()
		if got := len(src.built()); got != 2 {
			t.Errorf("a notification with no change built a bundle (%d)", got)
		}

		// A Tower that reconnects and already has the mirror gets no bundle on the wire.
		n := len(tw.log())
		tw.stop()
		tw.start(true)
		eventually(t, "the reconnection", func() bool { return tw.lsn() == r.lsn() && statusOf(t, r, "t1").Acked == r.lsn() })
		for _, e := range tw.log()[n:] {
			if e == "bundle" {
				t.Errorf("the mirror was sent again to a Tower that had it: %v", tw.log())
			}
		}

		// A Tower that missed a move while away gets the mirror brought up to date.
		tw.stop()
		c4 := commit("a.txt", "4\n", "feat: four")
		tw.start(true)
		eventually(t, "the mirror after coming back", atTip(c4))

		// A record that cites the mirror finds it up to date on the Tower, with no
		// notification: the bundle goes before the record.
		c5 := commit("a.txt", "5\n", "feat: five")
		r.exec(`INSERT INTO machines (machine_id, name, instance, priority, excluded, state, names, joined_at) VALUES ('m1', 'n', 'ws1', 0, 0, 'active', '{}', 0)`)
		lsn := r.lsn()
		eventually(t, "the record that cites the mirror", func() bool { return tw.lsn() >= lsn })
		if tip() != c5 {
			t.Fatalf("after the record that cites it, main = %s, want %s", tip(), c5)
		}
		ev := tw.log()
		bi, ai := lastIndexOf(ev, "bundle"), appliedAt(ev, lsn)
		if bi < 0 || ai < 0 || bi > ai {
			t.Fatalf("the bundle was at event %d and the record that cites the mirror was applied at %d; events = %v", bi, ai, ev)
		}

		// A branch that the Citadel deletes is deleted from the mirror too.
		hasRef := func(name string) bool {
			repo, err := g.Open(ctx, filepath.Join(tw.mirrors, "ws1", "espelho.git"))
			if err != nil {
				return false
			}
			list, err := repo.ListRefs(ctx, replication.ReplicaPrefix)
			if err != nil {
				return false
			}
			for _, ref := range list {
				if ref.Name == replication.ReplicaRef(name) {
					return true
				}
			}
			return false
		}
		if err := ws.CreateBranch(ctx, "feature", string(c5)); err != nil {
			t.Fatal(err)
		}
		r.primary.MirrorChanged("ws1")
		eventually(t, "the new branch on the mirror", func() bool { return hasRef("refs/heads/feature") })
		if err := ws.DeleteBranch(ctx, "feature"); err != nil {
			t.Fatal(err)
		}
		r.primary.MirrorChanged("ws1")
		eventually(t, "the branch to leave the mirror", func() bool { return !hasRef("refs/heads/feature") })
		if !hasRef("refs/heads/main") {
			t.Error("the prune took main with it")
		}
	})
}

func lastIndexOf(s []string, v string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == v {
			return i
		}
	}
	return -1
}
