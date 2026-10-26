package characters

import (
	"context"
	"errors"
	"fmt"

	"github.com/mestre-ia/mestre/internal/terminal"
)

// Streamer is what a Process may also be: the output of its terminal read from
// the buffer of the mestre-pty by offset (terminal.replay), which is how the panel
// shows a terminal (docs/arquitetura/terminal.md#buffer-painel-e-log). A Process
// that is not one (the scripted ones of the tests) has no stream.
type Streamer interface {
	// Replay is the output from offset from on, at most one answer of the
	// mestre-pty (see terminal.Client.Replay).
	Replay(ctx context.Context, from uint64) (terminal.Replayed, error)
	// WaitOutput blocks until the terminal has printed something after offset
	// after, or ctx ends, or the terminal is gone.
	WaitOutput(ctx context.Context, after uint64) error
}

// ErrNoStream: the terminal does not run on this machine, or its process cannot
// be followed.
var ErrNoStream = errors.New("no output to follow for the terminal on this machine")

func (rt *Runtime) streamer(terminalID string) (Streamer, error) {
	rt.mu.Lock()
	s := rt.byTerm[terminalID]
	rt.mu.Unlock()
	if s == nil {
		return nil, fmt.Errorf("%w: %s", ErrNoStream, terminalID)
	}
	s.mu.Lock()
	p := s.proc
	s.mu.Unlock()
	st, ok := p.(Streamer)
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrNoStream, terminalID)
	}
	return st, nil
}

// Replay reads the output of a terminal of this machine from offset from on.
func (rt *Runtime) Replay(ctx context.Context, terminalID string, from uint64) (terminal.Replayed, error) {
	st, err := rt.streamer(terminalID)
	if err != nil {
		return terminal.Replayed{}, err
	}
	return st.Replay(ctx, from)
}

// WaitOutput waits for the terminal to print something after offset after.
func (rt *Runtime) WaitOutput(ctx context.Context, terminalID string, after uint64) error {
	st, err := rt.streamer(terminalID)
	if err != nil {
		return err
	}
	return st.WaitOutput(ctx, after)
}

// Type types what a person typed on the keyboard of the panel into the terminal
// of a character. Unlike Input it does not refuse a character that is busy: the
// panel asked the person to confirm that ("o agente está trabalhando"), and
// who may type at all was decided by the level before it got here
// (docs/painel/canvas.md#digitar-no-terminal). It never goes through the inbox.
func (rt *Runtime) Type(ctx context.Context, agentID string, data []byte) error {
	s := rt.session(agentID)
	if s == nil {
		return fmt.Errorf("characters: %s does not run on this machine", agentID)
	}
	st := s.status()
	s.mu.Lock()
	p := s.proc
	if st.State == Blocked {
		s.inputGiven = true
	}
	s.mu.Unlock()
	if p == nil {
		return noProcess(agentID)
	}
	return p.Input(ctx, data)
}
