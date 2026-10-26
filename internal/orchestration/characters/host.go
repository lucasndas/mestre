package characters

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/terminal"
)

// HostSpec is what the host needs to create the terminal of a character: the
// command of the plan, already resolved by the adapter.
type HostSpec struct {
	// ID is the id of the terminal, CharacterID the character (MESTRE_AGENT_ID).
	ID, CharacterID   string
	Dir, Class        string
	ProviderSessionID string
	Path              string
	Args, Env         []string
	// WorkDir is the folder of the process.
	WorkDir string
}

// Screen is the rendered screen of a terminal with the row of the cursor.
type Screen struct {
	Text string
	Row  int
	// History is the lines that scrolled off, when they were asked for.
	History string
}

// Process is a terminal the engine holds: what the runtime does with the
// process of a character. terminal.Terminal is one (TerminalHost); the tests
// use a scripted one.
type Process interface {
	ID() string
	CharacterID() string
	// Input types into the terminal.
	Input(ctx context.Context, data []byte) error
	// Screen is the rendered screen.
	Screen(ctx context.Context) (Screen, error)
	// Stop ends the terminal in the three steps of the document: graceful is
	// typed first (nil skips the step) and waited for up to gracefulWait; then
	// the signal to the session, then the kill of the whole tree.
	Stop(ctx context.Context, graceful []byte, gracefulWait time.Duration) error
	// PTY is the mestre-pty of the terminal.
	PTY() PTYInfo
	// Exited is closed when the agent ended.
	Exited() <-chan struct{}
	// Detach lets go of the terminal without touching it: the engine is going
	// down and the mestre-pty keeps running.
	Detach()
}

// HostEventKind is what happened to a terminal.
type HostEventKind string

// The events.
const (
	// HostStable: the screen stopped changing.
	HostStable HostEventKind = "stable"
	// HostExit: the agent ended without being asked to.
	HostExit HostEventKind = "exit"
	// HostLost: the connection to the mestre-pty dropped; Dead says the process is gone.
	HostLost HostEventKind = "lost"
)

// HostEvent is something that happened to a terminal.
type HostEvent struct {
	Terminal string
	Kind     HostEventKind
	// Dead is set on HostLost when the process is gone.
	Dead     bool
	ExitCode int
}

// HostReport is what a host found when the engine came back.
type HostReport struct {
	// Reconnected are the terminals that were alive.
	Reconnected []Process
	// Interrupted are the ids of the terminals whose mestre-pty is dead.
	Interrupted []string
}

// Host creates and finds the terminals of the machine (terminal.Manager).
type Host interface {
	Create(ctx context.Context, spec HostSpec) (Process, error)
	// Reconcile reconnects to the terminals that survived the restart of the
	// engine and says which ones did not.
	Reconcile(ctx context.Context) (HostReport, error)
	// Events sets who is told of the events; the runtime sets it once.
	Events(func(HostEvent))
	// SetStatus keeps the state of the character in the terminal registry.
	SetStatus(ctx context.Context, terminalID, status string) error
}

// TerminalHost is the Host over a terminal.Manager. Build it with
// NewTerminalHost, which takes the callbacks of the manager.
type TerminalHost struct {
	mgr *terminal.Manager
	dir *localchan.Directory

	mu sync.Mutex
	on func(HostEvent)

	// The events of the terminals are handed over in order by a goroutine of
	// their own: the manager calls its callback in the goroutine that reads
	// the mestre-pty, which must not wait for the mestre-pty (the screen).
	qmu   sync.Mutex
	queue []HostEvent
	wake  chan struct{}
	done  chan struct{}
	once  sync.Once
}

// NewTerminalHost builds the manager from cfg, adding to the callbacks it
// already has the ones the host needs, and keeps dir (the directory of the
// local channel, nil for none) up to date.
func NewTerminalHost(cfg terminal.ManagerConfig, dir *localchan.Directory) (*TerminalHost, error) {
	h := &TerminalHost{dir: dir, wake: make(chan struct{}, 1), done: make(chan struct{})}
	prevEvent, prevStarting, prevDiscarded, prevEnded := cfg.OnEvent, cfg.OnStarting, cfg.OnDiscarded, cfg.OnEnded
	cfg.OnEvent = func(e terminal.Event) {
		if prevEvent != nil {
			prevEvent(e)
		}
		h.event(e)
	}
	cfg.OnStarting = func(s terminal.Starting) {
		if prevStarting != nil {
			prevStarting(s)
		}
		if dir != nil {
			dir.Starting(s)
		}
	}
	cfg.OnDiscarded = func(id string) {
		if prevDiscarded != nil {
			prevDiscarded(id)
		}
		if dir != nil {
			dir.Remove(id)
		}
	}
	cfg.OnEnded = func(id string) {
		if prevEnded != nil {
			prevEnded(id)
		}
		if dir != nil {
			dir.Remove(id)
		}
	}
	m, err := terminal.NewManager(cfg)
	if err != nil {
		return nil, err
	}
	h.mgr = m
	go h.deliver()
	return h, nil
}

// Close stops the delivery of the events; the terminals are not touched.
func (h *TerminalHost) Close() { h.once.Do(func() { close(h.done) }) }

