package characters

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/store"
)

// Launch is what the engine of a machine keeps of how a character was
// started, in the machine database (character_runtime.launch), to supervise
// it again after a restart of the engine: not the secrets, not the layer.
type Launch struct {
	Workspace string `json:"workspace"`
	Table     string `json:"table"`
	Name      string `json:"name"`
	Role      Role   `json:"role"`
	Kind      Kind   `json:"kind"`
	Machine   string `json:"machine"`
	// Dir is the worktree of the character, Tmp its MESTRE_TMP.
	Dir string `json:"dir"`
	Tmp string `json:"tmp,omitempty"`
	// Provider is the provider of its sheet, Class its class.
	Provider providers.ID `json:"provider"`
	Class    string       `json:"class,omitempty"`
	// Ready is how the base says the terminal is ready: "hook", "pattern" or
	// "delay"; ReadyPattern the regex, ReadyDelay the seconds.
	Ready        string `json:"ready"`
	ReadyPattern string `json:"ready_pattern,omitempty"`
	ReadyDelay   int    `json:"ready_delay,omitempty"`
}

// RuntimeRow is a row of character_runtime.
type RuntimeRow struct {
	Agent, Terminal string
	Status
	Session, LastTurn string
	Reduced           string
	// Starts counts the starts of the character on this machine.
	Starts   int
	Launch   Launch
	Since    time.Time
	LastSeen time.Time
}

// LocalStore keeps the runtime rows in the machine database.
type LocalStore struct{ DB *store.DB }

func (l LocalStore) put(ctx context.Context, r RuntimeRow) error {
	raw, err := json.Marshal(r.Launch)
	if err != nil {
		return err
	}
	_, err = l.DB.Writer().ExecContext(ctx, `INSERT INTO character_runtime
		(agent_id, terminal_id, state, reason, provider_session_id, last_turn, reduced_monitoring, starts, launch, state_since, last_signal_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(agent_id) DO UPDATE SET terminal_id = excluded.terminal_id, state = excluded.state, reason = excluded.reason,
			provider_session_id = excluded.provider_session_id, last_turn = excluded.last_turn, reduced_monitoring = excluded.reduced_monitoring,
			starts = excluded.starts, launch = excluded.launch, state_since = excluded.state_since, last_signal_at = excluded.last_signal_at`,
		r.Agent, r.Terminal, string(r.State), r.Reason, r.Session, r.LastTurn, r.Reduced, r.Starts, string(raw), r.Since.UnixMilli(), r.LastSeen.UnixMilli())
	if err != nil {
		return fmt.Errorf("keep the state of %s: %w", r.Agent, err)
	}
	return nil
}

func scanRuntime(sc interface{ Scan(...any) error }) (RuntimeRow, error) {
	var r RuntimeRow
	var state, launch string
	var since, seen int64
	if err := sc.Scan(&r.Agent, &r.Terminal, &state, &r.Reason, &r.Session, &r.LastTurn, &r.Reduced, &r.Starts, &launch, &since, &seen); err != nil {
		return RuntimeRow{}, err
	}
	r.State = State(state)
	r.Since, r.LastSeen = time.UnixMilli(since).UTC(), time.UnixMilli(seen).UTC()
	if err := json.Unmarshal([]byte(launch), &r.Launch); err != nil {
		return RuntimeRow{}, fmt.Errorf("runtime row of %s: %w", r.Agent, err)
	}
	return r, nil
}

const runtimeColumns = `agent_id, terminal_id, state, reason, provider_session_id, last_turn, reduced_monitoring, starts, launch, state_since, last_signal_at`

// Get returns the row of a character; ok is false if the machine has none.
func (l LocalStore) Get(ctx context.Context, agent string) (RuntimeRow, bool, error) {
	r, err := scanRuntime(l.DB.Reader().QueryRowContext(ctx, `SELECT `+runtimeColumns+` FROM character_runtime WHERE agent_id = ?`, agent))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return RuntimeRow{}, false, nil
	case err != nil:
		return RuntimeRow{}, false, err
	}
	return r, true, nil
}

// List returns every row.
func (l LocalStore) List(ctx context.Context) ([]RuntimeRow, error) {
	rows, err := l.DB.Reader().QueryContext(ctx, `SELECT `+runtimeColumns+` FROM character_runtime ORDER BY agent_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RuntimeRow
	for rows.Next() {
		r, err := scanRuntime(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Delete forgets a character that ended for good.
func (l LocalStore) Delete(ctx context.Context, agent string) error {
	_, err := l.DB.Writer().ExecContext(ctx, `DELETE FROM character_runtime WHERE agent_id = ?`, agent)
	return err
}
