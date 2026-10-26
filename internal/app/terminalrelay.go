package app

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/termstream"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// The Citadel side of the terminal in the browser: it answers the panel
// (panel.TerminalSource), asks the machine of the character for the replay of its
// buffer and relays what comes, and keeps no buffer, only how far each browser
// stream has read (docs/painel/canvas.md#terminais-no-canvas).

// relayWorld is what the relay needs of the Citadel around it.
type relayWorld interface {
	ByCharacter(ctx context.Context, id string) (characters.Character, error)
	ByTerminal(ctx context.Context, terminal string) (characters.Character, error)
	// Local is the id of this machine; LocalSource its buffers (nil when its
	// terminals are not wired).
	Local() string
	LocalSource() termstream.Source
	// Connected says whether the Tower of the machine is connected now.
	Connected(machine string) bool
	// SendTo sends a message to a connected Tower, on the open connection only.
	SendTo(ctx context.Context, machine, typ string, payload any) error
	// TypeKeys types what a person typed into the terminal of a character of
	// the machine, through the engine there; Screen is its screen.
	TypeKeys(ctx context.Context, machine, character, terminal string, data []byte) error
	Screen(ctx context.Context, machine, character string) (characters.Screen, error)
	Now() time.Time
	NewStream() protocol.ULID
}

// terminalRelay implements panel.TerminalSource.
type terminalRelay struct {
	w relayWorld

	mu      sync.Mutex
	streams map[protocol.ULID]*relayStream
	down    map[string]time.Time // the machines without signal, since when
}

var _ panel.TerminalSource = (*terminalRelay)(nil)

func newTerminalRelay(w relayWorld) *terminalRelay {
	return &terminalRelay{w: w, streams: map[protocol.ULID]*relayStream{}, down: map[string]time.Time{}}
}

// relayStream is one browser following a terminal of a Tower.
type relayStream struct {
	id       protocol.ULID
	terminal string
	machine  string
	sink     panel.TerminalSink

	mu   sync.Mutex
	next uint64 // the end of what was relayed: where a new request resumes
	dead bool
}

func (r *terminalRelay) info(c characters.Character) panel.TerminalInfo {
	local := c.Machine == r.w.Local()
	info := panel.TerminalInfo{
		TerminalID: c.TerminalID, Character: c.ID, Name: c.Name, Machine: c.Machine, Workspace: c.Workspace,
		State: string(c.State), Busy: busy(c), Signal: true,
	}
	if !local && !r.w.Connected(c.Machine) {
		r.mu.Lock()
		since, ok := r.down[c.Machine]
		r.mu.Unlock()
		if !ok {
			since = c.LastSignalAt
		}
		if since.IsZero() {
			since = r.w.Now()
		}
		since = since.UTC()
		info.Signal, info.NoSignalSince = false, &since
	}
	return info
}

// busy says the character is working: typing into it asks for a confirmation.
func busy(c characters.Character) bool {
	if c.Kind == characters.KindTerminal {
		return false
	}
	switch c.State {
	case characters.Starting, characters.Busy, characters.Blocked, characters.Stopping:
		return true
	}
	return false
}

func (r *terminalRelay) found(c characters.Character, err error) (characters.Character, error) {
	switch {
	case errors.Is(err, characters.ErrNoCharacter):
		return c, panel.ErrNoTerminal
	case err != nil:
		return c, err
	case c.TerminalID == "" || c.State == characters.Stopped:
		return c, panel.ErrNoTerminal
	}
	return c, nil
}

// ByCharacter implements panel.TerminalSource.
func (r *terminalRelay) ByCharacter(ctx context.Context, id string) (panel.TerminalInfo, error) {
	c, err := r.found(r.w.ByCharacter(ctx, id))
	if err != nil {
		return panel.TerminalInfo{}, err
	}
	return r.info(c), nil
}

// aliveWorld is a relayWorld that can list the living characters (the engine's).
type aliveWorld interface {
	Alive(ctx context.Context) ([]characters.Character, error)
}

var _ panel.TerminalFinder = (*terminalRelay)(nil)

