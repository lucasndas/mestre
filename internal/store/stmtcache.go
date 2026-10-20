package store

import (
	"context"
	"database/sql/driver"
	"fmt"
	"reflect"
	"strings"
)

// A per-connection cache of prepared statements, off unless Options.StmtCache
// asks for it. Preparing a statement is the costliest thing SQLite does for the
// short statements the engine runs (the parser and the code generator of the
// transpiled library are slow, and every transaction runs the same handful);
// the cache prepares each text once per connection and runs it again after a
// reset. Production leaves it off; the tests of the other packages turn it on
// (internal/testkit/guildtpl), where a world of a few hundred statements runs
// each of its few dozen texts several times.
//
// The cache sits under the wrappers of the connections (the log of the
// replication, the guard of a replica): they see the same calls as without it.
// A text that is already running (a query whose rows are still open, a nested
// query of a transaction) is not run through the cached statement: it takes the
// plain path, so two uses never share one statement.

// cachedConnector opens connections whose single statements are cached, up to
// max texts each.
type cachedConnector struct {
	driver.Connector
	max int
}

func (c cachedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	inner, ok := raw.(innerConn)
	if !ok {
		_ = raw.Close()
		return nil, fmt.Errorf("store: the sqlite connection %T lacks what the statement cache needs", raw)
	}
	return newCachedConn(inner, c.max), nil
}

// cachedConn runs the single statements of its text through the cache; the
// rest (scripts, DDL, transactions) goes to the connection as it is.
type cachedConn struct {
	innerConn
	max   int
	stmts map[string]*cachedStmt
}

type cachedStmt struct {
	st   driver.Stmt
	busy bool // running: its rows are open
}

// newCachedConn wraps c; max <= 0 returns c itself.
func newCachedConn(c innerConn, max int) innerConn {
	if max <= 0 {
		return c
	}
	return &cachedConn{innerConn: c, max: max, stmts: map[string]*cachedStmt{}}
}

// cacheable says whether q is one statement that reads or writes rows.
func cacheable(q string) bool {
	q = strings.TrimSpace(q)
	end := strings.IndexFunc(q, func(r rune) bool { return r == ' ' || r == '\n' || r == '\t' || r == '\r' || r == '(' })
	if end < 0 {
		return false
	}
	switch strings.ToUpper(q[:end]) {
	case "SELECT", "INSERT", "UPDATE", "DELETE", "REPLACE", "WITH", "PRAGMA":
	default:
		return false
	}
	return !strings.Contains(strings.TrimRight(q, "; \n\t\r"), ";")
}

// take returns the cached statement of q, prepared if it is new, marked busy;
// nil, with no error, means q takes the plain path.
func (c *cachedConn) take(ctx context.Context, q string) (*cachedStmt, error) {
	if !cacheable(q) {
		return nil, nil
	}
	if e, ok := c.stmts[q]; ok {
		if e.busy {
			return nil, nil
		}
		e.busy = true
		return e, nil
	}
	if len(c.stmts) >= c.max {
		return nil, nil
	}
	st, err := c.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	e := &cachedStmt{st: st, busy: true}
	c.stmts[q] = e
	return e, nil
}

func (c *cachedConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	e, err := c.take(ctx, q)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return c.innerConn.ExecContext(ctx, q, args)
	}
	defer func() { e.busy = false }()
	return e.st.(driver.StmtExecContext).ExecContext(ctx, args)
}

func (c *cachedConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	e, err := c.take(ctx, q)
	if err != nil {
		return nil, err
	}
	if e == nil {
		return c.innerConn.QueryContext(ctx, q, args)
	}
	rows, err := e.st.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		e.busy = false
		return nil, err
	}
	return &cachedRows{Rows: rows, e: e}, nil
}

// Close finalizes the cached statements before the connection closes: SQLite
// does not checkpoint and remove the WAL of a connection that still has any.
func (c *cachedConn) Close() error {
	for _, e := range c.stmts {
		_ = e.st.Close()
	}
	c.stmts = nil
	return c.innerConn.Close()
}

// cachedRows frees its statement for the next use when the rows are closed.
type cachedRows struct {
	driver.Rows
	e *cachedStmt
}

func (r *cachedRows) Close() error {
	err := r.Rows.Close()
	r.e.busy = false
	return err
}

// The optional parts of driver.Rows that database/sql looks for.

func (r *cachedRows) ColumnTypeDatabaseTypeName(i int) string {
	if t, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return t.ColumnTypeDatabaseTypeName(i)
	}
	return ""
}

func (r *cachedRows) ColumnTypeLength(i int) (int64, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypeLength); ok {
		return t.ColumnTypeLength(i)
	}
	return 0, false
}

func (r *cachedRows) ColumnTypeNullable(i int) (bool, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypeNullable); ok {
		return t.ColumnTypeNullable(i)
	}
	return false, false
}

func (r *cachedRows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypePrecisionScale); ok {
		return t.ColumnTypePrecisionScale(i)
	}
	return 0, 0, false
}

func (r *cachedRows) ColumnTypeScanType(i int) reflect.Type {
	if t, ok := r.Rows.(driver.RowsColumnTypeScanType); ok {
		return t.ColumnTypeScanType(i)
	}
	return reflect.TypeFor[any]()
}
