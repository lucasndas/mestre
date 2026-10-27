package app

import (
	"bytes"
	"context"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/terminal"
	"github.com/mestre-ia/mestre/internal/termstream"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// bufferSource is the mestre-pty buffer of a machine: bytes by offset, from a
// start that may have left (a Lost stretch), answering at most `max` per call as
// the mestre-pty does (4 MiB).
type bufferSource struct {
	mu      sync.Mutex
	start   uint64
	data    []byte
	max     int
	wait    chan struct{}
	replays []uint64
}

func newBufferSource(start uint64, data []byte) *bufferSource {
	return &bufferSource{start: start, data: data, max: 4 << 20, wait: make(chan struct{})}
}

func (b *bufferSource) end() uint64 { return b.start + uint64(len(b.data)) }

func (b *bufferSource) Replay(_ context.Context, _ string, from uint64) (terminal.Replayed, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.replays = append(b.replays, from)
	r := terminal.Replayed{Offset: from, End: b.end()}
	if from < b.start {
		r.Offset, r.Lost = b.start, b.start-from
	}
	if r.Offset < b.end() {
		d := b.data[r.Offset-b.start:]
		r.Data = d[:min(len(d), b.max)]
	}
	return r, nil
}

func (b *bufferSource) WaitOutput(ctx context.Context, _ string, after uint64) error {
	for {
		b.mu.Lock()
		more, ch := b.end() > after, b.wait
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

func (b *bufferSource) print(p []byte) {
	b.mu.Lock()
	b.data = append(b.data, p...)
	close(b.wait)
	b.wait = make(chan struct{})
	b.mu.Unlock()
}

// towerLink is the connection of a fake Tower to the Citadel: what the Tower
// sends goes straight to the relay, as the router of the Citadel would call it.
type towerLink struct {
	relay *terminalRelay
	peer  protocol.Peer

	mu   sync.Mutex
	sess *protocol.Session
}

func (l *towerLink) Session() *protocol.Session {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sess
}

// drop ends the connection: the streams that began on it end the next time they
// try to send, as in the Tower, where a stream never outlives its connection.
func (l *towerLink) drop() {
	l.mu.Lock()
	l.sess = nil
	l.mu.Unlock()
}

// reconnect is a new connection, which no old stream belongs to.
func (l *towerLink) reconnect() {
	l.mu.Lock()
	l.sess = &protocol.Session{}
	l.mu.Unlock()
}

func (l *towerLink) SendData(_ context.Context, f protocol.DataFrame) error {
	f.Data = bytes.Clone(f.Data)
	l.relay.OnFrame(context.Background(), l.peer, f)
	return nil
}

func (l *towerLink) Send(_ context.Context, o protocol.Outgoing) (string, error) {
	if p, ok := o.Payload.(protocol.TerminalReplayed); ok {
		l.relay.OnReplayed(l.peer, p)
	}
	return "", nil
}

// relayWorldFake is a Citadel with one character on the machine "t1", whose
// Tower is the terminalServer behind a towerLink.
type relayWorldFake struct {
	mu        sync.Mutex
	char      characters.Character
	connected bool
	sent      []sentMsg
	server    *terminalServer
	link      *towerLink
	typed     [][]byte
	clk       *clock.Fake
	ids       *protocol.IDGen
}

type sentMsg struct {
	machine, typ string
	payload      any
}

func (w *relayWorldFake) ByCharacter(context.Context, string) (characters.Character, error) {
	return w.char, nil
}
func (w *relayWorldFake) ByTerminal(context.Context, string) (characters.Character, error) {
	return w.char, nil
}
func (w *relayWorldFake) Local() string                  { return "citadel" }
func (w *relayWorldFake) LocalSource() termstream.Source { return nil }
func (w *relayWorldFake) Connected(string) bool          { w.mu.Lock(); defer w.mu.Unlock(); return w.connected }
func (w *relayWorldFake) Now() time.Time                 { return w.clk.Now() }
func (w *relayWorldFake) NewStream() protocol.ULID       { return w.ids.New() }
func (w *relayWorldFake) Screen(context.Context, string, string) (characters.Screen, error) {
	return characters.Screen{Text: "tela", Offset: 7}, nil
}

func (w *relayWorldFake) TypeKeys(_ context.Context, _, _, _ string, data []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.typed = append(w.typed, data)
	return nil
}

// SendTo is what the Tower would receive: replay starts a stream of its server,
// unfollow stops it.
func (w *relayWorldFake) SendTo(_ context.Context, machine, typ string, payload any) error {
	w.mu.Lock()
	w.sent = append(w.sent, sentMsg{machine, typ, payload})
	w.mu.Unlock()
	switch p := payload.(type) {
	case protocol.TerminalReplay:
		return w.server.serve(w.link, p)
	case protocol.TerminalUnfollow:
		w.server.stop(p.Stream)
	}
	return nil
}

func (w *relayWorldFake) requests(typ string) []sentMsg {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []sentMsg
	for _, m := range w.sent {
		if m.typ == typ {
			out = append(out, m)
		}
	}
	return out
}

// collector is the browser end of a followed terminal: it keeps what the panel
// would put in the socket.
type collector struct {
	mu       sync.Mutex
	data     []byte
	first    uint64
	got      bool
	next     uint64
	jumps    int
	replayed []protocol.TerminalReplayed
	signals  []bool
	changed  chan struct{}
}

func newCollector() *collector { return &collector{changed: make(chan struct{}, 1024)} }

func (c *collector) Output(off uint64, data []byte) {
	c.mu.Lock()
	if !c.got {
		c.got, c.first, c.next = true, off, off
	}
	if off != c.next {
		c.jumps++
	}
	c.data = append(c.data, data...)
	c.next = off + uint64(len(data))
	c.mu.Unlock()
	c.tick()
}

func (c *collector) Replayed(from, to uint64, lost bool) {
	c.mu.Lock()
	c.replayed = append(c.replayed, protocol.TerminalReplayed{From: from, To: to, Lost: lost})
	c.mu.Unlock()
	c.tick()
}

func (c *collector) Signal(up bool, _ time.Time) {
	c.mu.Lock()
	c.signals = append(c.signals, up)
	c.mu.Unlock()
	c.tick()
}

func (c *collector) tick() {
	select {
	case c.changed <- struct{}{}:
	default:
	}
}

// until waits for a change of the collector until cond holds.
func (c *collector) until(t *testing.T, cond func() bool) {
	t.Helper()
	for {
		c.mu.Lock()
		ok := cond()
		c.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-c.changed:
		case <-t.Context().Done():
			t.Fatal("the condition never came")
		}
	}
}

func newRelayWorld(t *testing.T, src *bufferSource) (*terminalRelay, *relayWorldFake) {
	t.Helper()
	clk := clock.NewFake(time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC))
	w := &relayWorldFake{
		char:      characters.Character{ID: "c1", Name: "Dev-1", Machine: "t1", Workspace: "loja", Table: "m1", TerminalID: protocol.NewIDGen(clk).NewString(), State: characters.Idle},
		connected: true, server: newTerminalServer(src), clk: clk, ids: protocol.NewIDGen(clk),
	}
	r := newTerminalRelay(w)
	w.link = &towerLink{relay: r, peer: protocol.Peer{ID: "t1", Role: protocol.RoleMachine}, sess: &protocol.Session{}}
	return r, w
}

// CA-PROTO-09: a browser opens a terminal whose machine already sent 300 KiB of
// output; the Citadel asks the machine for the replay from offset 0 and the
// browser gets the 300 KiB, byte for byte, and then what the terminal prints
// afterwards, without the Citadel keeping any of it.
func TestTheCitadelAsksTheMachineForTheReplayFromZeroAndRelaysItAndThenTheLiveOutput(t *testing.T) {
	t.Parallel()
	out := make([]byte, 300<<10)
	for i := range out {
		out[i] = byte('a' + i%23)
	}
	src := newBufferSource(0, bytes.Clone(out))
	r, w := newRelayWorld(t, src)
	b := newCollector()
	stop, err := r.Follow(t.Context(), w.char.TerminalID, 0, b)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	reqs := w.requests(protocol.TypeTerminalReplay)
	if len(reqs) != 1 {
		t.Fatalf("%d terminal.replay sent, want 1", len(reqs))
	}
	if rq := reqs[0].payload.(protocol.TerminalReplay); rq.From != 0 || rq.TerminalID != w.char.TerminalID || rq.Stream == "" || reqs[0].machine != "t1" {
		t.Fatalf("the request = %+v to %s", rq, reqs[0].machine)
	}
	b.until(t, func() bool { return len(b.data) >= len(out) && len(b.replayed) == 1 })
	if !bytes.Equal(b.data, out) || b.first != 0 || b.jumps != 0 {
		t.Fatalf("the browser got %d bytes from %d (%d jumps), want the 300 KiB from 0", len(b.data), b.first, b.jumps)
	}
	if rp := b.replayed[0]; rp.From != 0 || rp.To != uint64(len(out)) || rp.Lost {
		t.Fatalf("replayed = %+v", rp)
	}
	// what the terminal prints afterwards comes on the same stream
	src.print([]byte("depois"))
	b.until(t, func() bool { return len(b.data) == len(out)+len("depois") })
	if string(b.data[len(out):]) != "depois" {
		t.Fatalf("the live output = %q", b.data[len(out):])
	}
}

// A replay whose start left the buffer reaches the browser with the lost mark,
// from the byte where the buffer begins.
func TestAStretchThatLeftTheBufferIsMarkedLost(t *testing.T) {
	t.Parallel()
	src := newBufferSource(1<<20, []byte("o que sobrou"))
	r, w := newRelayWorld(t, src)
	b := newCollector()
	stop, err := r.Follow(t.Context(), w.char.TerminalID, 0, b)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	b.until(t, func() bool { return len(b.replayed) == 1 })
	if rp := b.replayed[0]; !rp.Lost || rp.From != 1<<20 || b.first != 1<<20 || string(b.data) != "o que sobrou" {
		t.Fatalf("replayed = %+v, first = %d, data = %q", rp, b.first, b.data)
	}
}

// A machine that answers the replay in several chunks of the buffer (the
// mestre-pty answers at most 4 MiB) is read to the end.
func TestAReplayLongerThanOneAnswerIsReadToTheEnd(t *testing.T) {
	t.Parallel()
	out := bytes.Repeat([]byte("0123456789"), 5000)
	src := newBufferSource(0, bytes.Clone(out))
	src.max = 7000
	r, w := newRelayWorld(t, src)
	b := newCollector()
	stop, err := r.Follow(t.Context(), w.char.TerminalID, 0, b)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	b.until(t, func() bool { return len(b.replayed) == 1 })
	if !bytes.Equal(b.data, out) || b.jumps != 0 {
		t.Fatalf("got %d bytes with %d jumps, want %d", len(b.data), b.jumps, len(out))
	}
}

// Frames of a stream are taken only from the machine it was asked of: another
// Tower cannot put output in a browser, nor guess at a terminal's frames.
func TestOnlyTheMachineOfTheStreamFeedsIt(t *testing.T) {
	t.Parallel()
	src := newBufferSource(0, []byte("ok"))
	r, w := newRelayWorld(t, src)
	b := newCollector()
	stop, err := r.Follow(t.Context(), w.char.TerminalID, 0, b)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	b.until(t, func() bool { return len(b.replayed) == 1 })
	rq := w.requests(protocol.TypeTerminalReplay)[0].payload.(protocol.TerminalReplay)
	id, _ := protocol.ParseULID(rq.Stream)
	r.OnFrame(t.Context(), protocol.Peer{ID: "t2", Role: protocol.RoleMachine}, protocol.DataFrame{Type: protocol.DataTerminal, ID: id, Pos: 2, Data: []byte("falso")})
	r.OnReplayed(protocol.Peer{ID: "t2"}, protocol.TerminalReplayed{Stream: rq.Stream, From: 9, To: 9})
	tid, _ := protocol.ParseULID(w.char.TerminalID)
	r.OnFrame(t.Context(), w.link.peer, protocol.DataFrame{Type: protocol.DataTerminal, ID: tid, Pos: 2, Data: []byte("sem fluxo")})
	b.mu.Lock()
	defer b.mu.Unlock()
	if string(b.data) != "ok" || len(b.replayed) != 1 {
		t.Fatalf("the browser got %q and %d replayed", b.data, len(b.replayed))
	}
}

// The machine drops: the browser is told it has no signal; when it is back, the
// stream asks again from where it was, and the output that came meanwhile arrives
// without a repeat.
func TestAMachineThatDropsGreysTheTerminalAndResumesWhereItWas(t *testing.T) {
	t.Parallel()
	src := newBufferSource(0, []byte("antes"))
	r, w := newRelayWorld(t, src)
	b := newCollector()
	stop, err := r.Follow(t.Context(), w.char.TerminalID, 0, b)
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	b.until(t, func() bool { return string(b.data) == "antes" })

	w.mu.Lock()
	w.connected = false
	w.mu.Unlock()
	w.link.drop() // the stream of the old connection ends; without this it could also deliver "durante" and the new one would repeat it
	r.MachineDown("t1")
	info, _ := r.Info(t.Context(), w.char.TerminalID)
	if info.Signal || info.NoSignalSince == nil || !info.NoSignalSince.Equal(w.clk.Now()) {
		t.Fatalf("info = %+v", info)
	}
	b.until(t, func() bool { return len(b.signals) == 1 && !b.signals[0] })
	// the output goes on in the buffer of the machine while the Citadel is away
	src.print([]byte("durante"))
	w.mu.Lock()
	w.connected = true
	w.mu.Unlock()
	w.link.reconnect()
	r.MachineUp(t.Context(), "t1")
	b.until(t, func() bool { return string(b.data) == "antesdurante" && len(b.signals) == 2 })
	if !b.signals[1] || b.jumps != 0 {
		t.Fatalf("signals = %v, jumps = %d", b.signals, b.jumps)
	}
	reqs := w.requests(protocol.TypeTerminalReplay)
	if len(reqs) != 2 || reqs[1].payload.(protocol.TerminalReplay).From != uint64(len("antes")) {
		t.Fatalf("the second request = %+v", reqs)
	}
}

// Letting go of the terminal tells the machine to stop the stream, and the
// machine stops sending.
func TestLeavingTheTerminalStopsTheStreamOnTheMachine(t *testing.T) {
	t.Parallel()
	src := newBufferSource(0, []byte("x"))
	r, w := newRelayWorld(t, src)
	b := newCollector()
	stop, err := r.Follow(t.Context(), w.char.TerminalID, 0, b)
	if err != nil {
		t.Fatal(err)
	}
	b.until(t, func() bool { return len(b.replayed) == 1 })
	stop()
	if n := len(w.requests(protocol.TypeTerminalUnfollow)); n != 1 {
		t.Fatalf("%d unfollow sent", n)
	}
	src.print([]byte("depois"))
	// a frame that still comes for a stream that was let go is nobody's
	rq := w.requests(protocol.TypeTerminalReplay)[0].payload.(protocol.TerminalReplay)
	id, _ := protocol.ParseULID(rq.Stream)
	r.OnFrame(t.Context(), w.link.peer, protocol.DataFrame{Type: protocol.DataTerminal, ID: id, Pos: 1, Data: []byte("tarde")})
	b.mu.Lock()
	defer b.mu.Unlock()
	if bytes.Contains(b.data, []byte("tarde")) {
		t.Fatal("a stream that was let go still delivers")
	}
}

// A Tower that gets a replay with the stream of one before (the Citadel asks
// again after a reconnection) replaces it: one stream per id.
func TestAnotherReplayOfTheSameStreamReplacesTheOld(t *testing.T) {
	t.Parallel()
	src := newBufferSource(0, []byte("abc"))
	srv := newTerminalServer(src)
	r := newTerminalRelay(&relayWorldFake{clk: clock.NewFake(time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC)), ids: protocol.NewIDGen(clock.Real{})})
	link := &towerLink{relay: r, peer: protocol.Peer{ID: "t1"}, sess: &protocol.Session{}}
	sid := protocol.NewIDGen(clock.Real{}).NewString()
	for range 3 {
		if err := srv.serve(link, protocol.TerminalReplay{TerminalID: sid, From: 0, Stream: sid}); err != nil {
			t.Fatal(err)
		}
	}
	srv.mu.Lock()
	n := len(srv.streams)
	srv.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d streams for one id", n)
	}
	if err := srv.serve(link, protocol.TerminalReplay{TerminalID: sid, From: 0, Stream: "não é ulid"}); err == nil {
		t.Fatal("a stream that is not a ULID was accepted")
	}
	srv.stop(sid)
}

