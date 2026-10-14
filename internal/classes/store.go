package classes

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// SnapshotName is the file of the snapshot of the effective rules in the
// character's folder (docs/classes/permissoes.md#modo-degradado).
const SnapshotName = "policy.json"

// Store keeps the effective rules of the characters of this machine in the
// machine database and writes the snapshot the hook reads with the engine
// down. It is the RulesSource and the ExceptionLedger of the engine.
type Store struct {
	DB    *store.DB
	Clock clock.Clock
	// Agents is the folder of the characters (agentes/); the snapshot of a
	// character goes in Agents/<id>/policy.json. Empty writes none.
	Agents string
}

// ErrStale is a document older than the one kept: it is dropped.
var ErrStale = errors.New("effective rules older than the ones kept")

// Save keeps a document when it is not older than the one kept (by
// generated_at), writes its snapshot and forgets the used exceptions that
// left the document's list.
func (s *Store) Save(ctx context.Context, r *Rules) error {
	if err := r.Validate(); err != nil {
		return err
	}
	doc, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("effective rules: %w", err)
	}
	now := s.Clock.Now().UTC().UnixMilli()
	gen := r.GeneratedAt.UTC().UnixMilli()
	err = s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		var kept int64
		switch err := tx.QueryRowContext(ctx, `SELECT generated_at FROM agent_rules WHERE agent_id = ?`, r.AgentID).Scan(&kept); {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return err
		case gen < kept:
			return ErrStale
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO agent_rules (agent_id, generated_at, doc, received_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (agent_id) DO UPDATE SET generated_at = excluded.generated_at, doc = excluded.doc, received_at = excluded.received_at`,
			r.AgentID, gen, doc, now); err != nil {
			return err
		}
		listed := map[int64]bool{}
		for _, d := range r.Decrees {
			if d.Kind == DecreeException {
				listed[d.ID] = true
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT decree_id FROM used_exceptions WHERE agent_id = ?`, r.AgentID)
		if err != nil {
			return err
		}
		var gone []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if !listed[id] {
				gone = append(gone, id)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
		for _, id := range gone {
			if _, err := tx.ExecContext(ctx, `DELETE FROM used_exceptions WHERE agent_id = ? AND decree_id = ?`, r.AgentID, id); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrStale) {
			return err
		}
		return fmt.Errorf("effective rules of %s: %w", r.AgentID, err)
	}
	return s.writeSnapshot(r.AgentID, doc)
}

// Rules implements RulesSource; a character without rules is an error.
func (s *Store) Rules(ctx context.Context, agentID string) (*Rules, error) {
	var doc []byte
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT doc FROM agent_rules WHERE agent_id = ?`, agentID).Scan(&doc)
	if err != nil {
		return nil, fmt.Errorf("effective rules of %s: %w", agentID, err)
	}
	return ParseRules(doc)
}

// Use implements ExceptionLedger.
func (s *Store) Use(ctx context.Context, agentID string, id int64) (bool, error) {
	var n int64
	err := s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO used_exceptions (agent_id, decree_id, used_at) VALUES (?, ?, ?) ON CONFLICT DO NOTHING`,
			agentID, id, s.Clock.Now().UTC().UnixMilli())
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, fmt.Errorf("exception %d of %s: %w", id, agentID, err)
	}
	return n == 1, nil
}

// writeSnapshot writes Agents/<id>/policy.json whole (a temporary file
// renamed over it), readable by the agents' group and not writable by it.
func (s *Store) writeSnapshot(agentID string, doc []byte) error {
	if s.Agents == "" {
		return nil
	}
	if !ValidAgentID(agentID) {
		return fmt.Errorf("snapshot: invalid agent id %q", agentID)
	}
	dir := filepath.Join(s.Agents, agentID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".policy-*.json")
	if err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(doc); err != nil {
		tmp.Close()
		return fmt.Errorf("snapshot: %w", err)
	}
	if err := tmp.Chmod(0o640); err != nil {
		tmp.Close()
		return fmt.Errorf("snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	if err := os.Rename(tmp.Name(), filepath.Join(dir, SnapshotName)); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	return nil
}

// LoadSnapshot reads a snapshot for the degraded mode: nil, with the error,
// when it is missing or unreadable (then only the fixed rules hold).
func LoadSnapshot(agents, agentID string) (*Rules, error) {
	if !ValidAgentID(agentID) {
		return nil, fmt.Errorf("snapshot: invalid agent id %q", agentID)
	}
	data, err := os.ReadFile(filepath.Join(agents, agentID, SnapshotName))
	if err != nil {
		return nil, fmt.Errorf("snapshot: %w", err)
	}
	return ParseRules(data)
}

// Handler is agent.rules on the machine (docs/conectividade/mensagens.md):
// only the Citadel sends it; an invalid document is invalid_payload and the
// machine keeps the last valid one; an older one is acknowledged and dropped.
func (s *Store) Handler(isCitadel func(protocol.Peer) bool) protocol.HandlerFunc {
	return func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		if !isCitadel(m.Peer) {
			return nil, protocol.NewError(protocol.CodeForbidden, "only the Citadel sends agent.rules")
		}
		var obj protocol.Object
		switch p := m.Payload.(type) {
		case *protocol.Object:
			obj = *p
		case protocol.Object:
			obj = p
		default:
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "agent.rules payload is %T", m.Payload)
		}
		data, err := json.Marshal(obj)
		if err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "agent.rules: %v", err)
		}
		r, err := ParseRules(data)
		if err != nil {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
		}
		if err := s.Save(ctx, r); err != nil && !errors.Is(err, ErrStale) {
			return nil, err
		}
		return nil, nil
	}
}

// Message is the agent.rules the Citadel sends to the machine of the
// character.
func Message(r *Rules) (protocol.Outgoing, error) {
	data, err := json.Marshal(r)
	if err != nil {
		return protocol.Outgoing{}, err
	}
	var obj protocol.Object
	if err := json.Unmarshal(data, &obj); err != nil {
		return protocol.Outgoing{}, err
	}
	return protocol.Outgoing{Type: protocol.TypeAgentRules, Payload: obj}, nil
}
