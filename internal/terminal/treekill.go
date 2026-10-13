package terminal

import (
	"context"
	"errors"
	"io/fs"
)

// TreeKiller is a Spawner that can also end the mestre-pty and the sessions
// of a terminal with the right of the account of the agents. On Linux the
// engine runs as another account (`mestre`, without CAP_KILL), so without a
// delegated cgroup (where the launcher's cgroup.kill reaches everything) it
// cannot signal what `mestre-agente` runs; the launcher can take that
// account's user ID on a thread of its own. It only ends a process whose
// start identity it checks (docs/arquitetura/terminal.md#encerramento).
type TreeKiller interface {
	// KillTree ends the mestre-pty p of terminal id and every process of the
	// sessions that started after it, owned by the account of the agents.
	KillTree(ctx context.Context, id string, p ProcID, sessions []int) error
}

// killTree ends the mestre-pty and the sessions of terminal id. The engine
// tries first, with its own right (the same account, tests, Windows); when
// the system refuses for lack of permission, the spawner's launcher does it.
func (m *Manager) killTree(ctx context.Context, id string, p ProcID, sessions []int) error {
	err := m.cfg.System.KillTree(ctx, p, sessions)
	if err == nil || !errors.Is(err, fs.ErrPermission) {
		return err
	}
	tk, ok := m.cfg.Spawner.(TreeKiller)
	if !ok {
		return err
	}
	m.log.Info("the engine may not signal the terminal's processes: asking the launcher", "terminal", id, "pid", p.PID)
	if kerr := tk.KillTree(ctx, id, p, sessions); kerr != nil {
		return errors.Join(err, kerr)
	}
	return nil
}
