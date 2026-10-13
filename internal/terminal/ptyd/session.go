// Package ptyd is the mestre-pty: the small process that holds one terminal
// (the PTY or ConPTY, the agent, the emulator and the buffer) and survives
// restarts of the engine (docs/arquitetura/terminal.md#mestre-pty). It only
// executes: the engine decides what to start, when, and what to type.
package ptyd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Defaults of the session.
const (
	// DefaultStableAfter is how long the screen must stay unchanged to count
	// as stable (docs/arquitetura/terminal.md#emulador-de-terminal).
	DefaultStableAfter = 500 * time.Millisecond
	// DefaultDrainAfterExit is how long the last output of an agent that
	// just exited may still arrive before the exit is announced.
	DefaultDrainAfterExit = 250 * time.Millisecond

	// feedBatch is the most bytes the emulator takes from the buffer in one
	// go; the screen is compared once per batch.
	feedBatch = 256 << 10
)

// Errors of the session.
var (
	ErrAlreadyStarted = errors.New("terminal already started")
	ErrNotRunning     = errors.New("terminal is not running")
)

// ExitInfo is how the agent ended.
type ExitInfo struct {
	Code   int
	Signal string // "" unless a signal ended it (Unix)
}

// StartSpec is the process to start, as the engine resolved it.
type StartSpec struct {
	Path       string
	Args       []string
	Env        []string // complete environment
	Dir        string
	Cols, Rows uint16
}

// Agent is the started process with the PTY it runs on. The OS-specific
// implementations are in runner_linux.go and runner_windows.go; the fast
// suite uses an in-memory one (internal/testkit/fakepty).
type Agent interface {
	// Read and Write are the master side of the PTY.
	io.ReadWriter
	// PID is the PID of the root process; on Linux it is also the session ID.
	PID() int
	// Wait blocks until the root process ended.
	Wait() ExitInfo
	// Signal asks the tree to end (SIGTERM to the session; ClosePseudoConsole).
	Signal() error
	// Kill ends the whole tree (every process of the session; the Job Object).
	Kill() error
	// Close releases the PTY.
	Close() error
}

// Runner starts the agent. ctx only governs the start.
type Runner interface {
	Start(ctx context.Context, spec StartSpec) (Agent, error)
}

// SessionConfig configures a Session.
type SessionConfig struct {
	Runner Runner
	Clock  clock.Clock
	// BufferSize (1 MiB) and UnackedMax (64 MiB): see Buffer.
	BufferSize, UnackedMax int
	HistoryLines           int // default 5,000
	StableAfter            time.Duration
	DrainAfterExit         time.Duration
	Emulator               EmulatorFactory // default NewVTEmulator
	// OnEvent receives the "stable" and "exit" events. It must not block.
	OnEvent func(ptywire.Msg)
	Logger  *slog.Logger
}

// Session is the one terminal of a mestre-pty: it reads the PTY into the
// buffer, feeds the emulator behind the buffer and tells when the screen is
// stable and when the agent ended. Output never waits for anyone: the
// emulator and every reader follow the buffer by offset.
type Session struct {
	cfg SessionConfig
	buf *Buffer
	clk clock.Clock

	mu         sync.Mutex
	state      string
	agent      Agent
	emu        Emulator
	cols, rows uint16
	exit       ExitInfo
	cancel     context.CancelFunc
	pumpDone   chan struct{}
	replies    chan []byte

	processed uint64
	progress  chan struct{} // closed and replaced when processed grows
	started   bool          // the screen changed at least once
	sig       string
	gen       uint64
	stable    bool
	timer     clock.Timer
}

