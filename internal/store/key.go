package store

import (
	"context"
	"fmt"
)

// RowKey is the primary key of the row a change touches: the names of the key
// columns and their values, from the new row (the old one for a delete). It
// is how a change is named without its values, as the audit of the
// transactions discarded in a change of Citadel lists them
// (docs/guilda/replicacao.md#perda-na-troca). A schema change has no key.
func (d *DB) RowKey(ctx context.Context, c Change) (cols []string, vals []any, err error) {
	if c.Op == OpSQL {
		return nil, nil, nil
	}
	ti, err := d.tableInfo(ctx, d.Reader(), c.Table)
	if err != nil {
		return nil, nil, fmt.Errorf("store: the key of %s: %w", c.Table, err)
	}
	row := c.New
	if c.Op == OpDelete {
		row = c.Old
	}
	for _, i := range ti.pk {
		if i >= len(row) {
			return nil, nil, fmt.Errorf("store: the key of %s: the row has %d columns", c.Table, len(row))
		}
		cols = append(cols, ti.cols[i])
		vals = append(vals, row[i])
	}
	return cols, vals, nil
}
