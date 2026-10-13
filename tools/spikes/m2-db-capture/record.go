//go:build spike

package dbcapture

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sort"
)

// Encode is the wire form of a record (a binary frame 0x03 payload):
// lsn, salts, first frame, frame count, page size, db size, page count, then
// (pgno, page) pairs in page order.
func (r *Record) Encode() []byte {
	var b bytes.Buffer
	w := func(v any) { _ = binary.Write(&b, binary.BigEndian, v) }
	w(r.LSN)
	w(r.Salt1)
	w(r.Salt2)
	w(r.FirstFrame)
	w(uint32(r.Frames))
	w(r.PageSize)
	w(r.DBSize)
	w(uint32(len(r.Pages)))
	pgnos := make([]uint32, 0, len(r.Pages))
	for pg := range r.Pages {
		pgnos = append(pgnos, pg)
	}
	sort.Slice(pgnos, func(i, j int) bool { return pgnos[i] < pgnos[j] })
	for _, pg := range pgnos {
		w(pg)
		b.Write(r.Pages[pg])
	}
	return b.Bytes()
}

// DecodeRecord is the inverse of Encode.
func DecodeRecord(p []byte) (*Record, error) {
	rd := bytes.NewReader(p)
	r := &Record{Pages: map[uint32][]byte{}}
	var frames, n uint32
	for _, v := range []any{&r.LSN, &r.Salt1, &r.Salt2, &r.FirstFrame, &frames, &r.PageSize, &r.DBSize, &n} {
		if err := binary.Read(rd, binary.BigEndian, v); err != nil {
			return nil, fmt.Errorf("decode record: %w", err)
		}
	}
	r.Frames = int(frames)
	for range n {
		var pg uint32
		if err := binary.Read(rd, binary.BigEndian, &pg); err != nil {
			return nil, err
		}
		page := make([]byte, r.PageSize)
		if _, err := io.ReadFull(rd, page); err != nil {
			return nil, err
		}
		r.Pages[pg] = page
	}
	if rd.Len() != 0 {
		return nil, errors.New("decode record: trailing bytes")
	}
	return r, nil
}

// Deflated is the size of p compressed with DEFLATE (level 1, the cheap
// one): what the wire would carry with compression.
func Deflated(p []byte) int {
	var b bytes.Buffer
	w, _ := flate.NewWriter(&b, flate.BestSpeed)
	w.Write(p)
	w.Close()
	return b.Len()
}
