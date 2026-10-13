// Package fakepty is the in-memory pseudo-terminal of the fast suite
// (docs/qualidade/dubles.md#outros-dublês). The terminal side implements
// terminal.PTY; the process side is what the fake agent reads and writes.
// Screen rendering arrives with the terminal emulator in M1; until then
// tests read the raw output transcript.
package fakepty

import (
	"sync"

	"github.com/lucasndas/mestre/internal/terminal"
	"github.com/lucasndas/mestre/internal/testkit/memio"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Pair is one fake PTY.
type Pair struct {
	in  *memio.Pipe // terminal -> process (keystrokes)
	out *memio.Pipe // process -> terminal (screen bytes)

	mu         sync.Mutex
	cols, rows uint16
}

// New returns a PTY of cols x rows.
func New(clk clock.Clock, cols, rows uint16) *Pair {
	return &Pair{in: memio.New(clk, true), out: memio.New(clk, true), cols: cols, rows: rows}
}

// Terminal returns the side used by the machine's engine (and by tests as the user).
func (p *Pair) Terminal() terminal.PTY { return termSide{p} }

// Process returns the side given to the process.
func (p *Pair) Process() *ProcessSide { return &ProcessSide{p} }

// Output returns everything the process printed so far.
func (p *Pair) Output() string { return string(p.out.Written()) }

// Input returns everything typed so far.
func (p *Pair) Input() string { return string(p.in.Written()) }

// Size returns the current size.
func (p *Pair) Size() (cols, rows uint16) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cols, p.rows
}

type termSide struct{ p *Pair }

func (t termSide) Read(b []byte) (int, error)  { return t.p.out.Read(b) }
func (t termSide) Write(b []byte) (int, error) { return t.p.in.Write(b) }

func (t termSide) Resize(cols, rows uint16) error {
	t.p.mu.Lock()
	t.p.cols, t.p.rows = cols, rows
	t.p.mu.Unlock()
	return nil
}

// Close hangs up: the process reads EOF.
func (t termSide) Close() error {
	t.p.in.CloseWrite()
	t.p.out.CloseRead()
	return nil
}

// ProcessSide is the process end of the PTY.
type ProcessSide struct{ p *Pair }

func (s *ProcessSide) Read(b []byte) (int, error)  { return s.p.in.Read(b) }
func (s *ProcessSide) Write(b []byte) (int, error) { return s.p.out.Write(b) }

// Size returns the terminal size the process sees.
func (s *ProcessSide) Size() (cols, rows uint16) { return s.p.Size() }

// Close is the process exiting: the terminal side reads EOF.
func (s *ProcessSide) Close() error {
	s.p.out.CloseWrite()
	return nil
}
