//go:build linux

package app

import (
	"errors"

	"github.com/mestre-ia/mestre/internal/launcher"
	"github.com/mestre-ia/mestre/internal/terminal"
)

// launcherSpawner asks the launcher service at the socket to start the
// mestre-pty as the account of the agents. A caller is known by the term-<id>
// cgroup under the cgroup of the launcher service, which the installation
// names; without it the identifier would take a cgroup of that name anywhere,
// one an agent could make itself, so the engine does not start.
func launcherSpawner(t Terminals) (terminal.Spawner, error) {
	switch {
	case t.Socket == "":
		return nil, errors.New("app: terminais.socket names no launcher and the engine has no other spawner")
	case t.CgroupBase == "":
		return nil, errors.New("app: terminais.cgroup names no cgroup of the launcher: callers cannot be told apart without it")
	}
	return launcher.NewClient(t.Socket), nil
}
