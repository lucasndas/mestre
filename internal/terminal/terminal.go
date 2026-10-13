package terminal

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/terminal/ptywire"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Defaults of the engine side of a terminal.
const (
	// DefaultAckDelay is how long the engine lets output pile up before it
	// confirms to the mestre-pty what the log holds; it is the same pause
	// for a trickle and for a flood, so the ack costs a message per pause.
	DefaultAckDelay = 200 * time.Millisecond
)

// Terminal is the engine's handle on one terminal: the connection to its
// mestre-pty, and the log written from what the mestre-pty sends. Losing the
// engine does not touch the terminal: Reconcile builds a new Terminal from
// the registry.
type Terminal struct {
	m      *Manager
	id     string
	rec    Record
	client *Client
	proc   ProcID // the mestre-pty
	logw   *LogWriter
	logf   io.Closer

	agentPID int

	mu         sync.Mutex
	next       uint64 // offset after the last byte given to the log
	acked      uint64
	logChanged chan struct{}
	ackKick    chan struct{}
	closed     chan struct{}
	closeOnce  sync.Once
	wg         sync.WaitGroup
}

func (m *Manager) newTerminal(rec Record, c *Client, proc ProcID, agentPID int) (*Terminal, error) {
	f, err := os.OpenFile(m.logPath(rec.ID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log of terminal %s: %w", rec.ID, err)
	}
	return &Terminal{
		m: m, id: rec.ID, rec: rec, client: c, proc: proc, agentPID: agentPID,
		logw: NewLogWriter(f, m.cfg.Masker), logf: f,
		logChanged: make(chan struct{}), ackKick: make(chan struct{}, 1), closed: make(chan struct{}),
	}, nil
}

// attach starts the log from the point the mestre-pty says the log reached
// and the goroutines that keep it going.
func (t *Terminal) attach(ctx context.Context) error {
	st, err := t.client.Status(ctx)
	if err != nil {
		return fmt.Errorf("status of terminal %s: %w", t.id, err)
	}
	if st.AgentPID != 0 {
		t.agentPID = st.AgentPID
	}
	t.mu.Lock()
	t.next, t.acked = st.Acked, st.Acked
	t.mu.Unlock()
	t.client.SetEventHandler(t.onEvent)
	if _, err := t.client.Follow(ctx, st.Acked, t.onFrame); err != nil {
		return fmt.Errorf("follow terminal %s: %w", t.id, err)
	}
	t.wg.Add(2)
	go t.ackLoop()
	go t.supervise()
	return nil
}

// onFrame writes the log. It runs in the reading goroutine of the client.
// Bytes before t.next were already written (a reconnection starts from the
// last ack, which may be behind the log) and are skipped; a jump forward is
// a chunk that left the mestre-pty's buffer before it could be read.
func (t *Terminal) onFrame(f ptywire.Frame) {
	t.mu.Lock()
	data := f.Data
	switch {
	case f.Offset > t.next:
		t.logw.WriteLost(f.Offset - t.next)
	case f.Offset < t.next:
		skip := t.next - f.Offset
		if skip >= uint64(len(data)) {
			t.mu.Unlock()
			return
		}
		data = data[skip:]
	}
	_, _ = t.logw.Write(data)
	t.next = f.Offset + uint64(len(f.Data))
	close(t.logChanged)
	t.logChanged = make(chan struct{})
	t.mu.Unlock()
	t.kickAck()
}

func (t *Terminal) kickAck() {
	select {
	case t.ackKick <- struct{}{}:
	default:
	}
}

// ackLoop confirms the log to the mestre-pty, after a short pause so that a
// burst of output costs one message.
func (t *Terminal) ackLoop() {
	defer t.wg.Done()
	ctx, cancel := t.context()
	defer cancel()
	for {
		select {
		case <-t.ackKick:
		case <-t.closed:
			return
		}
		if clock.Sleep(ctx, t.m.clk, t.m.cfg.AckDelay) != nil {
			return
		}
		t.ackNow(ctx)
	}
}

// ackNow confirms everything written to the log so far.
func (t *Terminal) ackNow(ctx context.Context) {
	t.mu.Lock()
	off := t.next
	if off <= t.acked {
		t.mu.Unlock()
		return
	}
	t.acked = off
	t.mu.Unlock()
	if err := t.client.Ack(ctx, off); err != nil && ctx.Err() == nil {
		t.m.log.Warn("ack to the mestre-pty failed", "terminal", t.id, "error", err)
	}
}

// context returns a context that ends when the terminal is released.
func (t *Terminal) context() (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		select {
		case <-t.closed:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx, cancel
}

func (t *Terminal) onEvent(m ptywire.Msg) {
	ev := Event{Terminal: t.id, Offset: m.Offset}
	switch m.T {
	case ptywire.TypeStable:
		ev.Kind = EventStable
	case ptywire.TypeExit:
		ev.Kind = EventExit
		ev.Exit = t.client.Exit()
	default:
		return
	}
	if f := t.m.cfg.OnEvent; f != nil {
		f(ev)
	}
}

// supervise watches the connection: when the agent exits it finishes the
// log and confirms it, which lets the mestre-pty go; when the connection
// drops with the agent alive, it reports the loss.
func (t *Terminal) supervise() {
	defer t.wg.Done()
	ctx, cancel := t.context()
	defer cancel()
	select {
	case <-t.client.Exited():
		t.finishLog(ctx)
	case <-t.client.Done():
		select {
		case <-t.closed:
		default:
			t.m.connectionLost(t)
		}
	case <-t.closed:
	}
}

// finishLog waits for the log to hold the whole output, writes the last
// unfinished line and confirms everything: the mestre-pty of an agent that
// ended exits once it has that confirmation.
func (t *Terminal) finishLog(ctx context.Context) {
	if err := t.WaitLogged(ctx, t.client.Exit().End); err != nil {
		return
	}
	t.logw.Flush()
	t.ackNow(ctx)
}

// WaitLogged blocks until the log holds the output up to offset off.
func (t *Terminal) WaitLogged(ctx context.Context, off uint64) error {
	for {
		t.mu.Lock()
		done, ch := t.next >= off, t.logChanged
		t.mu.Unlock()
		if done {
			return nil
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return ctx.Err()
		case <-t.client.Done():
			return t.client.Err()
		}
	}
}

// FlushLog waits until the log holds everything the terminal printed so
// far and writes the unfinished last line.
func (t *Terminal) FlushLog(ctx context.Context) error {
	st, err := t.client.Status(ctx)
	if err != nil {
		return err
	}
	if err := t.WaitLogged(ctx, st.End); err != nil {
		return err
	}
	t.logw.Flush()
	t.ackNow(ctx)
	return t.logw.Err()
}

// ID is the terminal id.
func (t *Terminal) ID() string { return t.id }

// Record is the registry row as of this connection.
func (t *Terminal) Record() Record { return t.rec }

// PTY is the mestre-pty process of the terminal.
func (t *Terminal) PTY() ProcID { return t.proc }

// AgentPID is the PID of the agent (the root process of the terminal).
func (t *Terminal) AgentPID() int { return t.agentPID }

// PTYVersion is the version of the mestre-pty the terminal was born with.
func (t *Terminal) PTYVersion() string { return t.client.Info().Version }

// Client is the connection to the mestre-pty.
func (t *Terminal) Client() *Client { return t.client }

// Screen is terminal.screen: the text of the screen (and, with lines > 0,
// of the history).
type Screen struct {
	Text    string
	History string
	Col     int
	Row     int
	Offset  uint64
}

// Screen asks the mestre-pty for the screen.
func (t *Terminal) Screen(ctx context.Context, lines int) (Screen, error) {
	m, err := t.client.Screen(ctx, lines)
	if err != nil {
		return Screen{}, err
	}
	return Screen{Text: m.Text, History: m.History, Col: m.CurCol, Row: m.CurRow, Offset: m.Offset}, nil
}

// Input types into the terminal. Who may type is decided above this layer.
func (t *Terminal) Input(ctx context.Context, data []byte) error { return t.client.Input(ctx, data) }

// Replay is terminal.replay: the output from offset from on.
func (t *Terminal) Replay(ctx context.Context, from uint64) (Replayed, error) {
	return t.client.Replay(ctx, from)
}

// Status is the state the mestre-pty reports.
func (t *Terminal) Status(ctx context.Context) (ptywire.Msg, error) { return t.client.Status(ctx) }

// Ping checks the mestre-pty answers; ErrUnsupported for one older than
// protocol 2.
func (t *Terminal) Ping(ctx context.Context) error {
	_, err := t.client.Ping(ctx)
	if err == nil {
		_ = t.m.reg.touch(ctx, t.id, t.m.clk.Now())
	}
	return err
}

// Exited is closed when the agent ended.
func (t *Terminal) Exited() <-chan struct{} { return t.client.Exited() }

// Detach lets go of the terminal without touching it: the connection and
// the log file close, and the mestre-pty keeps running. It is what the
// engine does when it shuts down.
func (t *Terminal) Detach() {
	t.closeOnce.Do(func() {
		close(t.closed)
		_ = t.client.Close()
		t.wg.Wait()
		t.logw.Flush()
		_ = t.logf.Close()
	})
}
