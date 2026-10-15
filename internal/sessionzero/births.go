package sessionzero

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/orchestration/presets"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Birth is what a character was born with: the version of the models of its
// Mestre layer and the copy of its sheet and base, which later edits of
// either do not change (docs/sessao-zero/personagem.md#modelos-versionados-e-regeneração,
// docs/orquestracao/bases-e-fichas.md#onde-ficam-e-quem-edita).
type Birth struct {
	AgentID  string
	Provider providers.ID
	Version  string
	Snapshot presets.Snapshot
	BornAt   time.Time
}

// Births keeps the births of the characters of the machine.
type Births interface {
	// Get is the birth of a character.
	Get(ctx context.Context, agentID string) (Birth, bool, error)
	// Put records a birth; a character is born once, so a second Put of
	// the same id is an error.
	Put(ctx context.Context, b Birth) error
	// SetVersion moves a character to another version of the models: the
	// regeneration.
	SetVersion(ctx context.Context, agentID, version string) error
	// Delete forgets a character that ended for good.
	Delete(ctx context.Context, agentID string) error
}

// ErrBorn is a second birth of the same character.
var ErrBorn = errors.New("the character was already born")

// BirthsDB keeps the births in the machine database (character_births).
type BirthsDB struct {
	DB    *store.DB
	Clock clock.Clock
}

var _ Births = BirthsDB{}

// Get implements Births.
func (d BirthsDB) Get(ctx context.Context, agentID string) (Birth, bool, error) {
	b := Birth{AgentID: agentID}
	var provider, sheet, base string
	var born int64
	err := d.DB.Reader().QueryRowContext(ctx, `SELECT provider, model_version, sheet, base, born_at FROM character_births WHERE agent_id = ?`, agentID).
		Scan(&provider, &b.Version, &sheet, &base, &born)
	if errors.Is(err, sql.ErrNoRows) {
		return Birth{}, false, nil
	}
	if err != nil {
		return Birth{}, false, fmt.Errorf("birth of %s: %w", agentID, err)
	}
	b.Provider, b.BornAt = providers.ID(provider), time.UnixMilli(born).UTC()
	if err := json.Unmarshal([]byte(sheet), &b.Snapshot.Sheet); err != nil {
		return Birth{}, false, fmt.Errorf("birth of %s: sheet: %w", agentID, err)
	}
	if err := json.Unmarshal([]byte(base), &b.Snapshot.Base); err != nil {
		return Birth{}, false, fmt.Errorf("birth of %s: base: %w", agentID, err)
	}
	return b, true, nil
}

// Put implements Births.
func (d BirthsDB) Put(ctx context.Context, b Birth) error {
	sheet, err := json.Marshal(b.Snapshot.Sheet)
	if err != nil {
		return err
	}
	base, err := json.Marshal(b.Snapshot.Base)
	if err != nil {
		return err
	}
	born := b.BornAt
	if born.IsZero() {
		born = d.Clock.Now()
	}
	return d.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO character_births (agent_id, provider, model_version, sheet, base, born_at)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`, b.AgentID, string(b.Provider), b.Version, string(sheet), string(base), born.UTC().UnixMilli())
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: %s", ErrBorn, b.AgentID)
		}
		return nil
	})
}

// SetVersion implements Births.
func (d BirthsDB) SetVersion(ctx context.Context, agentID, version string) error {
	return d.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE character_births SET model_version = ? WHERE agent_id = ?`, version, agentID)
		return err
	})
}

// Delete implements Births.
func (d BirthsDB) Delete(ctx context.Context, agentID string) error {
	return d.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM character_births WHERE agent_id = ?`, agentID)
		return err
	})
}
