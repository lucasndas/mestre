package replication

import (
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/store"
)

// Defaults of docs/guilda/replicacao.md.
const (
	// DefaultAckEvery and DefaultAckRecords: the Tower confirms every second or
	// 256 records.
	DefaultAckEvery   = time.Second
	DefaultAckRecords = 256
	// DefaultLagThreshold and DefaultLagWindow: a lag over 5 s for more than a
	// minute is an alert of the checklist.
	DefaultLagThreshold = 5 * time.Second
	DefaultLagWindow    = time.Minute
	// DefaultBatchBytes bounds the records of a frame; a frame carries at most
	// protocol.MaxDataPayload.
	DefaultBatchBytes = 512 << 10
	// DefaultBatchRecords bounds the records read from the log at once.
	DefaultBatchRecords = 512
	// DefaultMaxQueueBytes is how much a Tower holds of records it did not
	// apply yet; over it the frames are dropped and asked for again.
	DefaultMaxQueueBytes = 64 << 20
	// PruneEvery is how often the retention runs.
	PruneEvery = time.Minute
)

// VaultTables are the tables of the vault: they go to every machine but the
// excluded one, which has them empty.
var VaultTables = []string{"secrets", "site_login_attempts"}

// The frame 0x03 (docs/conectividade/protocolo.md#multiplexação-e-prioridade):
// the ID is the guild_id, the position the lsn of the first record, and the
// payload a sequence of records, each
//
//	uvarint lsn | varint at | uvarint len(body) | body
//
// with the lsn of each one the lsn of the one before it plus one.

func appendRecord(b []byte, r store.LogRow) []byte {
	b = binary.AppendUvarint(b, r.LSN)
	b = binary.AppendVarint(b, r.At)
	b = binary.AppendUvarint(b, uint64(len(r.Body)))
	return append(b, r.Body...)
}

// recordSize is the size of the record on the wire.
func recordSize(r store.LogRow) int { return len(appendRecord(nil, r)) }

var errBadFrame = errors.New("replication: malformed frame of records")

func parseRecords(first uint64, data []byte) ([]store.LogRow, error) {
	var out []store.LogRow
	want := first
	for len(data) > 0 {
		lsn, n := binary.Uvarint(data)
		if n <= 0 {
			return nil, errBadFrame
		}
		data = data[n:]
		at, n := binary.Varint(data)
		if n <= 0 {
			return nil, errBadFrame
		}
		data = data[n:]
		size, n := binary.Uvarint(data)
		if n <= 0 || size > uint64(len(data)-n) {
			return nil, errBadFrame
		}
		data = data[n:]
		if lsn != want {
			return nil, fmt.Errorf("%w: record %d where %d should be", errBadFrame, lsn, want)
		}
		out = append(out, store.LogRow{LSN: lsn, At: at, Body: append([]byte(nil), data[:size]...)})
		data = data[size:]
		want++
	}
	return out, nil
}

// frameLimit is the most a frame of records carries.
const frameLimit = protocol.MaxDataPayload

// strip returns the record without the changes of the tables; the record
// keeps its lsn, so the sequence of the machine that does not get them has no
// hole.
func strip(r store.LogRow, tables map[string]bool) (store.LogRow, error) {
	cs, err := store.DecodeChanges(r.Body)
	if err != nil {
		return r, fmt.Errorf("record %d: %w", r.LSN, err)
	}
	kept := cs[:0:0]
	for _, c := range cs {
		if c.Op != store.OpSQL && tables[c.Table] {
			continue
		}
		kept = append(kept, c)
	}
	if len(kept) == len(cs) {
		return r, nil
	}
	r.Body = store.EncodeChanges(kept)
	return r, nil
}

func tableSet(names []string) map[string]bool {
	m := make(map[string]bool, len(names))
	for _, n := range names {
		m[n] = true
	}
	return m
}