// deliver hands the events to whoever asked, in the order they came.
func (h *TerminalHost) deliver() {
	for {
		select {
		case <-h.done:
			return
		case <-h.wake:
		}
		for {
			h.qmu.Lock()
			if len(h.queue) == 0 {
				h.qmu.Unlock()
				break
			}
			ev := h.queue[0]
			h.queue = h.queue[1:]
			h.qmu.Unlock()
			h.mu.Lock()
			on := h.on
			h.mu.Unlock()
			if on != nil {
				on(ev)
			}
		}
	}
}

// Manager is the terminal manager of the host.
func (h *TerminalHost) Manager() *terminal.Manager { return h.mgr }

func (h *TerminalHost) event(e terminal.Event) {
	ev := HostEvent{Terminal: e.Terminal}
	switch e.Kind {
	case terminal.EventStable:
		ev.Kind = HostStable
	case terminal.EventExit:
		ev.Kind, ev.ExitCode = HostExit, e.Exit.Code
	case terminal.EventLost:
		ev.Kind, ev.Dead = HostLost, e.Interrupted
	default:
		return
	}
	h.qmu.Lock()
	h.queue = append(h.queue, ev)
	h.qmu.Unlock()
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Events implements Host.
func (h *TerminalHost) Events(f func(HostEvent)) {
	h.mu.Lock()
	h.on = f
	h.mu.Unlock()
}

// Create implements Host.
func (h *TerminalHost) Create(ctx context.Context, spec HostSpec) (Process, error) {
	t, err := h.mgr.Create(ctx, terminal.CreateSpec{ID: spec.ID, CharacterID: spec.CharacterID, Dir: spec.Dir, Class: spec.Class,
		ProviderSessionID: spec.ProviderSessionID, Path: spec.Path, Args: spec.Args, Env: spec.Env, WorkDir: spec.WorkDir})
	if err != nil {
		return nil, err
	}
	return h.process(t), nil
}

// Reconcile implements Host.
func (h *TerminalHost) Reconcile(ctx context.Context) (HostReport, error) {
	rep, err := h.mgr.Reconcile(ctx)
	if rep == nil {
		return HostReport{}, err
	}
	out := HostReport{}
	for _, id := range rep.Interrupted {
		if !strings.HasPrefix(id, VerificationTerminalPrefix) {
			out.Interrupted = append(out.Interrupted, id)
		}
	}
	for _, t := range rep.Reconnected {
		if strings.HasPrefix(t.ID(), VerificationTerminalPrefix) {
			// a command of a verification that the fall of the engine left running
			// belongs to no character: it ends here, with its tree
			if _, serr := t.Stop(ctx, terminal.StopPlan{}); serr != nil {
				err = errors.Join(err, serr)
			}
			continue
		}
		if h.dir != nil {
			h.dir.Add(t)
		}
		out.Reconnected = append(out.Reconnected, h.process(t))
	}
	// what the verifications left in the registry goes with them
	if _, perr := h.mgr.Prune(ctx, VerificationTerminalPrefix); perr != nil {
		err = errors.Join(err, perr)
	}
	return out, err
}

// VerificationTerminalPrefix starts the id of the terminal of a command of the
// verification (no character): Reconcile ends and forgets the ones left by a
// fall of the engine.
const VerificationTerminalPrefix = "verifica-"

// SetStatus implements Host.
func (h *TerminalHost) SetStatus(ctx context.Context, terminalID, status string) error {
	return h.mgr.SetStatus(ctx, terminalID, status)
}

func (h *TerminalHost) process(t *terminal.Terminal) Process { return &terminalProcess{t: t, h: h} }

type terminalProcess struct {
	t *terminal.Terminal
	h *TerminalHost
}

func (p *terminalProcess) ID() string          { return p.t.ID() }
func (p *terminalProcess) CharacterID() string { return p.t.Record().CharacterID }
func (p *terminalProcess) Input(ctx context.Context, data []byte) error {
	return p.t.Input(ctx, data)
}
func (p *terminalProcess) Screen(ctx context.Context) (Screen, error) {
	s, err := p.t.Screen(ctx, 0)
	return Screen{Text: s.Text, Row: s.Row}, err
}

// ScreenLines is the screen with up to lines lines of the history.
func (p *terminalProcess) ScreenLines(ctx context.Context, lines int) (Screen, error) {
	s, err := p.t.Screen(ctx, lines)
	return Screen{Text: s.Text, Row: s.Row, History: s.History}, err
}
func (p *terminalProcess) Exited() <-chan struct{} { return p.t.Exited() }
func (p *terminalProcess) PTY() PTYInfo {
	id := p.t.PTY()
	return PTYInfo{PID: id.PID, Started: id.Start, Version: p.t.PTYVersion()}
}
func (p *terminalProcess) Detach() { p.t.Detach() }

func (p *terminalProcess) Stop(ctx context.Context, graceful []byte, wait time.Duration) error {
	plan := terminal.StopPlan{GracefulWait: wait}
	if len(graceful) > 0 {
		plan.Graceful = func(ctx context.Context, t *terminal.Terminal) error { return t.Input(ctx, graceful) }
	}
	_, err := p.t.Stop(ctx, plan)
	if p.h.dir != nil {
		p.h.dir.Remove(p.t.ID())
	}
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// ErrNoProcess: the character has no process on this machine.
var ErrNoProcess = errors.New("the character has no process on this machine")

func noProcess(id string) error { return fmt.Errorf("%w: %s", ErrNoProcess, id) }
