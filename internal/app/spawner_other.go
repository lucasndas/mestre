//go:build !linux

package app

import (
	"errors"

	"github.com/lucasndas/mestre/internal/terminal"
)

// launcherSpawner: the client of the launcher service of Windows arrives with
// the installation (milestone 7, CA-INST-18); until then only an injected
// spawner starts terminals.
func launcherSpawner(Terminals) (terminal.Spawner, error) {
	return nil, errors.New("app: the client of the launcher service is not built for this system yet (docs/desenvolvimento/ligacao-de-producao.md)")
}
