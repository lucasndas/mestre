package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"sync"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Capture of the changes of the Guild database
// (docs/arquitetura/persistencia.md#banco-configuração).
//
// The write connection of a database opened with Options.Capture is wrapped
// at the level of database/sql's driver, so no caller of Writer() has to
// know about the log:
//
//   - SQLite's preupdate hook, installed on the connection when the pool opens
//     it, hands over every row change, cascades included, with the whole old
//     and new row;
//   - the commit of a transaction that changed rows first inserts the
//     replication_log row with those changes, in the same transaction, and a
//     statement outside a transaction is wrapped in one;
//   - the commit hook refuses, as a last defence, a commit with changes that
//     did not go through the log (a prepared statement, a RETURNING query),
//     and a statement that failed after changing rows poisons its transaction,
//     because the hook cannot take back what it already reported;
//   - schema statements are refused unless the store itself runs them (the
//     migrations), and they enter the log as OpSQL.

// ErrUnloggedWrite is wrapped by the error of a commit refused because the
// change did not go through the change log (CA-REPL-11).
var ErrUnloggedWrite = errors.New("store: the write did not go through the change log; it was refused")

// ErrSchemaOutsideMigration is returned for a schema statement run by
// anyone but the migrations on a database that replicates.
var ErrSchemaOutsideMigration = errors.New("store: schema changes of a replicated database go through migrations")

// ErrUnsupportedStatement is returned for the statements the log cannot
// follow (savepoints).
var ErrUnsupportedStatement = errors.New("store: statement not supported on a replicated database")

type ddlMode int

const (
	ddlNone ddlMode = iota
	// ddlLogged is a migration: the statement runs and travels in the log.
	ddlLogged
	// ddlSilent is the store's own bootstrap (the log table, schema_migrations):
	// it exists before the first record and so on every copy.
	ddlSilent
)

type ddlKey struct{}

func withDDL(ctx context.Context, m ddlMode) context.Context {
	return context.WithValue(ctx, ddlKey{}, m)
}

func ddlOf(ctx context.Context) ddlMode {
	m, _ := ctx.Value(ddlKey{}).(ddlMode)
	return m
}

// capture is the part of a database that outlives its connections: the clock
// that stamps the log and the broadcast of the commits.
type capture struct {
	clk clock.Clock

	mu    sync.Mutex
	watch chan struct{}
}

func newCapture(clk clock.Clock) *capture {
	return &capture{clk: clk, watch: make(chan struct{})}
}

// Watch returns a channel that is closed at the next commit that wrote to the
// log.
func (c *capture) Watch() <-chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.watch
}

func (c *capture) wake() {
	c.mu.Lock()
	close(c.watch)
	c.watch = make(chan struct{})
	c.mu.Unlock()
}

// connState is what the hooks of the write connection collect. A connection
// runs one statement at a time, and the hooks run inside that statement, so
// the state needs no lock.
type connState struct {
	pending []Change
	poison  error // the transaction cannot commit
	logging bool  // the log row is being inserted
	logged  bool
	inTx    bool
	refused bool
}

func (s *connState) reset() {
	s.pending = s.pending[:0]
	s.poison, s.logging, s.logged, s.refused = nil, false, false, false
}

func (s *connState) preupdate(d sqlite.SQLitePreUpdateData) {
	if d.DatabaseName != "main" {
		return
	}
	if d.TableName == LogTable {
		if s.logging && d.Op == sqlite3.SQLITE_INSERT {
			s.logged = true
		}
		return
	}
	c := Change{Table: d.TableName}
	n := d.Count()
	switch d.Op {
	case sqlite3.SQLITE_INSERT:
		c.Op = OpInsert
	case sqlite3.SQLITE_UPDATE:
		c.Op = OpUpdate
	case sqlite3.SQLITE_DELETE:
		c.Op = OpDelete
	default:
		return
	}
	if c.Op != OpInsert {
		c.Old = make([]any, n)
		if err := d.Old(c.Old...); err != nil && s.poison == nil {
			s.poison = fmt.Errorf("store: reading the old row of %s: %w", d.TableName, err)
		}
	}
	if c.Op != OpDelete {
		c.New = make([]any, n)
		if err := d.New(c.New...); err != nil && s.poison == nil {
			s.poison = fmt.Errorf("store: reading the new row of %s: %w", d.TableName, err)
		}
	}
	s.pending = append(s.pending, c)
}

