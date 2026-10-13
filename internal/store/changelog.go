package store

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// The change log of the Guild database (docs/guilda/replicacao.md#banco-cópia-completa-e-envio-contínuo):
// every committed transaction that changes a row leaves one row in
// replication_log(lsn, at, body), written in the same transaction. The body
// is the list of changes in the order SQLite made them, cascades included,
// encoded as below.

// LogTable is the table of the change log.
const LogTable = "replication_log"

// createLogTable is the DDL of the log. The table is not a numbered
// migration: like schema_migrations it exists before any migration runs, so
// the migrations themselves are logged.
const createLogTable = `CREATE TABLE IF NOT EXISTS replication_log (
	lsn  INTEGER PRIMARY KEY,
	at   INTEGER NOT NULL,
	body BLOB    NOT NULL
)`

// Op is the kind of a change.
type Op byte

// The operations of a change. A schema change (a migration) is OpSQL: the
// replica runs the statement as it is.
const (
	OpInsert Op = 1
	OpUpdate Op = 2
	OpDelete Op = 3
	OpSQL    Op = 4
)

func (o Op) String() string {
	switch o {
	case OpInsert:
		return "insert"
	case OpUpdate:
		return "update"
	case OpDelete:
		return "delete"
	case OpSQL:
		return "sql"
	}
	return fmt.Sprintf("op(%d)", byte(o))
}

// Change is one row change, or one schema statement. Old and New are the
// whole row, a value per column in table order: nil, int64, float64, string
// or []byte.
type Change struct {
	Table string
	Op    Op
	Old   []any // UPDATE and DELETE
	New   []any // INSERT and UPDATE
	SQL   string
}

// LogRow is one row of replication_log. Body is the encoded changes.
type LogRow struct {
	LSN  uint64
	At   int64 // Unix milliseconds
	Body []byte
}

// ErrBadLog is wrapped by the decoding errors.
var ErrBadLog = errors.New("store: malformed change log record")

const bodyVersion = 1

// EncodeChanges is the form of the changes in replication_log.body and on
// the wire: a version byte, the number of changes, then for each change its
// operation and table (or its SQL) and the rows, every value tagged by its
// type. Integers are varints, so a small row is a few bytes.
func EncodeChanges(cs []Change) []byte {
	b := make([]byte, 0, 64)
	b = append(b, bodyVersion)
	b = binary.AppendUvarint(b, uint64(len(cs)))
	for _, c := range cs {
		b = append(b, byte(c.Op))
		if c.Op == OpSQL {
			b = appendBytes(b, []byte(c.SQL))
			continue
		}
		b = appendBytes(b, []byte(c.Table))
		if c.Op != OpInsert {
			b = appendRow(b, c.Old)
		}
		if c.Op != OpDelete {
			b = appendRow(b, c.New)
		}
	}
	return b
}

const (
	tagNull  byte = 0
	tagInt   byte = 1
	tagFloat byte = 2
	tagText  byte = 3
	tagBlob  byte = 4
)

func appendBytes(b, v []byte) []byte {
	b = binary.AppendUvarint(b, uint64(len(v)))
	return append(b, v...)
}

func appendRow(b []byte, row []any) []byte {
	b = binary.AppendUvarint(b, uint64(len(row)))
	for _, v := range row {
		switch x := v.(type) {
		case nil:
			b = append(b, tagNull)
		case int64:
			b = append(b, tagInt)
			b = binary.AppendVarint(b, x)
		case float64:
			b = append(b, tagFloat)
			b = binary.BigEndian.AppendUint64(b, math.Float64bits(x))
		case string:
			b = append(b, tagText)
			b = appendBytes(b, []byte(x))
		case []byte:
			b = append(b, tagBlob)
			b = appendBytes(b, x)
		default:
			// The hook only produces the five types above; anything else is a
			// bug that must not be silently stored as something else.
			panic(fmt.Sprintf("store: cannot log a value of type %T", v))
		}
	}
	return b
}

type reader struct {
	b   []byte
	err error
}

func (r *reader) fail(what string) {
	if r.err == nil {
		r.err = fmt.Errorf("%w: %s", ErrBadLog, what)
	}
}

func (r *reader) byte() byte {
	if r.err != nil || len(r.b) < 1 {
		r.fail("truncated")
		return 0
	}
	v := r.b[0]
	r.b = r.b[1:]
	return v
}

func (r *reader) uvarint() uint64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		r.fail("bad varint")
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) varint() int64 {
	if r.err != nil {
		return 0
	}
	v, n := binary.Varint(r.b)
	if n <= 0 {
		r.fail("bad varint")
		return 0
	}
	r.b = r.b[n:]
	return v
}

func (r *reader) bytes() []byte {
	n := r.uvarint()
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.b)) {
		r.fail("length over the end")
		return nil
	}
	v := r.b[:n:n]
	r.b = r.b[n:]
	return v
}

func (r *reader) row() []any {
	n := r.uvarint()
	if r.err != nil {
		return nil
	}
	if n > uint64(len(r.b)) { // every value takes at least a byte
		r.fail("row longer than the record")
		return nil
	}
	row := make([]any, n)
	for i := range row {
		switch r.byte() {
		case tagNull:
		case tagInt:
			row[i] = r.varint()
		case tagFloat:
			if len(r.b) < 8 {
				r.fail("truncated float")
				return nil
			}
			row[i] = math.Float64frombits(binary.BigEndian.Uint64(r.b))
			r.b = r.b[8:]
		case tagText:
			row[i] = string(r.bytes())
		case tagBlob:
			v := r.bytes()
			row[i] = append([]byte{}, v...) // a blob is never nil, so it never reads back as NULL
		default:
			r.fail("unknown value tag")
		}
		if r.err != nil {
			return nil
		}
	}
	return row
}

// maxPrealloc bounds what the decoder reserves up front for the number of
// changes it read from the record: that count is checked against the bytes
// left, which still allowed 88 times the size of the record (a row is
// bounded the same way but costs 16 bytes a value, which the record must
// really carry).
const maxPrealloc = 64

// DecodeChanges is the inverse of EncodeChanges.
func DecodeChanges(body []byte) ([]Change, error) {
	r := &reader{b: body}
	if v := r.byte(); r.err == nil && v != bodyVersion {
		return nil, fmt.Errorf("%w: version %d", ErrBadLog, v)
	}
	n := r.uvarint()
	if r.err == nil && n > uint64(len(r.b)) {
		r.fail("more changes than bytes")
	}
	if r.err != nil {
		return nil, r.err
	}
	cs := make([]Change, 0, min(n, maxPrealloc))
	for range n {
		var c Change
		c.Op = Op(r.byte())
		switch c.Op {
		case OpSQL:
			c.SQL = string(r.bytes())
		case OpInsert, OpUpdate, OpDelete:
			c.Table = string(r.bytes())
			if c.Op != OpInsert {
				c.Old = r.row()
			}
			if c.Op != OpDelete {
				c.New = r.row()
			}
		default:
			r.fail("unknown operation")
		}
		if r.err != nil {
			return nil, r.err
		}
		cs = append(cs, c)
	}
	if len(r.b) != 0 {
		return nil, fmt.Errorf("%w: trailing bytes", ErrBadLog)
	}
	return cs, nil
}
