//go:build full

package conformance

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/logging"
)

// The git state of a run knows the main branch of the test repository, so
// a script of the worktree the run did not change is not a changed script
// (which would make every project script medio during the conformance).
func TestTheGitStateOfARunKnowsTheMainBranch(t *testing.T) {
	t.Parallel()
	git, err := gitx.New(gitx.Options{Suite: "full", Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	ws, err := prepare(t.Context(), git, t.TempDir(), false, time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	// The engine's record of the worktree, taken right after it was
	// created: the git folder in the mirror that the Codex sandbox opens.
	if rel, err := filepath.Rel(filepath.Join(ws.Mirror, "worktrees"), ws.GitDir); err != nil || rel == "." || filepath.IsAbs(rel) || rel[0] == '.' {
		t.Errorf("GitDir %q is not a worktree folder of the mirror %q", ws.GitDir, ws.Mirror)
	}
	st := classes.GitxState{Git: git, Main: MainBranch}
	changed, err := st.Changed(t.Context(), ws.Worktree, filepath.Join(ws.Worktree, "README.md"))
	if err != nil || changed {
		t.Errorf("README.md of the main branch, untouched: changed %v, %v", changed, err)
	}
}
