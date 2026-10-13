// Package memio is the in-memory byte pipe under the fake PTY and the fake
// network. Blocking uses sync.Cond, which testing/synctest treats as durably
// blocking, and every deadline or latency goes through the injected clock.
package memio

import (
	"errors"
	"io"
	"os"
	"sync"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// ErrClosedPipe is returned when using a side that was closed locally.
var ErrClosedPipe = errors.New("memio: use of closed pipe")

type chunk struct {
	data []byte
	at   time.Time // readable from this instant on (latency)
}

// Pipe is one direction of a byte stream: writes on one side, reads on the
// other. It is safe for concurrent use.
type Pipe struct {
	clk  clock.Clock
	mu   sync.Mutex
	cond *sync.Cond

	chunks    []chunk
	written   []byte // transcript of everything accepted
	eof       bool   // writer closed: reads drain, then io.EOF
	brokenErr error  // both sides fail with this error
	readerOff bool   // reader closed
	blackhole bool   // writes succeed and are dropped (half-open)
	latency   time.Duration
	deadline  time.Time
	keep      bool // keep the transcript
}

// New returns an empty pipe. When keepTranscript is set, Written returns a
// copy of every byte written so far.
func New(clk clock.Clock, keepTranscript bool) *Pipe {
	p := &Pipe{clk: clk, keep: keepTranscript}
	p.cond = sync.NewCond(&p.mu)
	return p
}

// Write appends b. It never blocks.
func (p *Pipe) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.brokenErr != nil:
		return 0, p.brokenErr
	case p.eof:
		return 0, ErrClosedPipe
	case p.blackhole:
		// Over a half-open link the writer learns nothing, not even that
		// the reader closed.
		return len(b), nil
	case p.readerOff:
		return 0, io.ErrClosedPipe
	}
	c := chunk{data: append([]byte(nil), b...), at: p.clk.Now().Add(p.latency)}
	p.chunks = append(p.chunks, c)
	if p.keep {
		p.written = append(p.written, b...)
	}
	if p.latency > 0 {
		p.clk.AfterFunc(p.latency, p.wake)
	}
	p.cond.Broadcast()
	return len(b), nil
}

// Read blocks until data is readable, the writer closed, the pipe broke or
// the read deadline passed.
func (p *Pipe) Read(b []byte) (int, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for {
		switch {
		case p.brokenErr != nil:
			return 0, p.brokenErr
		case p.readerOff:
			return 0, ErrClosedPipe
		}
		if len(p.chunks) > 0 && !p.chunks[0].at.After(p.clk.Now()) {
			n := copy(b, p.chunks[0].data)
			p.chunks[0].data = p.chunks[0].data[n:]
			if len(p.chunks[0].data) == 0 {
				p.chunks = p.chunks[1:]
			}
			return n, nil
		}
		if len(p.chunks) == 0 && p.eof {
			return 0, io.EOF
		}
		if !p.deadline.IsZero() && !p.clk.Now().Before(p.deadline) {
			return 0, os.ErrDeadlineExceeded
		}
		p.cond.Wait()
	}
}

func (p *Pipe) wake() {
	p.mu.Lock()
	p.cond.Broadcast()
	p.mu.Unlock()
}

// CloseWrite ends the stream: the reader gets io.EOF after the data. With
// the blackhole on, the end of stream is lost like the data (half-open).
func (p *Pipe) CloseWrite() {
	p.mu.Lock()
	if p.blackhole {
		p.mu.Unlock()
		return
	}
	p.eof = true
	p.cond.Broadcast()
	p.mu.Unlock()
}

// CloseRead closes the reading side; later writes fail.
func (p *Pipe) CloseRead() {
	p.mu.Lock()
	p.readerOff = true
	p.cond.Broadcast()
	p.mu.Unlock()
}

// Break makes both sides fail with err (a cut connection).
func (p *Pipe) Break(err error) {
	p.mu.Lock()
	if p.brokenErr == nil {
		p.brokenErr = err
	}
	p.cond.Broadcast()
	p.mu.Unlock()
}

// SetBlackhole makes later writes vanish without error (half-open link).
func (p *Pipe) SetBlackhole(on bool) {
	p.mu.Lock()
	p.blackhole = on
	p.mu.Unlock()
}

// SetLatency delays the data written from now on by d (clock time).
func (p *Pipe) SetLatency(d time.Duration) {
	p.mu.Lock()
	p.latency = d
	p.mu.Unlock()
}

// SetReadDeadline sets the clock instant after which Read fails with
// os.ErrDeadlineExceeded; the zero time clears it.
func (p *Pipe) SetReadDeadline(t time.Time) {
	p.mu.Lock()
	p.deadline = t
	p.cond.Broadcast()
	p.mu.Unlock()
	if !t.IsZero() {
		if d := t.Sub(p.clk.Now()); d > 0 {
			p.clk.AfterFunc(d, p.wake)
		}
	}
}

// Written returns a copy of the transcript (only with keepTranscript).
func (p *Pipe) Written() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]byte(nil), p.written...)
}
