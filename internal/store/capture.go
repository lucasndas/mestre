package store

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
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
//     did not go through the log (a prepared statement or a RETURNING query
//     outside a transaction), and a statement that failed after changing
//     rows poisons its transaction, because the hook cannot take back what it
//     already reported: this holds for the statements that run through
//     Exec, through a prepared statement and through a query (its rows, up to
//     the last one);
//   - schema statements are refused unless the store itself runs them (the
//     migrations), and they enter the log as OpSQL; so are scripts of more
//     than one statement, ATTACH and the pragmas that rewrite the schema, and
//     a transaction whose schema version moved without a migration is
//     refused at commit, whatever the text of the statement looked like;
//   - nothing but the store writes to replication_log.

// ErrUnloggedWrite is wrapped by the error of a commit refused because the
// change did not go through the change log (CA-REPL-11).
var ErrUnloggedWrite = errors.New("store: the write did not go through the change log; it was refused")

// ErrSchemaOutsideMigration is returned for a schema statement run by
// anyone but the migrations on a database that replicates.
var ErrSchemaOutsideMigration = errors.New("store: schema changes of a replicated database go through migrations")

// ErrUnsupportedStatement is returned for the statements the log cannot
// follow (savepoints) or that open a way around it (ATTACH, the pragmas that
// rewrite the schema).
var ErrUnsupportedStatement = errors.New("store: statement not supported on a replicated database")

// ErrMultiStatement is returned for a script of several statements outside the
// migrations: each statement is looked at on its own, so one at a time.
var ErrMultiStatement = errors.New("store: a replicated database takes one statement at a time outside the migrations")

// ErrLogTableWrite is returned for a write to replication_log that is not the
// store's own: the log is what the replicas believe.
var ErrLogTableWrite = errors.New("store: replication_log is written by the store only")

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

type maintKey struct{}

// withMaintenance marks the statements of a retention pass over the log: they
// may delete from replication_log.
func withMaintenance(ctx context.Context) context.Context {
	return context.WithValue(ctx, maintKey{}, true)
}

func isMaintenance(ctx context.Context) bool {
	v, _ := ctx.Value(maintKey{}).(bool)
	return v
}

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
	refused bool  // the commit hook said no; read by whoever gets the error
	maint   bool  // a retention statement runs: it may delete from the log
	schema  int64 // schema_version when the transaction began
}

// reset forgets the transaction. It leaves refused alone: the rollback that
// follows a refused commit runs before the error is read.
func (s *connState) reset() {
	s.pending = s.pending[:0]
	s.poison, s.logging, s.logged = nil, false, false
}

