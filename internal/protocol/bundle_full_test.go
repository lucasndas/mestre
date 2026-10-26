//go:build full

package protocol_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/gitx"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/testkit/gitxsuite"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// A bundle whose prerequisite the Citadel does not have: at xfer.end the
// answer is missing_prerequisite and the Tower sends the whole branch. The
// verification is the real `git bundle verify`.
// CA-PROTO-05
func TestABundleWithoutThePrerequisiteIsAnsweredAndTheWholeBranchGoes(t *testing.T) {
	t.Parallel()
	g, err := gitx.New(gitx.Options{Clock: clock.NewFake(gitxsuite.Now), Masker: logging.NewMasker()})
	if err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, opts{})
	res := bundleWithoutPrerequisite(t, w, g, func(t testing.TB, r gitx.Repo, rel, content string) {
		p := filepath.Join(r.Dir(), filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	})
	checkBundleResult(t, res)
}
