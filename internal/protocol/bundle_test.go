package protocol_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/gitxsuite"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The criterion CA-PROTO-05 is in the full suite (bundle_full_test.go, with the
// real git); this runs the same scenario with the fake git so the fast suite
// covers the protocol side of it.
func TestBundleWithoutPrerequisiteIsAnsweredAndTheWholeBranchGoesWithTheFakeGit(t *testing.T) {
	t.Parallel()
	w := newWorld(t, opts{})
	g := fakegit.NewGit(clock.NewFake(gitxsuite.Now))
	res := bundleWithoutPrerequisite(t, w, g, func(_ testing.TB, r gitx.Repo, rel, content string) {
		r.(*fakegit.Handle).SetFile(rel, content)
	})
	checkBundleResult(t, res)
}