// NewSession returns an idle session; Start starts the agent.
func NewSession(cfg SessionConfig) *Session {
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.HistoryLines == 0 {
		cfg.HistoryLines = DefaultHistoryLines
	}
	if cfg.StableAfter == 0 {
		cfg.StableAfter = DefaultStableAfter
	}
	if cfg.DrainAfterExit == 0 {
		cfg.DrainAfterExit = DefaultDrainAfterExit
	}
	if cfg.Emulator == nil {
		cfg.Emulator = NewVTEmulator
	}
	if cfg.OnEvent == nil {
		cfg.OnEvent = func(ptywire.Msg) {}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Session{
		cfg: cfg, clk: cfg.Clock, state: ptywire.StateIdle,
		buf:      NewBuffer(cfg.BufferSize, cfg.UnackedMax),
		progress: make(chan struct{}),
		replies:  make(chan []byte, 64),
		pumpDone: make(chan struct{}),
	}
}

// Buffer returns the buffer of raw output.
func (s *Session) Buffer() *Buffer { return s.buf }

// Start starts the agent. The size is fixed from now on: there is no way to
// resize a terminal afterwards.
func (s *Session) Start(ctx context.Context, spec StartSpec) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state != ptywire.StateIdle {
		return 0, ErrAlreadyStarted
	}
	if spec.Cols == 0 {
		spec.Cols = ptywire.DefaultCols
	}
	if spec.Rows == 0 {
		spec.Rows = ptywire.DefaultRows
	}
	agent, err := s.cfg.Runner.Start(ctx, spec)
	if err != nil {
		return 0, err
	}
	s.agent, s.cols, s.rows = agent, spec.Cols, spec.Rows
	s.emu = s.cfg.Emulator(int(spec.Cols), int(spec.Rows), s.cfg.HistoryLines, s.reply)
	s.state = ptywire.StateRunning
	runCtx, cancel := context.WithCancel(context.Background())
	s.cancel = cancel
	go s.pump(agent)
	go s.feed(runCtx)
	go s.answer(runCtx, agent)
	go s.supervise(agent)
	return agent.PID(), nil
}

// pump moves the PTY output into the buffer. It never waits for the
// emulator or for a reader.
func (s *Session) pump(agent Agent) {
	defer close(s.pumpDone)
	b := make([]byte, 32<<10)
	for {
		n, err := agent.Read(b)
		if n > 0 {
			_, _ = s.buf.Write(b[:n])
		}
		if err != nil {
			return
		}
	}
}

// supervise waits for the agent, lets its last output arrive, and
// announces the exit.
func (s *Session) supervise(agent Agent) {
	info := agent.Wait()
	t := s.clk.NewTimer(s.cfg.DrainAfterExit)
	select {
	case <-s.pumpDone:
	case <-t.C():
	}
	t.Stop()
	s.mu.Lock()
	s.state, s.exit = ptywire.StateExited, info
	s.mu.Unlock()
	code := info.Code
	s.cfg.OnEvent(ptywire.Msg{T: ptywire.TypeExit, ExitCode: &code, Signal: info.Signal, End: s.buf.End()})
	_ = agent.Close()
}

// feed runs the emulator behind the buffer. If it falls so far behind that
// the buffer dropped bytes, the screen is rebuilt from what the buffer
// still holds.
func (s *Session) feed(ctx context.Context) {
	var off uint64
	for {
		c, err := s.buf.Next(ctx, off, feedBatch)
		if err != nil {
			return
		}
		if c.Lost > 0 {
			s.emu.Reset()
		}
		s.emu.Write(c.Data)
		off = c.Offset + uint64(len(c.Data))
		s.fed(off)
	}
}

// fed records progress and restarts the stability timer when the screen
// (text or cursor) changed.
func (s *Session) fed(off uint64) {
	col, row := s.emu.Cursor()
	sig := fmt.Sprintf("%d,%d\n%s", col, row, s.emu.Screen())
	s.mu.Lock()
	defer s.mu.Unlock()
	s.processed = off
	close(s.progress)
	s.progress = make(chan struct{})
	if s.started && sig == s.sig {
		return
	}
	s.started, s.sig = true, sig
	s.gen++
	s.stable = false
	if s.timer != nil {
		s.timer.Stop()
	}
	gen := s.gen
	s.timer = s.clk.AfterFunc(s.cfg.StableAfter, func() { s.becameStable(gen) })
}

