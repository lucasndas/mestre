package chain

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"

	"github.com/mestre-ia/mestre/internal/store"
)

// PolicyDB is the PolicyStore in the database of the Guild: table policy_versions, one row per
// workspace with the text of the last valid policy file.
//
// Keep is asked while the policy is being read, which other services do inside a write of the
// database; a write there would wait for the one that waits for it. So Keep only records the
// text and a single goroutine writes it, the last text of each workspace winning.
type PolicyDB struct {
	DB     *store.DB
	Logger *slog.Logger

	mu      sync.Mutex
	pending map[string]policyRow
	running bool
	idle    *sync.Cond
}

type policyRow struct {
	hash string
	doc  []byte
}

var _ PolicyStore = (*PolicyDB)(nil)

// Last implements PolicyStore.
func (p *PolicyDB) Last(workspace string) ([]byte, bool, error) {
	var doc string
	err := p.DB.Reader().QueryRowContext(context.Background(), `SELECT doc FROM policy_versions WHERE workspace = ?`, workspace).Scan(&doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return []byte(doc), true, nil
}

// Keep implements PolicyStore.
func (p *PolicyDB) Keep(workspace, hash string, doc []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending == nil {
		p.pending = map[string]policyRow{}
		p.idle = sync.NewCond(&p.mu)
	}
	p.pending[workspace] = policyRow{hash: hash, doc: append([]byte(nil), doc...)}
	if !p.running {
		p.running = true
		go p.drain()
	}
}

// Flush waits until every text handed to Keep is written.
func (p *PolicyDB) Flush() {
	p.mu.Lock()
	defer p.mu.Unlock()
	for p.running {
		p.idle.Wait()
	}
}

func (p *PolicyDB) drain() {
	for {
		p.mu.Lock()
		var ws string
		var row policyRow
		found := false
		for ws, row = range p.pending {
			found = true
			break
		}
		if !found {
			p.running = false
			p.idle.Broadcast()
			p.mu.Unlock()
			return
		}
		delete(p.pending, ws)
		p.mu.Unlock()
		err := p.DB.WriteDurable(context.Background(), func(tx *sql.Tx) error {
			_, err := tx.Exec(`INSERT INTO policy_versions (workspace, hash, doc) VALUES (?, ?, ?)
				ON CONFLICT (workspace) DO UPDATE SET hash = excluded.hash, doc = excluded.doc WHERE hash <> excluded.hash`,
				ws, row.hash, string(row.doc))
			return err
		})
		if err != nil && p.Logger != nil {
			p.Logger.Warn("chain: the valid policy was not stored", "workspace", ws, "err", err)
		}
	}
}
