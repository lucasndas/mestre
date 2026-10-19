package terminal_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/terminal"
)

// Part of CA-TERM-05: the engine can ask for the end of the same terminal twice at
// once (the stop, and the end of its agent), and the second request finds the
// cgroup removed by the first while it waits for it to empty: that is success,
// not "no such file". Here the first request is the missing cgroup.events of a
// directory that cgroup.kill was written in and that is gone by the time the
// wait begins.
func TestKillingACgroupThatTheOtherRequestJustRemovedIsSuccess(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "cgroup.kill"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := terminal.KillCgroupDir(t.Context(), dir); err != nil {
		t.Fatalf("KillCgroupDir = %v, want success for a cgroup that went while it waited", err)
	}
	if err := terminal.KillCgroupDir(t.Context(), filepath.Join(dir, "gone")); err != nil {
		t.Fatalf("KillCgroupDir of a cgroup already gone = %v", err)
	}
}
