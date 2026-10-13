package ptyd

import (
	"context"
	"io"
	"sort"
	"sync"
)

// Retention defaults (docs/arquitetura/terminal.md#buffer-painel-e-log).
const (
	// DefaultBufferSize is the ring buffer of each terminal: the last 1 MiB
	// is always kept, for the panel and for the attach.
	DefaultBufferSize = 1 << 20
	// DefaultUnackedMax is how much output the engine may leave
	// unconfirmed: bytes it has not acked are kept beyond the ring up to
	// this size (64 MiB).
	DefaultUnackedMax = 64 << 20

	blockSize = 64 << 10
)

// Chunk is a run of raw output bytes with the offset of the first one.
// Lost is the number of bytes between the offset the reader asked for and
// Offset that already left the buffer: the "lost chunk" mark that comes
// before the data.
type Chunk struct {
	Offset uint64
	Data   []byte
	Lost   uint64
}

type block struct {
	off  uint64
	data []byte
}

func (b *block) end() uint64 { return b.off + uint64(len(b.data)) }

// Buffer holds the raw output of a terminal, each byte with its absolute
// offset (the number of bytes written before it). It always keeps the last
// ringSize bytes and, besides them, every byte the engine has not
// acknowledged, up to unackedMax before the end. Readers follow it by
// offset, so a slow reader never slows the writer: it only loses what was
// trimmed.
type Buffer struct {
	ringSize, unackedMax uint64

	mu     sync.Mutex
	blocks []*block
	first  uint64 // offset of the oldest byte kept
	end    uint64 // offset of the next byte to be written
	acked  uint64
	closed bool
	wake   chan struct{} // closed and replaced on every write and on Close
}

// NewBuffer returns a buffer keeping the last ringSize bytes and unacked
// bytes up to unackedMax (zero means the defaults).
func NewBuffer(ringSize, unackedMax int) *Buffer {
	if ringSize <= 0 {
		ringSize = DefaultBufferSize
	}
	if unackedMax < ringSize {
		unackedMax = max(DefaultUnackedMax, ringSize)
	}
	return &Buffer{ringSize: uint64(ringSize), unackedMax: uint64(unackedMax), wake: make(chan struct{})}
}

// Write appends p. It never blocks and never fails.
func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if b.closed || n == 0 {
		return n, nil
	}
	for len(p) > 0 {
		var last *block
		if k := len(b.blocks); k > 0 {
			last = b.blocks[k-1]
		}
		if last == nil || len(last.data) == blockSize {
			last = &block{off: b.end, data: make([]byte, 0, blockSize)}
			b.blocks = append(b.blocks, last)
		}
		c := min(len(p), blockSize-len(last.data))
		last.data = append(last.data, p[:c]...)
		b.end += uint64(c)
		p = p[c:]
	}
	b.trimLocked()
	b.signalLocked()
	return n, nil
}

// trimLocked drops what no rule asks to keep: the oldest byte kept is the
// smaller of "the last ringSize bytes" and "the unacked bytes, at most
// unackedMax".
func (b *Buffer) trimLocked() {
	ringKeep := uint64(0)
	if b.end > b.ringSize {
		ringKeep = b.end - b.ringSize
	}
	ackKeep := b.acked
	if b.end-ackKeep > b.unackedMax {
		ackKeep = b.end - b.unackedMax
	}
	if keep := min(ringKeep, ackKeep); keep > b.first {
		b.first = keep
	}
	i := 0
	for i < len(b.blocks) && b.blocks[i].end() <= b.first {
		b.blocks[i] = nil
		i++
	}
	b.blocks = b.blocks[i:]
}

// Ack records that the engine's log holds everything before offset.
func (b *Buffer) Ack(offset uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	offset = min(offset, b.end)
	if offset > b.acked {
		b.acked = offset
		b.trimLocked()
	}
}

// Acked returns the offset up to which the engine confirmed the log.
func (b *Buffer) Acked() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.acked
}

// Close ends the stream: readers get the remaining bytes, then io.EOF.
func (b *Buffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.closed {
		b.closed = true
		b.signalLocked()
	}
}

func (b *Buffer) signalLocked() {
	close(b.wake)
	b.wake = make(chan struct{})
}

// End returns the offset after the last byte written (the total written).
func (b *Buffer) End() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.end
}

// Start returns the offset of the oldest byte still kept.
func (b *Buffer) Start() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.first
}

// Position tells where a read from offset from would begin: the first byte
// kept at or after from, and how many bytes before it are lost.
func (b *Buffer) Position(from uint64) (offset, lost uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.positionLocked(from)
}

func (b *Buffer) positionLocked(from uint64) (offset, lost uint64) {
	offset = from
	if from < b.first {
		lost = b.first - from
		offset = b.first
	}
	return min(offset, b.end), lost
}

// Replay returns the bytes from offset from on (terminal.replay), at most
// max of them (all when max < 0). If part of it already left the buffer,
// Lost says how much and the data starts at the oldest byte kept. A from
// beyond the end returns an empty chunk at End.
func (b *Buffer) Replay(from uint64, max int) Chunk {
	c, _, _ := b.read(from, max)
	return c
}

// read copies up to max bytes (all when max < 0) starting at from. It also
// returns the channel that closes on the next write or Close, and whether
// the buffer is closed, sampled under the same lock as the data.
func (b *Buffer) read(from uint64, max int) (Chunk, <-chan struct{}, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	off, lost := b.positionLocked(from)
	c := Chunk{Offset: off, Lost: lost}
	n := b.end - off
	if max >= 0 && n > uint64(max) {
		n = uint64(max)
	}
	if n == 0 {
		return c, b.wake, b.closed
	}
	c.Data = make([]byte, 0, n)
	i := sort.Search(len(b.blocks), func(i int) bool { return b.blocks[i].end() > off })
	for ; i < len(b.blocks) && uint64(len(c.Data)) < n; i++ {
		blk := b.blocks[i]
		skip := uint64(0)
		if off > blk.off {
			skip = off - blk.off
		}
		take := min(uint64(len(blk.data))-skip, n-uint64(len(c.Data)))
		c.Data = append(c.Data, blk.data[skip:skip+take]...)
		off += take
	}
	return c, b.wake, b.closed
}

// Next blocks until there is data at or after from, then returns up to max
// bytes (all when max < 0). It returns io.EOF once the buffer is closed and
// drained, and ctx.Err() when ctx ends first.
func (b *Buffer) Next(ctx context.Context, from uint64, max int) (Chunk, error) {
	for {
		c, wake, closed := b.read(from, max)
		if len(c.Data) > 0 {
			return c, nil
		}
		if closed {
			return c, io.EOF
		}
		select {
		case <-wake:
		case <-ctx.Done():
			return Chunk{}, ctx.Err()
		}
	}
}
