//go:build spike

// Package dbcapture is the M2 spike "captura das mudanças do banco"
// (docs/guilda/replicacao.md#verificar-na-implementação): the engine turns
// off automatic checkpoints, reads the new frames of the -wal file after each
// committed transaction (like Litestream) and ships them as a change log; a
// replica applies the page images to its own copy through sqlite_dbpage. The
// plan B (a logical replication_log written in the same transaction) is in
// logical.go. Results: resultados.md.
//
//	go test -tags spike -v ./tools/spikes/m2-db-capture
//
// Spike code: kept as reference, outside the lint and every suite, never
// imported by production code (docs/desenvolvimento/convencoes.md#estilo).
package dbcapture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// WAL file format (https://www.sqlite.org/fileformat2.html#walformat).
const (
	walHeaderSize   = 32
	frameHeaderSize = 24
	walMagicLE      = 0x377f0682
	walMagicBE      = 0x377f0683
)

// ErrGap means the capture cannot prove it saw every committed transaction:
// the WAL was reset (new salts) or removed without a checkpoint the capture
// asked for. The replicas need a full copy.
var ErrGap = errors.New("wal capture: gap (wal reset or removed without a capture-driven checkpoint)")

type walHeader struct {
	magic      uint32
	version    uint32
	pageSize   uint32
	ckptSeq    uint32
	salt1      uint32
	salt2      uint32
	cksum1     uint32
	cksum2     uint32
	bigEndCsum bool
}

func parseWALHeader(b []byte) (walHeader, bool) {
	if len(b) < walHeaderSize {
		return walHeader{}, false
	}
	be := binary.BigEndian
	h := walHeader{
		magic: be.Uint32(b[0:]), version: be.Uint32(b[4:]), pageSize: be.Uint32(b[8:]),
		ckptSeq: be.Uint32(b[12:]), salt1: be.Uint32(b[16:]), salt2: be.Uint32(b[20:]),
		cksum1: be.Uint32(b[24:]), cksum2: be.Uint32(b[28:]),
	}
	switch h.magic {
	case walMagicLE:
	case walMagicBE:
		h.bigEndCsum = true
	default:
		return walHeader{}, false
	}
	if h.pageSize == 1 { // 65536 is stored as 1
		h.pageSize = 65536
	}
	if h.pageSize < 512 || h.pageSize > 65536 || h.pageSize&(h.pageSize-1) != 0 {
		return walHeader{}, false
	}
	s1, s2 := walChecksum(h.bigEndCsum, b[:24], 0, 0)
	if s1 != h.cksum1 || s2 != h.cksum2 {
		return walHeader{}, false
	}
	return h, true
}

// walChecksum is walChecksumBytes of SQLite: Fibonacci-weighted sums over
// pairs of 32-bit words, in the byte order the magic number chose.
func walChecksum(bigEnd bool, b []byte, s1, s2 uint32) (uint32, uint32) {
	var order binary.ByteOrder = binary.LittleEndian
	if bigEnd {
		order = binary.BigEndian
	}
	for i := 0; i+8 <= len(b); i += 8 {
		s1 += order.Uint32(b[i:]) + s2
		s2 += order.Uint32(b[i+4:]) + s1
	}
	return s1, s2
}

// Position is the state of the capture, small enough to persist after each
// shipped record so that a restarted engine continues where it stopped.
type Position struct {
	Known       bool   // a WAL generation has been read
	Salt1       uint32 // salts of the WAL generation being read
	Salt2       uint32
	CkptSeq     uint32
	PageSize    uint32
	NextFrame   int64  // index (0-based) of the first frame not yet shipped
	Cksum1      uint32 // running checksum at the last commit frame shipped
	Cksum2      uint32
	LSN         uint64 // lsn of the last record shipped
	ExpectReset bool   // a capture-driven checkpoint or close happened
}

// Capturer reads committed transactions from the -wal file of one database.
type Capturer struct {
	WALPath string
	Pos     Position
	// Open opens the WAL for reading; nil means os.Open. On Windows the
	// spike also tries a handle with FILE_SHARE_DELETE (share_windows.go).
	Open func(string) (*os.File, error)

	// Stats of the last Capture call.
	LastFramesRead   int
	LastPartialTail  int // valid frames after the last commit frame (an open transaction)
	LastBytesScanned int64
}

