package termstream_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/termstream"
)

type buffer struct {
	mu    sync.Mutex
	start uint64
	data  []byte
	wait  chan struct{}
	err   error
}

func (b *buffer) Replay(_ context.Context, _ string, from uint64) (terminal.Replayed, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return terminal.Replayed{}, b.err
	}
	end := b.start + uint64(len(b.data))
	r := terminal.Replayed{Offset: from, End: end}
	if from < b.start {
		r.Offset, r.Lost = b.start, b.start-from
	}
	if r.Offset < end {
		r.Data = b.data[r.Offset-b.start:]
	}
	return r, nil
}

func (b *buffer) WaitOutput(ctx context.Context, _ string, after uint64) error {
	for {
		b.mu.Lock()
		more, ch := b.start+uint64(len(b.data)) > after, b.wait
		b.mu.Unlock()
		if more {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (b *buffer) print(p []byte) {
	b.mu.Lock()
	b.data = append(b.data, p...)
	close(b.wait)
	b.wait = make(chan struct{})
	b.mu.Unlock()
}

type got struct {
	mu       sync.Mutex
	chunks   [][2]uint64 // offset, length
	data     []byte
	replayed []uint64 // from, to
	lost     bool
	signal   chan struct{}
}

func (g *got) Chunk(_ context.Context, off uint64, d []byte) error {
	g.mu.Lock()
	g.chunks = append(g.chunks, [2]uint64{off, uint64(len(d))})
	g.data = append(g.data, d...)
	g.mu.Unlock()
	g.signal <- struct{}{}
	return nil
}

func (g *got) Replayed(_ context.Context, from, to uint64, lost bool) error {
	g.mu.Lock()
	g.replayed, g.lost = []uint64{from, to}, lost
	g.mu.Unlock()
	g.signal <- struct{}{}
	return nil
}

// The stream hands the buffer in chunks no larger than ChunkSize, says once that it caught
// up, with where it began and whether the start was lost, and then follows the output.
func TestTheStreamReadsTheBufferThenFollowsTheOutput(t *testing.T) {
	t.Parallel()
	body := bytes.Repeat([]byte("x"), 3*termstream.ChunkSize+5)
	b := &buffer{start: 100, data: bytes.Clone(body), wait: make(chan struct{})}
	g := &got{signal: make(chan struct{}, 64)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- termstream.Run(ctx, b, "t", 0, g) }()
	for range 5 { // four chunks and the replayed
		<-g.signal
	}
	g.mu.Lock()
	if len(g.chunks) != 4 || g.chunks[0] != [2]uint64{100, termstream.ChunkSize} || g.chunks[3][1] != 5 {
		t.Fatalf("chunks = %v", g.chunks)
	}
	if len(g.replayed) != 2 || g.replayed[0] != 100 || g.replayed[1] != 100+uint64(len(body)) || !g.lost {
		t.Fatalf("replayed = %v lost = %v", g.replayed, g.lost)
	}
	g.mu.Unlock()
	b.print([]byte("depois"))
	<-g.signal
	g.mu.Lock()
	if string(g.data[len(body):]) != "depois" || g.chunks[4][0] != 100+uint64(len(body)) {
		t.Fatalf("live = %q at %v", g.data[len(body):], g.chunks[4])
	}
	g.mu.Unlock()
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
}

// An empty terminal still announces the replay, and a terminal that is gone ends the stream
// with the error of the machine.
func TestAnEmptyTerminalAnnouncesAndAGoneOneEndsTheStream(t *testing.T) {
	t.Parallel()
	b := &buffer{wait: make(chan struct{})}
	g := &got{signal: make(chan struct{}, 8)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- termstream.Run(ctx, b, "t", 0, g) }()
	<-g.signal
	g.mu.Lock()
	if len(g.replayed) != 2 || g.replayed[1] != 0 || g.lost {
		t.Fatalf("replayed = %v lost = %v", g.replayed, g.lost)
	}
	g.mu.Unlock()
	gone := errors.New("the terminal is gone")
	b.mu.Lock()
	b.err = gone
	b.mu.Unlock()
	b.print([]byte("x"))
	if err := <-done; !errors.Is(err, gone) {
		t.Fatalf("Run = %v", err)
	}
	cancel()
}
