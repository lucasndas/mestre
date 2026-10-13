package discovery

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// DefaultMaxPerSecond is how many requests a machine answers per second.
const DefaultMaxPerSecond = 10

// maxReadFailures is how many reads in a row may fail before a socket is
// given up.
const maxReadFailures = 50

// Info is what a machine says about itself when asked. The role and the
// epoch are the ones it has now.
type Info struct {
	GuildID   string
	MachineID string
	Name      string
	Role      string // RoleCitadel or RoleTower
	Epoch     uint64
	Port      int // the WSS port of this engine
	// Citadel is the Citadel this machine follows (itself, on the Citadel).
	Citadel  *CitadelRef
	Machines int
	CAFp     string
}

// ResponderConfig wires a Responder.
type ResponderConfig struct {
	Net PacketNetwork
	// Port is the discovery port; zero means DefaultPort.
	Port  int
	Clock clock.Clock
	// Info returns what to answer; false when this machine belongs to no
	// Guild yet, and then nothing is answered.
	Info         func() (Info, bool)
	MaxPerSecond int          // zero means DefaultMaxPerSecond
	Logger       *slog.Logger // nil discards
}

// Responder answers the broadcast requests of the machines that look for the
// Guild. Each instance of a machine runs its own, on the shared port, and
// answers with its own machine_id.
type Responder struct {
	cfg  ResponderConfig
	conn PacketConn
	wg   sync.WaitGroup
	once sync.Once

	// sent are the times of the last answers, newest last, for the limit.
	sent    []time.Time
	dropped atomic.Int64
}

// StartResponder binds the shared port and answers until Close or until ctx
// is done.
func StartResponder(ctx context.Context, cfg ResponderConfig) (*Responder, error) {
	if cfg.Net == nil || cfg.Clock == nil || cfg.Info == nil {
		return nil, errors.New("discovery: ResponderConfig needs Net, Clock and Info")
	}
	if cfg.Port == 0 {
		cfg.Port = DefaultPort
	}
	if cfg.MaxPerSecond <= 0 {
		cfg.MaxPerSecond = DefaultMaxPerSecond
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	conn, err := cfg.Net.ListenShared(cfg.Port)
	if err != nil {
		return nil, err
	}
	r := &Responder{cfg: cfg, conn: conn}
	stop := context.AfterFunc(ctx, r.Close)
	r.wg.Go(func() {
		defer stop()
		r.loop()
	})
	return r, nil
}

// Close stops the Responder and waits for it.
func (r *Responder) Close() {
	r.once.Do(func() { _ = r.conn.Close() })
	r.wg.Wait()
}

// Dropped is how many requests were not answered for the limit per second.
func (r *Responder) Dropped() int { return int(r.dropped.Load()) }

func (r *Responder) loop() {
	buf := make([]byte, 2*MaxMessage) // a longer datagram is read whole and refused
	failures := 0
	for {
		n, from, err := r.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			// A read can fail for a datagram that was not for us (an ICMP
			// error answering an earlier reply); only a socket that keeps
			// failing is given up.
			if failures++; failures >= maxReadFailures {
				r.cfg.Logger.Error("discovery: reading requests", "err", err)
				return
			}
			continue
		}
		failures = 0
		r.handle(buf[:n], from)
	}
}

func (r *Responder) handle(b []byte, from string) {
	req, err := DecodeRequest(b)
	if err != nil {
		return // not for us, another version, too big: ignored
	}
	info, ok := r.cfg.Info()
	if !ok || info.GuildID == "" {
		return
	}
	// Only the Guild asked for answers; an empty guild_id asks every Guild,
	// which only the join of a Guild does.
	if req.GuildID != "" && req.GuildID != info.GuildID {
		return
	}
	if !r.allow() {
		r.dropped.Add(1)
		return
	}
	out, err := Response{
		Nonce: req.Nonce, GuildID: info.GuildID, MachineID: info.MachineID, Name: info.Name,
		Role: info.Role, Epoch: info.Epoch, Port: info.Port, Citadel: info.Citadel,
		Machines: info.Machines, CAFp: info.CAFp,
	}.Encode()
	if err != nil {
		r.cfg.Logger.Error("discovery: encoding the answer", "err", err)
		return
	}
	if err := r.conn.WriteTo(out, from); err != nil {
		r.cfg.Logger.Debug("discovery: answering", "to", from, "err", err)
	}
}

// allow counts one more answer in the last second.
func (r *Responder) allow() bool {
	now := r.cfg.Clock.Now()
	if len(r.sent) >= r.cfg.MaxPerSecond && now.Sub(r.sent[len(r.sent)-r.cfg.MaxPerSecond]) < time.Second {
		return false
	}
	r.sent = append(r.sent, now)
	if len(r.sent) > 4*r.cfg.MaxPerSecond {
		r.sent = append([]time.Time(nil), r.sent[len(r.sent)-r.cfg.MaxPerSecond:]...)
	}
	return true
}