// The typing of a person goes to the machine as keys, never as the raw input of
// the inbox, and the screen of a terminal brings the offset it shows.
func TestTypingAndTheScreenGoThroughTheMachineOfTheCharacter(t *testing.T) {
	t.Parallel()
	r, w := newRelayWorld(t, newBufferSource(0, nil))
	if err := r.Type(t.Context(), w.char.TerminalID, []byte("ls\r")); err != nil || len(w.typed) != 1 || string(w.typed[0]) != "ls\r" {
		t.Fatalf("typed = %q, %v", w.typed, err)
	}
	text, off, err := r.Screen(t.Context(), w.char.TerminalID)
	if err != nil || text != "tela" || off != 7 {
		t.Fatalf("screen = %q %d %v", text, off, err)
	}
	w.char.State = characters.Busy
	info, _ := r.ByCharacter(t.Context(), "c1")
	if !info.Busy || info.Machine != "t1" || info.Workspace != "loja" || info.Table != "m1" || !info.Signal {
		t.Fatalf("info = %+v", info)
	}
	w.char.Machine = "citadel"
	if _, err := r.Follow(t.Context(), w.char.TerminalID, 0, newCollector()); err != panel.ErrNoTerminal {
		t.Fatalf("following a local terminal with no runtime: %v", err)
	}
}
