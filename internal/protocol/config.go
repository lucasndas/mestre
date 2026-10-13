package protocol

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Timing holds the intervals of docs/conectividade/protocolo.md#heartbeat-e-reconexão.
// The zero value of each field is its documented default.
type Timing struct {
	// Ping is the interval of the WebSocket ping (15 s).
	Ping time.Duration
	// PongTimeout is how long a ping may go without a pong before the
	// connection is given as dead (45 s).
	PongTimeout time.Duration
	// Heartbeat is the interval of machine.heartbeat (30 s).
	Heartbeat time.Duration
	// HeartbeatLost is the silence after which the Citadel considers the
	// Tower offline (90 s).
	HeartbeatLost time.Duration
	// Handshake is how long the opening may take (10 s).
	Handshake time.Duration
	// CertTimeout is how long to wait for the certificate asked of a peer
	// that follows a higher epoch (10 s).
	CertTimeout time.Duration
	// BackoffMin and BackoffMax bound the reconnection delay (1 s doubling
	// to 60 s), with Jitter of variation up to either side (0.2).
	BackoffMin, BackoffMax time.Duration
	Jitter                 float64
	// RediscoverAfter is how many failures in a row make the Tower look for
	// the Guild again (3).
	RediscoverAfter int
	// ProcessedTTL is how long processed ids are kept (24 h) and
	// ProcessedPrune how often they are pruned (1 h).
	ProcessedTTL, ProcessedPrune time.Duration
}

func (t Timing) withDefaults() Timing {
	def := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	def(&t.Ping, 15*time.Second)
	def(&t.PongTimeout, 45*time.Second)
	def(&t.Heartbeat, 30*time.Second)
	def(&t.HeartbeatLost, 90*time.Second)
	def(&t.Handshake, 10*time.Second)
	def(&t.CertTimeout, 10*time.Second)
	def(&t.BackoffMin, time.Second)
	def(&t.BackoffMax, 60*time.Second)
	def(&t.ProcessedTTL, DefaultProcessedTTL)
	def(&t.ProcessedPrune, time.Hour)
	if t.Jitter <= 0 {
		t.Jitter = 0.2
	}
	if t.RediscoverAfter <= 0 {
		t.RediscoverAfter = 3
	}
	return t
}

// Profile says which opening a connection uses. The motors use the Tower
// profile; the Guardian has its own version, negotiated in guardian.hello the
// same way (docs/conectividade/protocolo.md#conexão-do-guardião).
type Profile struct {
	HelloType, WelcomeType string
	// PeerRole is who may open this kind of connection.
	PeerRole Role
	// Versions is the range this binary speaks on this kind of connection.
	Versions Range
	// Heartbeats says whether the Tower sends machine.heartbeat and the
	// Citadel watches for it.
	Heartbeats bool
}

// TowerProfile is the profile of the connection between motors.
func TowerProfile() Profile {
	return Profile{HelloType: TypeHello, WelcomeType: TypeWelcome, PeerRole: RoleMachine, Versions: DefaultRange(), Heartbeats: true}
}

// HelloInfo is what the Tower puts in its opening besides what the protocol
// itself knows (the range, the capabilities and the certificate).
type HelloInfo struct {
	Version    string
	Sheet      Object
	ReplicaLSN uint64
	// ReplicaEpoch is the epoch of the replica (see Hello.ReplicaEpoch).
	ReplicaEpoch uint64
	Terminals    []TerminalState
}

// Hooks are the points where the stages that come after the protocol plug
// in. Every field is optional.
type Hooks struct {
	// OnHello lets the Citadel refuse an opening (a revoked machine, a
	// sheet it does not accept). Returning a *CloseError closes with that
	// code; a *Error answers with its code (`invalid_payload` for a sheet
	// without the required fields); any other error closes with `forbidden`.
	OnHello func(ctx context.Context, peer Peer, h *Hello) error
	// OnConnect is called when a connection is established, before the
	// outbox is resent; OnDisconnect when it ends, with the cause.
	OnConnect    func(s *Session)
	OnDisconnect func(s *Session, cause error)
	// OnReady is called once the connection is the current one of its link and
	// the outbox was queued on it: from here the application may send on the
	// link (the replication starts its stream here).
	OnReady func(s *Session)
	// HeartbeatLost is called when the Citadel hears no machine.heartbeat
	// from a Tower for Timing.HeartbeatLost.
	HeartbeatLost func(peer Peer)
	// ClockSkew is called by the Tower with the difference to the clock of
	// the Citadel when it passes 5 s (a warning of the checklist).
	ClockSkew func(skew time.Duration)
	// Rediscover is called by the Tower after Timing.RediscoverAfter
	// failures in a row, so the Dialer finds the Guild again.
	Rediscover func(ctx context.Context)
	// Incompatible is called by the Tower when the Citadel closed with 4003,
	// with the range the Citadel accepts.
	Incompatible func(local, remote Range)
	// Revoked is called by the Tower when the credential was revoked (4002);
	// the Client stops trying.
	Revoked func()
}

