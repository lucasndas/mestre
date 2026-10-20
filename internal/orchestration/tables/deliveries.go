package tables

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/lucasndas/mestre/internal/gitx"
)

// Stall is a table at work that has not delivered anything useful for a long
// time (docs/orquestracao/supervisao-da-cadeia.md#entrega-útil-e-mesa-que-gira).
type Stall struct {
	Table Table
	// Since is the instant the quiet counts from: the last useful delivery or
	// the entry of the table into work, the later of the two.
	Since time.Time
}

// delivery is what the engine keeps of the deliveries of a table.
type delivery struct {
	at      time.Time
	tip     string
	alerted time.Time
}

func (s Store) delivery(ctx context.Context, workspace, table string) (delivery, error) {
	var d delivery
	var at, alerted int64
	err := s.DB.Reader().QueryRowContext(ctx, `SELECT delivered_at, tip, alerted_for FROM table_deliveries WHERE workspace = ? AND table_id = ?`, workspace, table).Scan(&at, &d.tip, &alerted)
	if errors.Is(err, sql.ErrNoRows) {
		return delivery{}, nil
	}
	d.at, d.alerted = fromMs(at), fromMs(alerted)
	return d, err
}

func (s Store) putDelivery(ctx context.Context, workspace, table string, d delivery) error {
	return s.DB.WriteDurable(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO table_deliveries (workspace, table_id, delivered_at, tip, alerted_for) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (workspace, table_id) DO UPDATE SET delivered_at = excluded.delivered_at, tip = excluded.tip, alerted_for = excluded.alerted_for`,
			workspace, table, ms(d.at), d.tip, ms(d.alerted))
		return err
	})
}

// Delivered records a useful delivery of the table now: the pull request
// opened or updated, or a step of a mission that ended. A new commit on the
// branch is seen by Stalled.
func (s *Service) Delivered(ctx context.Context, workspace, table string) error {
	d, err := s.store.delivery(ctx, workspace, table)
	if err != nil {
		return err
	}
	d.at = s.clk.Now()
	return s.store.putDelivery(ctx, workspace, table, d)
}

// LastDelivery is the instant of the last useful delivery of the table (zero
// when there was none).
func (s *Service) LastDelivery(ctx context.Context, workspace, table string) (time.Time, error) {
	d, err := s.store.delivery(ctx, workspace, table)
	return d.at, err
}

// Stalled lists the tables at work that, for longer than after, delivered
// nothing useful: no new commit on the branch, no pull request opened or
// updated, no step of a mission ended. It skips the paused ones (also the ones
// the limit of use paused), the ones that wait for another table and the ones
// already warned about for this quiet. The commit of the branch is looked at
// here: a new one counts as a delivery from the moment it is seen.
func (s *Service) Stalled(ctx context.Context, after time.Duration) ([]Stall, error) {
	live, err := s.store.Live(ctx)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now()
	mirrors := map[string]gitx.Repo{}
	var out []Stall
	for _, t := range live {
		if t.State != StateWorking {
			continue
		}
		if p, err := s.Paused(ctx, t.Workspace, t.ID); err != nil || p {
			continue
		}
		if h, err := s.Held(ctx, t.Workspace, t.ID); err != nil || h {
			continue
		}
		d, err := s.store.delivery(ctx, t.Workspace, t.ID)
		if err != nil {
			return out, err
		}
		if tip := s.branchTip(ctx, mirrors, t); tip != "" && tip != d.tip {
			if d.tip != "" {
				d.at = now // a commit that was not there the last time
			}
			d.tip = tip
			if err := s.store.putDelivery(ctx, t.Workspace, t.ID, d); err != nil {
				return out, err
			}
		}
		since := d.at
		if t.UpdatedAt.After(since) {
			since = t.UpdatedAt
		}
		if now.Sub(since) >= after && !d.alerted.Equal(since) {
			out = append(out, Stall{Table: t, Since: since})
		}
	}
	return out, nil
}

// branchTip is the commit of the branch of the table in the mirror ("" when it
// can not be read).
func (s *Service) branchTip(ctx context.Context, mirrors map[string]gitx.Repo, t Table) string {
	m, ok := mirrors[t.Workspace]
	if !ok {
		var err error
		if m, err = s.o.Workspaces.OpenMirror(ctx, t.Workspace); err != nil {
			m = nil
		}
		mirrors[t.Workspace] = m
	}
	if m == nil {
		return ""
	}
	h, err := m.ResolveRef(ctx, "refs/heads/"+t.Branch)
	if err != nil {
		return ""
	}
	return string(h)
}

// StallWarned records that the warning of the quiet that began at since was
// sent: no other comes before the next delivery.
func (s *Service) StallWarned(ctx context.Context, workspace, table string, since time.Time) error {
	d, err := s.store.delivery(ctx, workspace, table)
	if err != nil {
		return err
	}
	d.alerted = since
	return s.store.putDelivery(ctx, workspace, table, d)
}