// FindByName implements panel.TerminalFinder: the living characters with the
// name that have a terminal.
func (r *terminalRelay) FindByName(ctx context.Context, name string) ([]panel.TerminalInfo, error) {
	aw, ok := r.w.(aliveWorld)
	if !ok {
		return nil, nil
	}
	all, err := aw.Alive(ctx)
	if err != nil {
		return nil, err
	}
	var out []panel.TerminalInfo
	for _, c := range all {
		if !strings.EqualFold(c.Name, name) {
			continue
		}
		if c, err := r.found(c, nil); err == nil {
			out = append(out, r.info(c))
		}
	}
	return out, nil
}

// Info implements panel.TerminalSource.
func (r *terminalRelay) Info(ctx context.Context, terminalID string) (panel.TerminalInfo, error) {
	c, err := r.found(r.w.ByTerminal(ctx, terminalID))
	if err != nil {
		return panel.TerminalInfo{}, err
	}
	return r.info(c), nil
}

// Follow implements panel.TerminalSource.
func (r *terminalRelay) Follow(ctx context.Context, terminalID string, from uint64, sink panel.TerminalSink) (func(), error) {
	c, err := r.found(r.w.ByTerminal(ctx, terminalID))
	if err != nil {
		return nil, err
	}
	if c.Machine == r.w.Local() {
		return r.followLocal(terminalID, from, sink)
	}
	st := &relayStream{id: r.w.NewStream(), terminal: terminalID, machine: c.Machine, sink: sink, next: from}
	r.mu.Lock()
	r.streams[st.id] = st
	r.mu.Unlock()
	if r.w.Connected(c.Machine) {
		if err := r.request(ctx, st); err != nil {
			r.markDown(st)
		}
	} else {
		r.markDown(st)
	}
	return func() { r.release(st) }, nil
}

func (r *terminalRelay) request(ctx context.Context, st *relayStream) error {
	st.mu.Lock()
	from := st.next
	st.mu.Unlock()
	return r.w.SendTo(ctx, st.machine, protocol.TypeTerminalReplay,
		protocol.TerminalReplay{TerminalID: st.terminal, From: from, Stream: st.id.String()})
}

func (r *terminalRelay) release(st *relayStream) {
	st.mu.Lock()
	st.dead = true
	st.mu.Unlock()
	r.mu.Lock()
	delete(r.streams, st.id)
	r.mu.Unlock()
	if r.w.Connected(st.machine) {
		ctx, cancel := clock.WithTimeout(context.Background(), clock.Real{}, 5*time.Second)
		defer cancel()
		_ = r.w.SendTo(ctx, st.machine, protocol.TypeTerminalUnfollow, protocol.TerminalUnfollow{Stream: st.id.String()})
	}
}

// markDown tells the browser of a stream that its machine has no signal.
func (r *terminalRelay) markDown(st *relayStream) {
	r.mu.Lock()
	since, ok := r.down[st.machine]
	if !ok {
		since = r.w.Now()
		r.down[st.machine] = since
	}
	r.mu.Unlock()
	st.sink.Signal(false, since.UTC())
}

// followLocal streams a terminal of this machine straight from its buffer.
func (r *terminalRelay) followLocal(terminalID string, from uint64, sink panel.TerminalSink) (func(), error) {
	src := r.w.LocalSource()
	if src == nil {
		return nil, panel.ErrNoTerminal
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = termstream.Run(ctx, src, terminalID, from, localSink{sink}) // it ends only with an error
		if ctx.Err() == nil {
			sink.Signal(false, r.w.Now().UTC()) // the terminal went away: the screen stays
		}
	}()
	return cancel, nil
}

// localSink hands a termstream to the panel without leaving the process.
type localSink struct{ s panel.TerminalSink }

func (l localSink) Chunk(_ context.Context, off uint64, data []byte) error {
	l.s.Output(off, data)
	return nil
}

func (l localSink) Replayed(_ context.Context, from, to uint64, lost bool) error {
	l.s.Replayed(from, to, lost)
	return nil
}

// Screen implements panel.TerminalSource.
func (r *terminalRelay) Screen(ctx context.Context, terminalID string) (string, uint64, error) {
	c, err := r.found(r.w.ByTerminal(ctx, terminalID))
	if err != nil {
		return "", 0, err
	}
	sc, err := r.w.Screen(ctx, c.Machine, c.ID)
	if err != nil {
		return "", 0, noSignal(err)
	}
	return sc.Text, sc.Offset, nil
}