// ClockSkewLimit is the difference between clocks that becomes a warning.
const ClockSkewLimit = 5 * time.Second

// Config is what a Client or a Server needs; the fields marked required
// must be set.
type Config struct {
	// MachineID is this machine (required).
	MachineID string
	// Clock drives every timer (required).
	Clock clock.Clock
	// Registry is the catalogue of the connection (default DefaultRegistry).
	Registry *Registry
	// Router holds the handlers of the application (default empty).
	Router *Router
	// IsCitadel, when set, says whether this machine holds the role now; a
	// Server that is not the Citadel closes the opening with 4007 and the
	// current certificate (docs/conectividade/protocolo.md#heartbeat-e-reconexão).
	IsCitadel func() bool
	// Epoch and Certs are the epoch check (required).
	Epoch EpochState
	Certs CertVerifier
	// Outbox and Processed are the durable halves of ack and idempotency
	// (required).
	Outbox    Outbox
	Processed Processed
	// Profile is the opening (default TowerProfile).
	Profile Profile
	// Capabilities this binary announces.
	Capabilities Capabilities
	// BinaryVersion is the version of the binary, for hello and welcome.
	BinaryVersion string
	Timing        Timing
	Hooks         Hooks
	// Hello builds the opening of a Tower (required on a Client).
	Hello func(ctx context.Context) (HelloInfo, error)
	// Heartbeat reads the numbers of machine.heartbeat on a Tower.
	Heartbeat func(ctx context.Context) (MachineHeartbeat, error)
	// Xfer receives the transfers in parts (nil refuses them as unavailable).
	Xfer *XferConfig
	// Rand supplies the jitter of the backoff, in [0,1) (default math/rand).
	Rand func() float64
	// Logger receives the events of the connection (default discards).
	Logger *slog.Logger
}

// ErrBadConfig is wrapped by the constructors.
var ErrBadConfig = errors.New("protocol: invalid configuration")

func (c *Config) prepare() error {
	switch {
	case c.MachineID == "":
		return fmt.Errorf("%w: MachineID", ErrBadConfig)
	case c.Clock == nil:
		return fmt.Errorf("%w: Clock", ErrBadConfig)
	case c.Epoch == nil:
		return fmt.Errorf("%w: Epoch", ErrBadConfig)
	case c.Certs == nil:
		return fmt.Errorf("%w: Certs", ErrBadConfig)
	case c.Outbox == nil:
		return fmt.Errorf("%w: Outbox", ErrBadConfig)
	case c.Processed == nil:
		return fmt.Errorf("%w: Processed", ErrBadConfig)
	}
	if c.Registry == nil {
		c.Registry = DefaultRegistry()
	}
	if c.Router == nil {
		c.Router = NewRouter()
	}
	if c.Profile.HelloType == "" {
		c.Profile = TowerProfile()
	}
	if !c.Profile.Versions.Valid() {
		return fmt.Errorf("%w: Profile.Versions %s", ErrBadConfig, c.Profile.Versions)
	}
	c.Timing = c.Timing.withDefaults()
	if c.Rand == nil {
		c.Rand = rand.Float64
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return nil
}

// backoff is the delay between attempts: BackoffMin, doubling up to
// BackoffMax, each time varied by up to Jitter to either side.
type backoff struct {
	t       Timing
	rnd     func() float64
	current time.Duration
}

func newBackoff(t Timing, rnd func() float64) *backoff {
	return &backoff{t: t, rnd: rnd, current: t.BackoffMin}
}

func (b *backoff) reset() { b.current = b.t.BackoffMin }

// next returns the delay to wait now and doubles the one for the next time.
func (b *backoff) next() time.Duration {
	d := b.current
	b.current = min(b.current*2, b.t.BackoffMax)
	factor := 1 + (2*b.rnd()-1)*b.t.Jitter
	return time.Duration(float64(d) * factor)
}
