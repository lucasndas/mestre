package protocol_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/internal/testkit/protorig"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const (
	citadelAddr = "citadel:7443"
	towerHost   = "tower1"
	towerID     = "tower1"
	citadelID   = "citadel"
)

// world is a Citadel and a Tower on the fake network, inside a synctest
// bubble: the time is the bubble's, so a minute of heartbeat costs nothing.
type world struct {
	t   *testing.T
	clk clock.Clock
	net *fakenet.Network
	nw  protorig.Network

	cfgC, cfgT     protocol.Config
	partsC, partsT protorig.Parts

	server *protocol.Server
	client *protocol.Client
	lis    protocol.Listener

	cancelServer, cancelClient context.CancelFunc
	wgServer, wgClient         sync.WaitGroup

	dropAcks atomic.Bool // the Tower's acks are lost on the way

	gate  atomic.Pointer[chan struct{}] // while set, the Tower's writes wait for it to close
	logMu sync.Mutex
	log   []sentFrame

	citadelSent []string // the types of the control messages the Citadel wrote

	holdAt          atomic.Int64 // a part with this index is never written (-1: none)
	corruptAt       atomic.Int64 // the part with this index is written once with a byte changed (-1: none)
	dropProgress    atomic.Bool  // the Citadel's xfer.progress is lost on the way
	citadelDropAcks atomic.Bool  // the Citadel's acks are lost on the way

	dialMu sync.Mutex
	dials  []time.Time // when the Tower tried to connect
}

type opts struct {
	epochC, epochT uint64
	versionsC      protocol.Range
	versionsT      protocol.Range
}

func newWorld(t *testing.T, o opts) *world {
	t.Helper()
	w := &world{t: t, clk: clock.Real{}}
	w.holdAt.Store(-1)
	w.corruptAt.Store(-1)
	w.net = fakenet.New(w.clk)
	w.nw = protorig.Network{Net: w.net}
	if o.epochC == 0 {
		o.epochC = 1
	}
	if o.epochT == 0 {
		o.epochT = o.epochC
	}
	w.cfgC, w.partsC = protorig.Config(w.clk, citadelID, o.epochC, citadelID)
	w.cfgT, w.partsT = protorig.Config(w.clk, towerID, o.epochT, citadelID)
	w.cfgC.Router, w.cfgT.Router = protocol.NewRouter(), protocol.NewRouter()
	if o.versionsC.Valid() {
		w.cfgC.Profile = protocol.TowerProfile()
		w.cfgC.Profile.Versions = o.versionsC
	}
	if o.versionsT.Valid() {
		w.cfgT.Profile = protocol.TowerProfile()
		w.cfgT.Profile.Versions = o.versionsT
	}
	return w
}

// start builds the Server and the Client from the (possibly adjusted)
// configurations and runs them.
func (w *world) start() {
	w.t.Helper()
	var err error
	if w.server, err = protocol.NewServer(w.cfgC); err != nil {
		w.t.Fatal(err)
	}
	if w.client, err = protocol.NewClient(w.cfgT, w.dialer()); err != nil {
		w.t.Fatal(err)
	}
	w.listenAndRun(true)
}

// startServer runs only the Citadel, for the tests that play the Tower by hand.
func (w *world) startServer() {
	w.t.Helper()
	var err error
	if w.server, err = protocol.NewServer(w.cfgC); err != nil {
		w.t.Fatal(err)
	}
	w.listenAndRun(false)
}

func (w *world) listenAndRun(withClient bool) {
	w.runServer()
	if withClient {
		ctx, cancel := context.WithCancel(context.Background())
		w.cancelClient = cancel
		w.wgClient.Go(func() { _ = w.client.Run(ctx) })
	}
}

func (w *world) runServer() {
	var err error
	// the machine that answers at the address is the one of the configuration
	// (a promoted Citadel, in a test of a change of Citadel)
	if w.lis, err = w.nw.Listen(citadelAddr, protocol.Peer{Role: protocol.RoleMachine, ID: w.cfgC.MachineID}); err != nil {
		w.t.Fatal(err)
	}
	w.lis = &tapListener{Listener: w.lis, w: w}
	ctx, cancel := context.WithCancel(context.Background())
	w.cancelServer = cancel
	srv, lis := w.server, w.lis
	w.wgServer.Go(func() { _ = srv.Serve(ctx, lis) })
}

// restartCitadel stops the Citadel and starts another over the same
// configuration (the test may have changed its stores): the machine rebooted.
func (w *world) restartCitadel() {
	w.t.Helper()
	w.cancelServer()
	w.wgServer.Wait()
	var err error
	if w.server, err = protocol.NewServer(w.cfgC); err != nil {
		w.t.Fatal(err)
	}
	w.runServer()
}

// stop ends both sides and waits for their goroutines.
func (w *world) stop() {
	if w.cancelClient != nil {
		w.cancelClient()
	}
	w.cancelServer()
	w.wgClient.Wait()
	w.wgServer.Wait()
}

func (w *world) dialer() protocol.Dialer {
	inner := w.nw.Dialer(towerHost, citadelAddr, protocol.Peer{Role: protocol.RoleMachine, ID: towerID})
	return protocol.DialerFunc(func(ctx context.Context) (protocol.Conn, error) {
		w.dialMu.Lock()
		w.dials = append(w.dials, w.clk.Now())
		w.dialMu.Unlock()
		c, err := inner.Dial(ctx)
		if err != nil {
			return nil, err
		}
		return &tap{Conn: c, w: w}, nil
	})
}

// toTower is the Citadel's link to the Tower.
func (w *world) toTower() *protocol.Link { return w.server.Link(towerID) }

