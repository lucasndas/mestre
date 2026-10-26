// Package termstream is the output of a terminal read from the buffer of its
// machine, by offset: the replay of the buffer and then the live output, as one
// ordered stream of chunks. The Tower runs it for `terminal.replay`, and the
// Citadel runs it for the terminals of its own machine
// (docs/arquitetura/terminal.md#buffer-painel-e-log). Nothing is stored here:
// the buffer is the one of the mestre-pty, and the stream only remembers how far
// it has read.
package termstream

import (
	"context"
	"fmt"

	"github.com/mestre-ia/mestre/internal/terminal"
)

// ChunkSize is the largest chunk handed to a Sink (a frame 0x01 carries one).
const ChunkSize = 32 << 10

// Source is the buffer of a machine.
type Source interface {
	// Replay is the output of the terminal from offset from on, at most one
	// answer of the mestre-pty (terminal.Client.Replay): Offset is where the data
	// really begins, later than from when a stretch left the buffer (Lost).
	Replay(ctx context.Context, terminalID string, from uint64) (terminal.Replayed, error)
	// WaitOutput blocks until the terminal printed something after offset after.
	WaitOutput(ctx context.Context, terminalID string, after uint64) error
}

// Sink receives the stream. A call may block (the Tower waits for room on the
// connection): the stream then waits too. An error ends the stream.
type Sink interface {
	// Chunk is data at byte offset off of the output; offsets only grow.
	Chunk(ctx context.Context, off uint64, data []byte) error
	// Replayed is told once, when the replay has caught up with the live
	// output: the bytes sent so far go from From to To, and Lost says that
	// before From some bytes already left the buffer.
	Replayed(ctx context.Context, from, to uint64, lost bool) error
}

// Run reads the output of the terminal from offset from on and hands it to the
// sink, first what the buffer holds and then each new byte as it is printed,
// until ctx ends, the sink fails or the terminal goes away (the error says it).
func Run(ctx context.Context, src Source, terminalID string, from uint64, out Sink) error {
	var (
		next      = from
		start     uint64
		lost      bool
		first     = true
		announced bool
	)
	for {
		r, err := src.Replay(ctx, terminalID, next)
		if err != nil {
			return fmt.Errorf("termstream: replay of %s from %d: %w", terminalID, next, err)
		}
		if first {
			start, lost, first = r.Offset, r.Lost > 0, false
		}
		for i := 0; i < len(r.Data); i += ChunkSize {
			end := min(i+ChunkSize, len(r.Data))
			if err := out.Chunk(ctx, r.Offset+uint64(i), r.Data[i:end]); err != nil {
				return err
			}
		}
		next = max(next, r.Offset+uint64(len(r.Data)))
		if len(r.Data) > 0 && next < r.End {
			continue // more of the buffer to read
		}
		if !announced {
			announced = true
			if err := out.Replayed(ctx, start, next, lost); err != nil {
				return err
			}
		}
		if len(r.Data) > 0 {
			continue // the answer may have ended before the output did: ask again before waiting
		}
		if err := src.WaitOutput(ctx, terminalID, next); err != nil {
			return fmt.Errorf("termstream: waiting for the output of %s after %d: %w", terminalID, next, err)
		}
	}
}
