package memio_test

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/testkit/memio"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func TestReadDeadlineFollowsFakeClock(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC))
	p := memio.New(clk, false)
	p.SetReadDeadline(clk.Now().Add(time.Second))
	done := make(chan error, 1)
	go func() {
		_, err := p.Read(make([]byte, 1))
		done <- err
	}()
	clk.BlockUntil(1)
	clk.Advance(time.Second)
	if err := <-done; !errors.Is(err, os.ErrDeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
}

func TestBreakAndBlackhole(t *testing.T) {
	t.Parallel()
	p := memio.New(clock.Real{}, true)
	p.SetBlackhole(true)
	if n, err := p.Write([]byte("lost")); n != 4 || err != nil {
		t.Fatalf("write = %d, %v", n, err)
	}
	p.SetBlackhole(false)
	_, _ = p.Write([]byte("kept"))
	b := make([]byte, 8)
	if n, _ := p.Read(b); string(b[:n]) != "kept" || string(p.Written()) != "kept" {
		t.Fatalf("read %q, transcript %q", b[:n], p.Written())
	}
	p.Break(io.ErrUnexpectedEOF)
	if _, err := p.Read(b); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v", err)
	}
	if _, err := p.Write(b); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("write err = %v", err)
	}
}