// commit is SQLite's commit hook: a non-zero result turns the commit into a
// rollback.
func (s *connState) commit() int32 {
	if s.poison != nil || (len(s.pending) > 0 && !s.logged) {
		s.refused = true
		return 1
	}
	return 0
}

func (s *connState) rollback() { s.reset() }

// innerConn is what the connection of the sqlite driver offers and
// database/sql looks for.
type innerConn interface {
	driver.Conn
	driver.ConnBeginTx
	driver.ConnPrepareContext
	driver.ExecerContext
	driver.QueryerContext
	driver.Pinger
	driver.SessionResetter
	driver.Validator
}

// logConnector wraps the connector of the sqlite driver so every connection
// it opens has the hooks and the log.
type logConnector struct {
	driver.Connector
	cap *capture
}

func (c logConnector) Connect(ctx context.Context) (driver.Conn, error) {
	raw, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	inner, ok := raw.(innerConn)
	hooks, hok := raw.(sqlite.HookRegisterer)
	if !ok || !hok {
		_ = raw.Close()
		return nil, fmt.Errorf("store: the sqlite connection %T has no hooks", raw)
	}
	st := &connState{}
	hooks.RegisterPreUpdateHook(st.preupdate)
	hooks.RegisterCommitHook(st.commit)
	hooks.RegisterRollbackHook(st.rollback)
	return &logConn{inner: inner, st: st, cap: c.cap}, nil
}

// logConn is the write connection of a database that replicates.
type logConn struct {
	inner innerConn
	st    *connState
	cap   *capture
}

func (c *logConn) Prepare(q string) (driver.Stmt, error) {
	return c.PrepareContext(context.Background(), q)
}
func (c *logConn) Close() error { return c.inner.Close() }
func (c *logConn) Begin() (driver.Tx, error) {
	return c.BeginTx(context.Background(), driver.TxOptions{})
}
func (c *logConn) Ping(ctx context.Context) error { return c.inner.Ping(ctx) }
func (c *logConn) IsValid() bool                  { return c.inner.IsValid() }
func (c *logConn) ResetSession(ctx context.Context) error {
	return c.inner.ResetSession(ctx)
}

// PrepareContext refuses a statement the log cannot follow; a prepared write
// otherwise runs outside the wrapper, and the commit hook refuses it.
func (c *logConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	if err := c.check(ctx, q); err != nil {
		return nil, err
	}
	return c.inner.PrepareContext(ctx, q)
}

func (c *logConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if err := c.check(ctx, q); err != nil {
		return nil, err
	}
	return c.inner.QueryContext(ctx, q, args)
}

func (c *logConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.st.reset()
	tx, err := c.inner.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	c.st.inTx = true
	return &logTx{c: c, inner: tx}, nil
}

// check applies the rules of the statements that need no wrapping.
func (c *logConn) check(ctx context.Context, q string) error {
	switch classify(q) {
	case stmtDDL:
		if ddlOf(ctx) == ddlNone {
			return ErrSchemaOutsideMigration
		}
	case stmtSavepoint:
		return ErrUnsupportedStatement
	}
	return nil
}

func (c *logConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	kind := classify(q)
	if err := c.check(ctx, q); err != nil {
		return nil, err
	}
	if ddlOf(ctx) == ddlLogged && c.st.inTx {
		return c.script(ctx, q, args)
	}
	if kind == stmtDML && !c.st.inTx {
		return c.autocommit(ctx, q, args)
	}
	mark := len(c.st.pending)
	res, err := c.inner.ExecContext(ctx, q, args)
	if err != nil {
		c.failed(mark)
		return nil, c.mapErr(err)
	}
	return res, nil
}

// script runs a migration. It travels in the log as its SQL, which the
// replica replays; the rows the script itself changed are left out, or the
// replica would change them twice.
func (c *logConn) script(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	at := len(c.st.pending)
	c.st.pending = append(c.st.pending, Change{Op: OpSQL, SQL: q})
	res, err := c.inner.ExecContext(ctx, q, args)
	if err != nil {
		c.failed(at)
		return nil, c.mapErr(err)
	}
	c.st.pending = c.st.pending[:at+1]
	return res, nil
}