func (s *connState) preupdate(d sqlite.SQLitePreUpdateData) {
	if d.DatabaseName != "main" {
		return
	}
	if d.TableName == LogTable {
		switch {
		case s.logging && d.Op == sqlite3.SQLITE_INSERT:
			s.logged = true
		case s.maint && d.Op == sqlite3.SQLITE_DELETE:
		case s.poison == nil:
			s.poison = ErrLogTableWrite
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

// PrepareContext refuses a statement the log cannot follow. A prepared write
// inside a transaction is logged at the commit like any other, and one that
// fails poisons the transaction; outside a transaction SQLite commits it on
// its own, and the commit hook refuses it.
func (c *logConn) PrepareContext(ctx context.Context, q string) (driver.Stmt, error) {
	c.st.refused = false
	if err := c.check(ctx, q); err != nil {
		return nil, err
	}
	st, err := c.inner.PrepareContext(ctx, q)
	if err != nil {
		return nil, err
	}
	return &logStmt{inner: st, c: c}, nil
}

// QueryContext runs a query. A statement that writes and returns rows
// (RETURNING) is checked as a write is: the rows it reports are poisoned when
// it fails, even at the last row.
func (c *logConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	c.st.refused = false
	if err := c.check(ctx, q); err != nil {
		return nil, err
	}
	mark := len(c.st.pending)
	rows, err := c.inner.QueryContext(ctx, q, args)
	if err != nil {
		c.failed(mark)
		return nil, c.mapErr(err)
	}
	return &logRows{Rows: rows, c: c, mark: mark}, nil
}

func (c *logConn) BeginTx(ctx context.Context, opts driver.TxOptions) (driver.Tx, error) {
	c.st.reset()
	c.st.refused = false
	tx, err := c.inner.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	ver, err := c.schemaVersion(ctx)
	if err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	c.st.schema = ver
	c.st.inTx = true
	return &logTx{c: c, inner: tx}, nil
}

// schemaVersion reads the schema cookie, which moves with every change of the
// schema, whatever statement made it.
func (c *logConn) schemaVersion(ctx context.Context) (int64, error) {
	rows, err := c.inner.QueryContext(ctx, `PRAGMA schema_version`, nil)
	if err != nil {
		return 0, fmt.Errorf("store: reading the schema version: %w", err)
	}
	defer rows.Close()
	dest := make([]driver.Value, 1)
	if err := rows.Next(dest); err != nil {
		return 0, fmt.Errorf("store: reading the schema version: %w", err)
	}
	v, ok := dest[0].(int64)
	if !ok {
		return 0, fmt.Errorf("store: schema version of type %T", dest[0])
	}
	return v, nil
}

// check applies the rules of the statements that need no wrapping, one
// statement at a time: a script can hide the one that matters behind another.
func (c *logConn) check(ctx context.Context, q string) error {
	mode := ddlOf(ctx)
	stmts := splitStatements(q)
	for _, s := range stmts {
		switch kind, rest := classifyStatement(s); kind {
		case stmtDDL:
			if mode == ddlNone {
				return ErrSchemaOutsideMigration
			}
		case stmtSavepoint, stmtAttach:
			return ErrUnsupportedStatement
		case stmtPragma:
			if name, sets := pragmaOf(rest); name == "writable_schema" || (name == "schema_version" && sets) {
				return ErrUnsupportedStatement
			}
		}
	}
	if len(stmts) > 1 && mode == ddlNone {
		return ErrMultiStatement
	}
	return nil
}

func (c *logConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	c.st.refused = false
	if err := c.check(ctx, q); err != nil {
		return nil, err
	}
	kind := classify(q)
	if ddlOf(ctx) == ddlLogged && c.st.inTx {
		return c.script(ctx, q, args)
	}
	if isMaintenance(ctx) {
		c.st.maint = true
		defer func() { c.st.maint = false }()
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

// mapErr turns the error of a statement or a commit that the commit hook
// refused into ErrUnloggedWrite.
func (c *logConn) mapErr(err error) error {
	if c.st.refused {
		c.st.refused = false
		return fmt.Errorf("%w: %w", ErrUnloggedWrite, err)
	}
	return err
}

// logStmt is a prepared statement of the write connection: it reports the
// failures the way the connection does.
type logStmt struct {
	inner driver.Stmt
	c     *logConn
}

func (s *logStmt) Close() error  { return s.inner.Close() }
func (s *logStmt) NumInput() int { return s.inner.NumInput() }

// Exec and Query are the old forms of the two methods below.
func (s *logStmt) Exec(args []driver.Value) (driver.Result, error) {
	return s.ExecContext(context.Background(), valuesToNamed(args))
}

func (s *logStmt) Query(args []driver.Value) (driver.Rows, error) {
	return s.QueryContext(context.Background(), valuesToNamed(args))
}

func (s *logStmt) ExecContext(ctx context.Context, args []driver.NamedValue) (driver.Result, error) {
	s.c.st.refused = false
	mark := len(s.c.st.pending)
	res, err := s.inner.(driver.StmtExecContext).ExecContext(ctx, args)
	if err != nil {
		s.c.failed(mark)
		return nil, s.c.mapErr(err)
	}
	return res, nil
}

func (s *logStmt) QueryContext(ctx context.Context, args []driver.NamedValue) (driver.Rows, error) {
	s.c.st.refused = false
	mark := len(s.c.st.pending)
	rows, err := s.inner.(driver.StmtQueryContext).QueryContext(ctx, args)
	if err != nil {
		s.c.failed(mark)
		return nil, s.c.mapErr(err)
	}
	return &logRows{Rows: rows, c: s.c, mark: mark}, nil
}

func valuesToNamed(args []driver.Value) []driver.NamedValue {
	out := make([]driver.NamedValue, len(args))
	for i, v := range args {
		out[i] = driver.NamedValue{Ordinal: i + 1, Value: v}
	}
	return out
}

// logRows reports the failure of a statement that fails while it returns
// rows: an INSERT ... RETURNING that hits a repeated key at the third row has
// already told the hook about two.
type logRows struct {
	driver.Rows
	c    *logConn
	mark int
}

func (r *logRows) Next(dest []driver.Value) error {
	err := r.Rows.Next(dest)
	if err != nil && !errors.Is(err, io.EOF) {
		r.c.failed(r.mark)
		return r.c.mapErr(err)
	}
	return err
}

func (r *logRows) Close() error {
	err := r.Rows.Close()
	if err != nil {
		r.c.failed(r.mark)
		return r.c.mapErr(err)
	}
	return nil
}

// The optional parts of driver.Rows that database/sql looks for.

func (r *logRows) ColumnTypeDatabaseTypeName(i int) string {
	if t, ok := r.Rows.(driver.RowsColumnTypeDatabaseTypeName); ok {
		return t.ColumnTypeDatabaseTypeName(i)
	}
	return ""
}

func (r *logRows) ColumnTypeLength(i int) (int64, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypeLength); ok {
		return t.ColumnTypeLength(i)
	}
	return 0, false
}

func (r *logRows) ColumnTypeNullable(i int) (bool, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypeNullable); ok {
		return t.ColumnTypeNullable(i)
	}
	return false, false
}

func (r *logRows) ColumnTypePrecisionScale(i int) (int64, int64, bool) {
	if t, ok := r.Rows.(driver.RowsColumnTypePrecisionScale); ok {
		return t.ColumnTypePrecisionScale(i)
	}
	return 0, 0, false
}

func (r *logRows) ColumnTypeScanType(i int) reflect.Type {
	if t, ok := r.Rows.(driver.RowsColumnTypeScanType); ok {
		return t.ColumnTypeScanType(i)
	}
	return reflect.TypeFor[any]()
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
	// A schema that moved without a migration is a change the log does not
	// carry, whatever the statement looked like.
	ver, err := c.schemaVersion(context.Background())
	if err != nil {
		_ = t.inner.Rollback()
		return err
	}
	if ver != c.st.schema && !slices.ContainsFunc(c.st.pending, func(ch Change) bool { return ch.Op == OpSQL }) {
		_ = t.inner.Rollback()
		return ErrSchemaOutsideMigration
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
	stmtAttach
	stmtPragma
)

// classify looks at the first statement of a script: a script is wrapped by
// what it starts with, and check has already refused the ones that hide
// something behind it.
func classify(q string) stmtKind {
	stmts := splitStatements(q)
	if len(stmts) == 0 {
		return stmtOther
	}
	k, _ := classifyStatement(stmts[0])
	return k
}

// classifyStatement reads the leading keyword of one statement and gives back
// what follows it.
func classifyStatement(s string) (stmtKind, string) {
	w, rest := firstWord(s)
	switch w {
	case "INSERT", "REPLACE", "UPDATE", "DELETE", "WITH":
		return stmtDML, rest
	case "CREATE", "ALTER", "DROP":
		return stmtDDL, rest
	case "ATTACH", "DETACH":
		return stmtAttach, rest
	case "PRAGMA":
		return stmtPragma, rest
	case "SAVEPOINT", "RELEASE":
		return stmtSavepoint, rest
	case "ROLLBACK":
		after := strings.ToUpper(skipBlank(rest))
		after = strings.TrimPrefix(after, "TRANSACTION")
		if strings.HasPrefix(skipBlank(after), "TO") {
			return stmtSavepoint, rest
		}
	}
	return stmtOther, rest
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
