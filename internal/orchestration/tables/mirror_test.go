package tables_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/replication"
)

// Part of CA-REPL-08 (the mirror of the workspace, milestone 4): the mirror of a
// workspace of the Citadel reaches a Tower that has the worktree of a table
// checked out in its replica. The branch of the main clone arrives in the
// namespace of the replica; the branches of the tables do not travel by the
// mirror (not even an untrusted one); the fetch does not meet the branch that
// is checked out in the Tower; and the commits the Tower has not sent are not
// pruned.
func TestTheMirrorOfAWorkspaceReachesATowerWithATableCheckedOut(t *testing.T) {
	t.Parallel()
	mirrorScenario(t, nil)
}

func mirrorScenario(t *testing.T, be backend) {
	t.Helper()
	w, tw, tbl := towerWorld(t, be)
	slug := w.slug()
	mirrors := replication.NewMirrors(w.wss.Mirrors(), t.TempDir())
	fin := replication.MirrorFinalizer(tw.mirrors)
	var have map[string]gitx.Hash
	send := func() protocol.Object {
		t.Helper()
		tr, err := mirrors.Bundle(w.ctx, slug, have)
		if err != nil {
			t.Fatal(err)
		}
		if tr.Tips == nil {
			return nil
		}
		if err := deliver(w.ctx, t, fin, tr.Meta, tr.Source, nil); err != nil {
			t.Fatalf("the Tower did not take the mirror: %v", err)
		}
		have = tr.Tips
		return tr.Meta
	}
	replicaRef := func(ref string) gitx.Hash {
		h, _ := tw.replica().ResolveRef(w.ctx, replication.ReplicaRef(ref))
		return h
	}

	// The main clone is on main, and the table has its branch on the Citadel.
	main, err := w.mirror().ResolveRef(w.ctx, "refs/heads/main")
	if err != nil {
		t.Fatal(err)
	}
	meta := send()
	if meta == nil {
		t.Fatal("the mirror has refs/heads/main and nothing was sent")
	}
	if strings.Contains(string(meta["refs"]), tbl.Branch) {
		t.Errorf("the branch of the table travels in the mirror: %s", meta["refs"])
	}
	if got := replicaRef("refs/heads/main"); got != main {
		t.Errorf("main on the Tower = %s; want %s", got.Short(), main.Short())
	}
	if got := replicaRef("refs/heads/" + tbl.Branch); got != "" {
		t.Errorf("the branch of the table is in the namespace of the replica: %s", got.Short())
	}

	// The Tower works on the branch that is checked out there; the Citadel's
	// main moves and the mirror is sent again.
	unsent := commit(t, w.be, tw.git, tw.repo(tbl.ID), "torre.go", "package torre\n", "feat: ainda nao enviado")
	clone, err := w.git.Open(w.ctx, w.wss.Layout().Principal(slug))
	if err != nil {
		t.Fatal(err)
	}
	moved := commit(t, w.be, w.git, clone, "principal.go", "package principal\n", "feat: no principal")
	if send() == nil {
		t.Fatal("main moved and nothing was sent")
	}
	if got := replicaRef("refs/heads/main"); got != moved {
		t.Errorf("main on the Tower = %s; want %s", got.Short(), moved.Short())
	}
	if got, _ := tw.replica().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); got != unsent {
		t.Errorf("the branch of the table on the Tower = %s; want the unsent commit %s untouched", got.Short(), unsent.Short())
	}

	// A branch of the mirror leaves it: it leaves the replica too, and nothing
	// of the Tower does.
	if err := w.mirror().CreateBranch(w.ctx, "feature", string(moved)); err != nil {
		t.Fatal(err)
	}
	send()
	if replicaRef("refs/heads/feature") != moved {
		t.Error("a new branch of the mirror did not reach the Tower")
	}
	if err := w.mirror().DeleteBranch(w.ctx, "feature"); err != nil {
		t.Fatal(err)
	}
	send()
	if replicaRef("refs/heads/feature") != "" {
		t.Error("a branch deleted on the Citadel stayed in the replica")
	}
	if got, _ := tw.replica().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); got != unsent {
		t.Errorf("the pruning touched the branch of the table on the Tower: %s", got.Short())
	}

	// An untrusted branch of a table does not go either, and what the Tower
	// had not sent still goes to the Citadel as the bundle of the table.
	foreign := commit(t, w.be, w.git, clone, "alheio.go", "package alheio\n", "feat: alheio")
	if err := w.mirror().UpdateRef(w.ctx, "refs/heads/"+tbl.Branch, foreign); err != nil {
		t.Fatal(err)
	}
	if m := send(); m != nil && slices.Contains(refNames(m), "refs/heads/"+tbl.Branch) {
		t.Errorf("an untrusted branch of a table went to the Tower: %s", m["refs"])
	}
	if got, _ := tw.replica().ResolveRef(w.ctx, "refs/heads/"+tbl.Branch); got != unsent {
		t.Errorf("the branch of the table on the Tower moved to %s", got.Short())
	}
	if filepath.Base(tw.replica().Dir()) != "espelho.git" {
		t.Errorf("the replica is at %s; want <dados>/workspaces/<slug>/espelho.git", tw.replica().Dir())
	}
}

func refNames(meta protocol.Object) []string {
	var m struct {
		Refs map[string]string `json:"refs"`
	}
	raw, err := json.Marshal(meta)
	if err != nil || json.Unmarshal(raw, &m) != nil {
		return nil
	}
	out := make([]string, 0, len(m.Refs))
	for k := range m.Refs {
		out = append(out, k)
	}
	return out
}
