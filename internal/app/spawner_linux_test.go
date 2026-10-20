//go:build linux

package app

import (
	"path/filepath"
	"testing"
)

// Part of CA-CIDADELA-16: the launcher is the spawner only with its socket and
// the cgroup that holds the terminals; without the cgroup the identifier would
// take a term-<id> cgroup anywhere, which an agent can make, so there is none.
func TestTheLauncherSpawnerNeedsTheSocketAndTheCgroupOfTheTerminals(t *testing.T) {
	t.Parallel()
	sock := filepath.Join(t.TempDir(), "l.sock")
	for name, term := range map[string]Terminals{
		"no socket": {CgroupBase: "/sys/fs/cgroup/system.slice/mestre-launcher.service"},
		"no cgroup": {Socket: sock},
	} {
		if sp, err := launcherSpawner(term); err == nil || sp != nil {
			t.Errorf("%s: %v, %v", name, sp, err)
		}
	}
	if sp, err := launcherSpawner(Terminals{Socket: sock, CgroupBase: "/sys/fs/cgroup/x"}); err != nil || sp == nil {
		t.Errorf("a complete configuration: %v, %v", sp, err)
	}
}