// Type implements panel.TerminalSource.
func (r *terminalRelay) Type(ctx context.Context, terminalID string, data []byte) error {
	c, err := r.found(r.w.ByTerminal(ctx, terminalID))
	if err != nil {
		return err
	}
	return noSignal(r.w.TypeKeys(ctx, c.Machine, c.ID, terminalID, data))
}

// noSignal turns "the machine is not reachable" into what the panel says.
func noSignal(err error) error {
	if errors.Is(err, characters.ErrPermissionPrompt) {
		return panel.ErrPermissionPrompt
	}
	var unreachable *characters.MachineUnreachableError
	if errors.As(err, &unreachable) || errors.Is(err, protocol.ErrNotConnected) {
		return panel.ErrNoSignal
	}
	return err
}

// OnFrame is the receiver of the binary frames 0x01 of the Towers: the output
// of a stream goes to the browser that asked for it, and to no other, and only
// from the machine the stream was asked of.
func (r *terminalRelay) OnFrame(_ context.Context, peer protocol.Peer, f protocol.DataFrame) {
	r.mu.Lock()
	st := r.streams[f.ID]
	r.mu.Unlock()
	if st == nil || st.machine != peer.ID || len(f.Data) == 0 {
		return
	}
	st.mu.Lock()
	if st.dead {
		st.mu.Unlock()
		return
	}
	if end := f.Pos + uint64(len(f.Data)); end > st.next {
		st.next = end
	}
	st.mu.Unlock()
	st.sink.Output(f.Pos, f.Data)
}

// OnReplayed handles terminal.replayed: the replay caught up.
func (r *terminalRelay) OnReplayed(peer protocol.Peer, p protocol.TerminalReplayed) {
	id, err := protocol.ParseULID(p.Stream)
	if err != nil {
		return
	}
	r.mu.Lock()
	st := r.streams[id]
	r.mu.Unlock()
	if st == nil || st.machine != peer.ID {
		return
	}
	st.sink.Replayed(p.From, p.To, p.Lost)
}

// MachineDown is told that the connection of a Tower ended: its browsers see the
// terminal grey, with the screen they have.
func (r *terminalRelay) MachineDown(machine string) {
	now := r.w.Now().UTC()
	r.mu.Lock()
	if _, ok := r.down[machine]; !ok {
		r.down[machine] = now
	}
	since := r.down[machine]
	var mine []*relayStream
	for _, st := range r.streams {
		if st.machine == machine {
			mine = append(mine, st)
		}
	}
	r.mu.Unlock()
	for _, st := range mine {
		st.sink.Signal(false, since)
	}
}

// MachineUp is told that a Tower is connected and ready: the terminals of its
// browsers get the signal back and each stream asks again from where it was.
func (r *terminalRelay) MachineUp(ctx context.Context, machine string) {
	r.mu.Lock()
	delete(r.down, machine)
	var mine []*relayStream
	for _, st := range r.streams {
		if st.machine == machine {
			mine = append(mine, st)
		}
	}
	r.mu.Unlock()
	for _, st := range mine {
		if err := r.request(ctx, st); err == nil {
			st.sink.Signal(true, time.Time{})
		}
	}
}

// Hooks chains the relay into the hooks of the protocol server: a Tower that
// connects gets its streams asked again, one that leaves greys them.
func (r *terminalRelay) Hooks(base protocol.Hooks) protocol.Hooks {
	h := base
	h.OnReady = func(s *protocol.Session) {
		if base.OnReady != nil {
			base.OnReady(s)
		}
		go r.MachineUp(context.Background(), s.Peer().ID)
	}
	h.OnDisconnect = func(s *protocol.Session, cause error) {
		if base.OnDisconnect != nil {
			base.OnDisconnect(s, cause)
		}
		r.MachineDown(s.Peer().ID)
	}
	return h
}

// runtimeSourceFor is the buffers of the local Runtime, nil if there is none.
func runtimeSourceFor(rt *characters.Runtime) termstream.Source {
	if rt == nil {
		return nil
	}
	return runtimeSource{rt}
}