// Record is one committed transaction: the last image of every page it wrote
// and the size of the database (in pages) after the commit.
type Record struct {
	LSN        uint64
	Salt1      uint32
	Salt2      uint32
	FirstFrame int64
	Frames     int
	PageSize   uint32
	DBSize     uint32
	Pages      map[uint32][]byte
}

// Capture returns the transactions committed since the last call, in
// order. Frames after the last commit frame (an open transaction, or one
// that will never commit) are left for the next call.
func (c *Capturer) Capture() ([]*Record, error) {
	c.LastFramesRead, c.LastPartialTail, c.LastBytesScanned = 0, 0, 0
	open := c.Open
	if open == nil {
		open = os.Open
	}
	f, err := open(c.WALPath)
	if errors.Is(err, os.ErrNotExist) {
		if c.Pos.Known && !c.Pos.ExpectReset {
			return nil, fmt.Errorf("%w: %s is gone", ErrGap, c.WALPath)
		}
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open wal: %w", err)
	}
	defer f.Close()

	hb := make([]byte, walHeaderSize)
	if _, err := io.ReadFull(f, hb); err != nil {
		return nil, nil // empty (truncated) WAL: nothing committed in it
	}
	h, ok := parseWALHeader(hb)
	if !ok {
		return nil, nil
	}
	if !c.Pos.Known || h.salt1 != c.Pos.Salt1 || h.salt2 != c.Pos.Salt2 {
		if c.Pos.Known && !c.Pos.ExpectReset {
			return nil, fmt.Errorf("%w: salts %08x/%08x -> %08x/%08x", ErrGap, c.Pos.Salt1, c.Pos.Salt2, h.salt1, h.salt2)
		}
		c.Pos = Position{Known: true, Salt1: h.salt1, Salt2: h.salt2, CkptSeq: h.ckptSeq,
			PageSize: h.pageSize, Cksum1: h.cksum1, Cksum2: h.cksum2, LSN: c.Pos.LSN}
	}
	ps := int64(c.Pos.PageSize)
	frameSize := frameHeaderSize + ps
	off := walHeaderSize + c.Pos.NextFrame*frameSize
	data, err := io.ReadAll(io.NewSectionReader(f, off, 1<<62))
	if err != nil {
		return nil, fmt.Errorf("read wal: %w", err)
	}
	c.LastBytesScanned = int64(len(data))

	var out []*Record
	s1, s2 := c.Pos.Cksum1, c.Pos.Cksum2
	pending := map[uint32][]byte{}
	pendingFrames := 0
	be := binary.BigEndian
	for i := int64(0); (i+1)*frameSize <= int64(len(data)); i++ {
		fr := data[i*frameSize : (i+1)*frameSize]
		pgno, dbSize := be.Uint32(fr[0:]), be.Uint32(fr[4:])
		if be.Uint32(fr[8:]) != c.Pos.Salt1 || be.Uint32(fr[12:]) != c.Pos.Salt2 {
			break // a frame of an older generation: end of the log
		}
		s1, s2 = walChecksum(h.bigEndCsum, fr[:8], s1, s2)
		s1, s2 = walChecksum(h.bigEndCsum, fr[frameHeaderSize:], s1, s2)
		if s1 != be.Uint32(fr[16:]) || s2 != be.Uint32(fr[20:]) {
			break // torn or stale frame: end of the valid log
		}
		c.LastFramesRead++
		pending[pgno] = fr[frameHeaderSize:]
		pendingFrames++
		if dbSize == 0 {
			continue
		}
		c.Pos.LSN++
		rec := &Record{LSN: c.Pos.LSN, Salt1: c.Pos.Salt1, Salt2: c.Pos.Salt2,
			FirstFrame: c.Pos.NextFrame, Frames: pendingFrames, PageSize: c.Pos.PageSize,
			DBSize: dbSize, Pages: pending}
		out = append(out, rec)
		c.Pos.NextFrame += int64(pendingFrames)
		c.Pos.Cksum1, c.Pos.Cksum2 = s1, s2
		pending = map[uint32][]byte{}
		pendingFrames = 0
	}
	c.LastPartialTail = pendingFrames
	if len(out) > 0 {
		c.Pos.ExpectReset = false
	}
	return out, nil
}
