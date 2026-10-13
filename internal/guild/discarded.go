package guild

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// MaxDiscardedListed bounds the transactions one guild.discarded carries (the
// message has 1 MiB); Total still counts them all.
const MaxDiscardedListed = 500

// ListDiscarded reads the transactions the old Citadel committed after fromLSN,
// the last lsn the promoted machine applied: what the change of Citadel lost
// (docs/guilda/replicacao.md#perda-na-troca). old is the database the old
// Citadel kept as descartado-<época>.db. Each transaction names the rows it
// changed by table and key, never by value, so nothing of the vault leaves
// with it. At most limit are listed; total counts them all.
func ListDiscarded(ctx context.Context, old *store.DB, fromLSN uint64, limit int) (total int, txs []protocol.DiscardedTx, err error) {
	if limit <= 0 {
		limit = MaxDiscardedListed
	}
	after := fromLSN
	for {
		rows, err := old.LogSince(ctx, after, 256, 1<<20)
		if err != nil {
			return 0, nil, err
		}
		if len(rows) == 0 {
			return total, txs, nil
		}
		for _, r := range rows {
			after = r.LSN
			total++
			if len(txs) >= limit {
				continue
			}
			changes, err := store.DecodeChanges(r.Body)
			if err != nil {
				return 0, nil, fmt.Errorf("guild: discarded transaction %d: %w", r.LSN, err)
			}
			tx := protocol.DiscardedTx{LSN: r.LSN, At: protocol.Timestamp{Time: time.UnixMilli(r.At).UTC()}}
			for _, c := range changes {
				dc := protocol.DiscardedChange{Table: c.Table, Op: c.Op.String()}
				if c.Op != store.OpSQL {
					_, key, err := old.RowKey(ctx, c)
					if err != nil {
						return 0, nil, err
					}
					dc.Key = key
				}
				tx.Changes = append(tx.Changes, dc)
			}
			txs = append(txs, tx)
		}
	}
}

// DiscardedAction is the action of the audit rows of the discarded
// transactions.
const DiscardedAction = "citadel.discarded"

// RecordDiscarded writes, in the audit of the Guild, one row per transaction
// that the old Citadel machineID lost in the change (the handler of
// guild.discarded). A repeat of the list writes nothing new.
func RecordDiscarded(ctx context.Context, db *store.DB, clk clock.Clock, machineID string, p *protocol.GuildDiscarded) error {
	if clk == nil {
		clk = clock.Real{}
	}
	tx, err := db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("guild: record the discarded transactions: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	now := clk.Now()
	for _, d := range p.Transactions {
		detail, err := json.Marshal(map[string]any{"epoch": p.Epoch, "from_lsn": p.FromLSN, "lsn": d.LSN, "at": d.At, "changes": d.Changes})
		if err != nil {
			return fmt.Errorf("guild: record the discarded transactions: %w", err)
		}
		id := fmt.Sprintf("discarded-%d-%s-%d", p.Epoch, machineID, d.LSN)
		if err := audit(ctx, tx, id, now, machineID, DiscardedAction, detail); err != nil {
			return err
		}
	}
	if p.Total > len(p.Transactions) {
		detail, _ := json.Marshal(map[string]any{"epoch": p.Epoch, "from_lsn": p.FromLSN, "total": p.Total, "listed": len(p.Transactions)})
		if err := audit(ctx, tx, fmt.Sprintf("discarded-%d-%s-total", p.Epoch, machineID), now, machineID, DiscardedAction, detail); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("guild: record the discarded transactions: %w", err)
	}
	return nil
}

// DiscardedEntry is a row of the audit of the discarded transactions.
type DiscardedEntry struct {
	MachineID string
	Epoch     uint64
	LSN       uint64
}

// Discarded lists the discarded transactions in the audit of the Guild.
func Discarded(ctx context.Context, db *store.DB) ([]DiscardedEntry, error) {
	rows, err := db.Reader().QueryContext(ctx, `SELECT machine_id, detail FROM audit_log WHERE action = ? ORDER BY id`, DiscardedAction)
	if err != nil {
		return nil, fmt.Errorf("guild: read the audit: %w", err)
	}
	defer rows.Close()
	var out []DiscardedEntry
	for rows.Next() {
		var (
			e      DiscardedEntry
			detail string
			d      struct {
				Epoch uint64  `json:"epoch"`
				LSN   *uint64 `json:"lsn"`
			}
		)
		if err := rows.Scan(&e.MachineID, &detail); err != nil {
			return nil, fmt.Errorf("guild: read the audit: %w", err)
		}
		if err := json.Unmarshal([]byte(detail), &d); err != nil || d.LSN == nil {
			continue // the row of the total
		}
		e.Epoch, e.LSN = d.Epoch, *d.LSN
		out = append(out, e)
	}
	return out, rows.Err()
}

// DiscardedHandler is the handler of guild.discarded on the Citadel: db is
// the main Guild database of the Citadel of now.
func DiscardedHandler(db func() *store.DB, clk clock.Clock) protocol.HandlerFunc {
	return func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		d := db()
		if d == nil {
			return nil, protocol.NewError(protocol.CodeUnavailable, "this machine is not the Citadel now")
		}
		p, ok := m.Payload.(*protocol.GuildDiscarded)
		if !ok {
			return nil, protocol.NewError(protocol.CodeInvalidPayload, "guild.discarded payload is %T", m.Payload)
		}
		return nil, RecordDiscarded(ctx, d, clk, m.Peer.ID, p)
	}
}