func (s *Session) becameStable(gen uint64) {
	s.mu.Lock()
	if gen != s.gen || s.stable {
		s.mu.Unlock()
		return
	}
	s.stable = true
	off := s.processed
	s.mu.Unlock()
	s.cfg.OnEvent(ptywire.Msg{T: ptywire.TypeStable, Offset: off})
}

// reply is the emulator's answer to a query of the process (cursor
// position, device attributes). It never blocks the emulator: answers are
// queued and dropped if the queue is full.
func (s *Session) reply(p []byte) {
	select {
	case s.replies <- p:
	default:
	}
}

func (s *Session) answer(ctx context.Context, agent Agent) {
	for {
		select {
		case p := <-s.replies:
			_, _ = agent.Write(p)
		case <-ctx.Done():
			return
		}
	}
}

// Input types into the terminal. Who may type is the engine's decision.
func (s *Session) Input(p []byte) error {
	s.mu.Lock()
	agent, state := s.agent, s.state
	s.mu.Unlock()
	if state != ptywire.StateRunning {
		return ErrNotRunning
	}
	_, err := agent.Write(p)
	return err
}

// Signal asks the agent tree to end.
func (s *Session) Signal() error {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	if agent == nil {
		return ErrNotRunning
	}
	return agent.Signal()
}

// Kill ends the whole tree of the agent.
func (s *Session) Kill() error {
	s.mu.Lock()
	agent := s.agent
	s.mu.Unlock()
	if agent == nil {
		return nil
	}
	return agent.Kill()
}

// State is a snapshot for the "status" message.
type State struct {
	State    string
	AgentPID int
	Exit     ExitInfo
	Stable   bool
	Cols     uint16
	Rows     uint16
}

// State returns a snapshot of the session.
func (s *Session) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := State{State: s.state, Exit: s.exit, Stable: s.stable && s.state == ptywire.StateRunning, Cols: s.cols, Rows: s.rows}
	if s.agent != nil {
		st.AgentPID = s.agent.PID()
	}
	return st
}

// Exited reports whether the agent ended and its exit was announced.
func (s *Session) Exited() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state == ptywire.StateExited
}

// Snapshot is the screen at some offset of the output.
type Snapshot struct {
	Text, History string
	CursorCol     int
	CursorRow     int
	Offset        uint64
}

// Screen returns the screen after the emulator consumed every byte written
// so far (or ctx ended). lines > 0 also returns that many lines of history.
func (s *Session) Screen(ctx context.Context, lines int) (Snapshot, error) {
	s.mu.Lock()
	emu := s.emu
	s.mu.Unlock()
	if emu == nil {
		return Snapshot{}, ErrNotRunning
	}
	if err := s.WaitProcessed(ctx, s.buf.End()); err != nil {
		return Snapshot{}, err
	}
	s.mu.Lock()
	off := s.processed
	s.mu.Unlock()
	col, row := emu.Cursor()
	snap := Snapshot{Text: emu.Screen(), CursorCol: col, CursorRow: row, Offset: off}
	if lines > 0 {
		snap.History = emu.History(lines)
	}
	return snap, nil
}

// WaitProcessed blocks until the emulator consumed the output up to offset
// off, so the screen reflects those bytes.
func (s *Session) WaitProcessed(ctx context.Context, off uint64) error {
	for {
		s.mu.Lock()
		done, ch := s.processed >= off, s.progress
		s.mu.Unlock()
		if done {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close stops the goroutines and releases the PTY and the emulator. It does
// not stop the process: that is Signal and Kill.
func (s *Session) Close() {
	s.mu.Lock()
	agent, cancel, emu := s.agent, s.cancel, s.emu
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if agent != nil {
		_ = agent.Close()
	}
	s.buf.Close()
	if emu != nil {
		emu.Close()
	}
}
