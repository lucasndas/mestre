package replication_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/testkit/fakegit"
	"github.com/lucasndas/mestre/internal/testkit/gitxsuite"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-REPL-08 (the mirrors; the criterion with real git and real sockets is in
// the full suite, and the mirror of a workspace itself is milestone 4)
func TestTheMirrorFollowsTheCitadelByBundleWithTheFakeGit(t *testing.T) {
	t.Parallel()
	mirrorScenario(t, fakegit.NewGit(clock.NewFake(gitxsuite.Now)), func(_ testing.TB, r gitx.Repo, rel, content string) {
		r.(*fakegit.Handle).SetFile(rel, content)
	})
}
