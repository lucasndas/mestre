package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"sync/atomic"
)

// The write connection of a replica (docs/arquitetura/persistencia.md): it is
// query_only, and this wrapper is what keeps it that way. Outside the
// replication it takes reads and nothing else; in particular it refuses
// PRAGMA query_only, which would lift the guard, writable_schema, which
// rewrites the schema under it, and ATTACH, which opens another file with
// the guard off. The replication opens the gate for the length of one of its
// operations (DB.privileged).

// ErrReplicaWrite is returned for a statement run on the writer of a replica
// outside the replication.
var ErrReplicaWrite = errors.New("store: a replica takes writes from the replication only")

type guardConnector struct {
	driver.Connector
	cache int // Options.StmtCache
}

func (c guardConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	inner, ok := raw.(innerConn)
	if !ok {
		_ = raw.Close()
		return nil, fmt.Errorf("store: the sqlite connection %T lacks what the guard needs", raw)
	}
	return &guardConn{inner: newCachedConn(inner, c.cache)}, nil
}

type guardConn struct {
	inner innerConn
	open  atomic.Bool // the replication is running something
}

func (g *guardConn) Prepare(q string) (driver.Stmt, error) {
	return g.PrepareContext(context.Background(), q)
}
func (g *guardConn) Close() error { return g.inner.Close() }
func (g *guardConn) Begin() (driver.Tx, error) {
	return g.inner.BeginTx(context.Background(), driver.TxOptions{})
}
func (g *guardConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	return g.inner.BeginTx(ctx, opts)
}
func (g *guardConn) Ping(ctx context.Context) error { return g.inner.Ping(ctx) }
func (g *guardConn) IsValid() bool                  { return g.inner.IsValid() }
func (g *guardConn) ResetSession(ctx context.Context) error {
	return g.inner.ResetSession(ctx)
}

func (g *guardConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if err := g.check(q); err != nil {
		return nil, err
	}
	return g.inner.PrepareContext(ctx, q)
}

func (g *guardConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if err := g.check(q); err != nil {
		return nil, err
	}
	return g.inner.ExecContext(ctx, q, args)
}

func (g *guardConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := g.check(q); err != nil {
		return nil, err
	}
	return g.inner.QueryContext(ctx, q, args)
}

// check lets through, outside the replication, the statements that read:
// SELECT, VALUES, WITH (a write behind it still meets query_only), EXPLAIN
// [QUERY PLAN] of one of those and the pragmas that only report. EXPLAIN is
// judged by what it explains: SQLite runs a PRAGMA at compile time, even
// behind EXPLAIN.
func (g *guardConn) check(q string) error {
	if g.open.Load() {
		return nil
	}
	for _, s := range splitStatements(q) {
		w, rest := firstWord(s)
		if w == "EXPLAIN" {
			w, rest = firstWord(rest)
			if w == "QUERY" {
				_, rest = firstWord(rest) // PLAN
				w, rest = firstWord(rest)
			}
		}
		switch w {
		case "SELECT", "VALUES", "WITH":
		case "PRAGMA":
			if _, sets := pragmaOf(rest); sets {
				return ErrReplicaWrite
			}
		default:
			return ErrReplicaWrite
		}
	}
	return nil
}
