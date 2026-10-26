package approvals

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Mode is how the approvals of a workspace are applied on GitHub.
type Mode string

// The modes (docs/github/aprovacao-no-github.md#plano-b-ruleset-com-o-grão-mestre-na-lista-de-exceção).
const (
	// ModeApp: the review of the App "Grão-Mestre" counts (or a ruleset lets it
	// through): the default.
	ModeApp Mode = "app"
	// ModeHuman: it does not and nothing lets it through: every merge asks a
	// person.
	ModeHuman Mode = "human"
)

// Modes keeps the mode of each workspace (github_review_modes).
type Modes struct {
	DB    *store.DB
	Clock clock.Clock
}

// Get is the mode of a workspace and why it is human, when it is; a workspace
// with no row is in the default mode.
func (m *Modes) Get(ctx context.Context, workspace string) (Mode, string, error) {
	var mode, reason string
	err := m.DB.Reader().QueryRowContext(ctx, `SELECT mode, reason FROM github_review_modes WHERE workspace = ?`, workspace).Scan(&mode, &reason)
	if errors.Is(err, sql.ErrNoRows) {
		return ModeApp, "", nil
	}
	if err != nil {
		return "", "", err
	}
	if Mode(mode) != ModeHuman {
		return ModeApp, "", nil
	}
	return ModeHuman, reason, nil
}

// Set records the mode of a workspace with the reason.
func (m *Modes) Set(ctx context.Context, workspace string, mode Mode, reason string) error {
	return m.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO github_review_modes (workspace, mode, reason, at) VALUES (?, ?, ?, ?)
			ON CONFLICT (workspace) DO UPDATE SET mode = excluded.mode, reason = excluded.reason, at = excluded.at`,
			workspace, string(mode), reason, m.Clock.Now().UnixMilli())
		return err
	})
}
