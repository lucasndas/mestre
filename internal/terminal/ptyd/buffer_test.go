package ptyd_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/mestre-ia/mestre/internal/terminal/ptyd"
)

// pattern returns n bytes whose value depends on their offset, so a replay
// can be checked byte by byte.
func pattern(from, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + (from+i)%26)
	}
	return b
}

func TestBufferReplayBeforeTrimming(t *testing.T) {
	t.Parallel()
	b := ptyd.NewBuffer(16, 0)
	_, _ = b.Write([]byte("hello "))
	_, _ = b.Write([]byte("world"))

	c := b.Replay(6, -1)

	if c.Offset != 6 || string(c.Data) != "world" || c.Lost != 0 {
		t.Fatalf("Replay(6) = %+v, want offset 6, data %q, nothing lost", c, "world")
	}
	if b.End() != 11 || b.Start() != 0 {
		t.Fatalf("Start, End = %d, %d, want 0, 11", b.Start(), b.End())
	}
}

// What the engine has not acked stays beyond the ring; what it acked leaves
// when the ring is full.
func TestBufferKeepsTheRingAndWhatIsNotAcked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		ring, cap  int
		writes     []int // sizes written, in order
		ack        uint64
		from       uint64
		offset     uint64
		lost       uint64
		wantLength int
	}{
		{"acked: only the ring stays", 16, 64, []int{10, 10}, 20, 0, 4, 4, 16},
		{"acked: asking from inside the kept part", 16, 64, []int{10, 10}, 20, 9, 9, 0, 11},
		{"acked: one write bigger than the ring", 16, 64, []int{50}, 50, 0, 34, 34, 16},
		{"not acked: everything stays", 16, 64, []int{10, 10, 10}, 0, 0, 0, 0, 30},
		{"acked halfway: from the ack or the ring, whichever is older", 16, 64, []int{40}, 10, 0, 10, 10, 30},
		{"not acked beyond the cap: the cap wins", 16, 32, []int{100}, 0, 0, 68, 68, 32},
		{"exactly full", 16, 64, []int{16}, 16, 0, 0, 0, 16},
		{"asking from the future", 16, 64, []int{5}, 5, 99, 5, 0, 0},
		{"across several blocks", 200_000, 400_000, []int{150_000, 150_000}, 300_000, 5, 100_000, 99_995, 200_000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			b := ptyd.NewBuffer(tt.ring, tt.cap)
			total := 0
			for _, n := range tt.writes {
				_, _ = b.Write(pattern(total, n))
				total += n
			}
			b.Ack(tt.ack)

			c := b.Replay(tt.from, -1)

			if c.Offset != tt.offset || c.Lost != tt.lost || len(c.Data) != tt.wantLength {
				t.Fatalf("Replay(%d) = offset %d, lost %d, %d bytes; want %d, %d, %d",
					tt.from, c.Offset, c.Lost, len(c.Data), tt.offset, tt.lost, tt.wantLength)
			}
			if want := pattern(int(c.Offset), tt.wantLength); !bytes.Equal(c.Data, want) {
				t.Fatalf("data differs from the pattern at offset %d", c.Offset)
			}
		})
	}
}

func TestBufferNextWaitsForDataThenEndsAfterClose(t *testing.T) {
	t.Parallel()
	b := ptyd.NewBuffer(16, 0)
	got := make(chan ptyd.Chunk)
	go func() {
		c, err := b.Next(context.Background(), 0, -1)
		if err != nil {
			close(got)
			return
		}
		got <- c
	}()

	_, _ = b.Write([]byte("abc"))

	c, ok := <-got
	if !ok || string(c.Data) != "abc" {
		t.Fatalf("Next = %q, %v, want %q", c.Data, ok, "abc")
	}
	_, _ = b.Write([]byte("de"))
	b.Close()
	c, err := b.Next(context.Background(), 3, -1)
	if err != nil || string(c.Data) != "de" {
		t.Fatalf("Next after Close = %q, %v, want the rest %q", c.Data, err, "de")
	}
	if _, err := b.Next(context.Background(), 5, -1); !errors.Is(err, io.EOF) {
		t.Fatalf("Next at the end of a closed buffer = %v, want io.EOF", err)
	}
}

func TestBufferNextStopsWhenTheContextEnds(t *testing.T) {
	t.Parallel()
	b := ptyd.NewBuffer(16, 0)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := b.Next(ctx, 0, -1); !errors.Is(err, context.Canceled) {
		t.Fatalf("Next = %v, want context.Canceled", err)
	}
}

func TestBufferNextHonoursMax(t *testing.T) {
	t.Parallel()
	b := ptyd.NewBuffer(16, 0)
	_, _ = b.Write([]byte("abcdef"))

	c, err := b.Next(t.Context(), 1, 3)

	if err != nil || string(c.Data) != "bcd" || c.Offset != 1 {
		t.Fatalf("Next = %+v, %v, want %q at offset 1", c, err, "bcd")
	}
}

func TestBufferAckNeverGoesBackOrPastTheEnd(t *testing.T) {
	t.Parallel()
	b := ptyd.NewBuffer(16, 0)
	_, _ = b.Write(pattern(0, 10))

	b.Ack(99)
	b.Ack(3)

	if got := b.Acked(); got != 10 {
		t.Fatalf("Acked = %d, want 10", got)
	}
}