// settle lets every goroutine of the bubble reach a point where it waits.
func settle() { synctest.Wait() }

// advance moves the bubble's time by d and lets everything settle.
func advance(d time.Duration) {
	_ = clock.Sleep(context.Background(), clock.Real{}, d) // the bubble's clock
	synctest.Wait()
}

// tap sits on the Tower's side of the connection: it drops the acks the Tower
// sends while dropAcks is set (a network that loses the answer after the
// command arrived), holds every Send while the gate is closed, and logs what
// went out.
type tap struct {
	protocol.Conn
	w *world
}

// sentFrame is one frame the Tower wrote.
type sentFrame struct {
	kind protocol.FrameKind
	n    int    // bytes of data for a binary frame
	pos  uint64 // its position
	typ  string // the type of a control frame
}

// Ping lets the protocol keep this connection alive, as the Conn under it asks.
func (c *tap) Ping(ctx context.Context) error { return c.Conn.(protocol.Pinger).Ping(ctx) }

func (c *tap) Write(ctx context.Context, kind protocol.FrameKind, data []byte) error {
	f := protocol.Frame{Kind: kind, Data: data}
	if f.Kind == protocol.FrameText && c.w.dropAcks.Load() && bytes.Contains(f.Data, []byte(`"type":"ack"`)) {
		return nil
	}
	if f.Kind == protocol.FrameBinary {
		pos := binary.BigEndian.Uint64(f.Data[17:25])
		if hold := c.w.holdAt.Load(); hold >= 0 && int64(pos) == hold {
			<-ctx.Done() // the part never leaves: the connection is about to be cut
			return ctx.Err()
		}
		if c.w.corruptAt.Load() == int64(pos) && f.Data[0] == byte(protocol.DataXfer) && c.w.corruptAt.CompareAndSwap(int64(pos), -1) {
			f.Data = append([]byte(nil), f.Data...)
			f.Data[protocol.DataHeaderSize] ^= 0xFF
		}
	}
	if g := c.w.gate.Load(); g != nil {
		select {
		case <-*g:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := c.Conn.Write(ctx, f.Kind, f.Data); err != nil {
		return err
	}
	e := sentFrame{kind: f.Kind}
	if f.Kind == protocol.FrameBinary {
		e.n = len(f.Data) - protocol.DataHeaderSize
		e.pos = binary.BigEndian.Uint64(f.Data[17:25])
	} else if _, rest, ok := bytes.Cut(f.Data, []byte(`"type":"`)); ok {
		typ, _, _ := bytes.Cut(rest, []byte(`"`))
		e.typ = string(typ)
	}
	c.w.logMu.Lock()
	c.w.log = append(c.w.log, e)
	c.w.logMu.Unlock()
	return nil
}

func (w *world) sent() []sentFrame {
	w.logMu.Lock()
	defer w.logMu.Unlock()
	return append([]sentFrame(nil), w.log...)
}

// gaps are the intervals between the attempts of the Tower to connect.
func (w *world) gaps() []time.Duration {
	w.dialMu.Lock()
	defer w.dialMu.Unlock()
	var out []time.Duration
	for i := 1; i < len(w.dials); i++ {
		out = append(out, w.dials[i].Sub(w.dials[i-1]))
	}
	return out
}

// tapListener sits on the Citadel's side: it can lose the xfer.progress the
// Citadel sends.
type tapListener struct {
	protocol.Listener
	w *world
}

func (l *tapListener) Accept(ctx context.Context) (protocol.Conn, error) {
	c, err := l.Listener.Accept(ctx)
	if err != nil {
		return nil, err
	}
	return &citadelTap{Conn: c, w: l.w}, nil
}

type citadelTap struct {
	protocol.Conn
	w *world
}

func (c *citadelTap) Ping(ctx context.Context) error { return c.Conn.(protocol.Pinger).Ping(ctx) }

// Read plays the transport that discards a message over the limit: the
// connection stays and the error says why.
func (c *citadelTap) Read(ctx context.Context) (protocol.FrameKind, []byte, error) {
	kind, data, err := c.Conn.Read(ctx)
	if err == nil && kind == protocol.FrameText && len(data) > protocol.MaxMessage {
		return 0, nil, fmt.Errorf("message of %d bytes: %w", len(data), protocol.ErrFrameTooLarge)
	}
	return kind, data, err
}

func (c *citadelTap) Write(ctx context.Context, kind protocol.FrameKind, data []byte) error {
	f := protocol.Frame{Kind: kind, Data: data}
	if f.Kind == protocol.FrameText {
		if _, rest, ok := bytes.Cut(f.Data, []byte(`"type":"`)); ok {
			typ, _, _ := bytes.Cut(rest, []byte(`"`))
			c.w.logMu.Lock()
			c.w.citadelSent = append(c.w.citadelSent, string(typ))
			c.w.logMu.Unlock()
		}
	}
	if f.Kind == protocol.FrameText && c.w.citadelDropAcks.Load() && bytes.Contains(f.Data, []byte(`"type":"ack"`)) {
		return nil
	}
	if f.Kind == protocol.FrameText && c.w.dropProgress.Load() && bytes.Contains(f.Data, []byte(`"type":"xfer.progress"`)) {
		return nil
	}
	return c.Conn.Write(ctx, f.Kind, f.Data)
}

// citadelLog calls f with the type of each control message the Citadel wrote.
func (w *world) citadelLog(f func(typ string)) {
	w.logMu.Lock()
	log := append([]string(nil), w.citadelSent...)
	w.logMu.Unlock()
	for _, typ := range log {
		f(typ)
	}
}