// failed handles a statement that returned an error: rows it changed before
// failing were already reported by the hook and SQLite took them back, so the
// transaction can no longer be logged faithfully.
func (c *logConn) failed(mark int) {
	if c.st.inTx && len(c.st.pending) > mark && c.st.poison == nil {
		c.st.poison = errors.New("store: a statement failed after changing rows; roll the transaction back")
	}
}

// autocommit runs a write statement that came outside a transaction as its
// own, so it is logged like any other.
func (c *logConn) autocommit(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	tx, err := c.BeginTx(ctx, driver.TxOptions{})
	if err != nil {
		return nil, err
	}
	res, err := c.inner.ExecContext(ctx, q, args)
	if err != nil {
		_ = tx.Rollback()
		return nil, c.mapErr(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return res, nil
}

func (c *logConn) mapErr(err error) error {
	if c.st.refused {
		c.st.refused = false
		return fmt.Errorf("%w (%v)", ErrUnloggedWrite, err)
	}
	return err
}

// logTx puts the log row in the transaction before it commits.
type logTx struct {
	c     *logConn
	inner driver.Tx
}

func (t *logTx) Commit() error {
	c := t.c
	defer func() {
		c.st.inTx = false
		c.st.reset()
	}()
	if err := c.st.poison; err != nil {
		_ = t.inner.Rollback() // its hook clears the state, so the error is read first
		return err
	}
	wrote := len(c.st.pending) > 0
	if wrote {
		c.st.logging = true
		body := EncodeChanges(c.st.pending)
		_, err := c.inner.ExecContext(context.Background(),
			`INSERT INTO replication_log (lsn, at, body) VALUES ((SELECT coalesce(max(lsn), 0) + 1 FROM replication_log), ?, ?)`,
			[]driver.NamedValue{{Ordinal: 1, Value: c.cap.clk.Now().UnixMilli()}, {Ordinal: 2, Value: body}})
		c.st.logging = false
		if err != nil {
			_ = t.inner.Rollback()
			return fmt.Errorf("store: writing the change log: %w", err)
		}
	}
	if err := t.inner.Commit(); err != nil {
		return c.mapErr(err)
	}
	if wrote {
		c.cap.wake()
	}
	return nil
}

func (t *logTx) Rollback() error {
	defer func() {
		t.c.st.inTx = false
		t.c.st.reset()
	}()
	return t.inner.Rollback()
}

type stmtKind int

const (
	stmtOther stmtKind = iota
	stmtDML
	stmtDDL
	stmtSavepoint
)

// classify looks at the first words of a statement. A script that starts with
// a write is wrapped as a whole; a schema statement hidden after a write in
// the same script is not seen, which is why the migrations are the only ones
// who run scripts.
func classify(q string) stmtKind {
	s := skipBlank(q)
	end := 0
	for end < len(s) && (s[end] >= 'a' && s[end] <= 'z' || s[end] >= 'A' && s[end] <= 'Z') {
		end++
	}
	switch strings.ToUpper(s[:end]) {
	case "INSERT", "REPLACE", "UPDATE", "DELETE", "WITH":
		return stmtDML
	case "CREATE", "ALTER", "DROP":
		return stmtDDL
	case "SAVEPOINT", "RELEASE":
		return stmtSavepoint
	case "ROLLBACK":
		rest := strings.ToUpper(skipBlank(s[end:]))
		rest = strings.TrimPrefix(rest, "TRANSACTION")
		if strings.HasPrefix(skipBlank(rest), "TO") {
			return stmtSavepoint
		}
	}
	return stmtOther
}

// skipBlank drops leading spaces and SQL comments.
func skipBlank(s string) string {
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		switch {
		case strings.HasPrefix(s, "--"):
			i := strings.IndexByte(s, '\n')
			if i < 0 {
				return ""
			}
			s = s[i+1:]
		case strings.HasPrefix(s, "/*"):
			i := strings.Index(s, "*/")
			if i < 0 {
				return ""
			}
			s = s[i+2:]
		default:
			return s
		}
	}
}
