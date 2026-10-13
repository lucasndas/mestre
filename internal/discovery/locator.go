package discovery

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Verifier is the identity check: it opens TLS to an address and says which
// machine of the Guild is there, or fails if the certificate is not of the
// Guild (a foreign CA, no mestre:// identity, a revoked credential).
type Verifier interface {
	Verify(ctx context.Context, addr string) (machineID string, err error)
}

// TLSVerifier is the Verifier over the transport: the chain to the CA of the
// Guild, the identity SAN and the revocation hook of the transport client.
type TLSVerifier struct{ Prober transport.Prober }

var _ Verifier = TLSVerifier{}

// Verify implements Verifier.
func (v TLSVerifier) Verify(ctx context.Context, addr string) (string, error) {
	p, err := v.Prober.Probe(ctx, addr)
	if err != nil {
		return "", err
	}
	if p.Kind != transport.KindMachine {
		return "", fmt.Errorf("discovery: %s is a %s certificate, not an engine", addr, p.Kind)
	}
	return p.MachineID, nil
}

// Errors of the locator.
var (
	// ErrNotFound: no address passed the identity check ("Cidadela não
	// encontrada" in the checklist).
	ErrNotFound = errors.New("discovery: Citadel not found")
	// ErrNotTheCitadel: the machine at the address is of the Guild but is
	// not the one the highest certificate of epoch names.
	ErrNotTheCitadel = errors.New("discovery: the machine is not the Citadel named by the certificate")
)

// Defaults of the locator.
const (
	DefaultVerifyTimeout = 5 * time.Second
	DefaultBackoffMin    = time.Second
	DefaultBackoffMax    = 60 * time.Second
	DefaultJitter        = 0.2
)

// LocatorConfig wires a Locator.
type LocatorConfig struct {
	Clock  clock.Clock
	Logger *slog.Logger // nil discards
	// Configured are the addresses of guild.endereco ("host:port", or
	// "host" for the default engine port).
	Configured []string
	// Disabled is `descoberta: false`: only the configuration and the
	// cache are used.
	Disabled bool
	// Cache defaults to a MemCache.
	Cache Cache
	// CacheMaxAge defaults to 30 days.
	CacheMaxAge time.Duration
	// Known says what this machine knows of the Citadel now (the epoch and
	// the machine named by the highest certificate); required.
	Known    func() Known
	Verifier Verifier
	// Search is the broadcast and mDNS search; its Clock is the one above,
	// and its GuildID is the Guild of this machine.
	Search SearchConfig
	// VerifyTimeout bounds the check of one address (5 s).
	VerifyTimeout time.Duration
	// BackoffMin, BackoffMax and Jitter drive the retries of Run: the same
	// as the reconnection of the protocol (1 s doubling to 60 s, 20%).
	BackoffMin, BackoffMax time.Duration
	Jitter                 float64
	// Rand returns a number in [0, 1) for the jitter; nil means math/rand.
	Rand func() float64
	// OnState is called when the state of the search changes: false while
	// no Citadel was found ("Cidadela não encontrada"), true when one was.
	OnState func(found bool)
	// AskOthers, after a pass that found no Citadel, hands out one machine
	// of the Guild that is not the known Citadel (the configured addresses
	// and the cache, in turn), only as a hint: the Citadel of a newer epoch
	// proves itself with the certificate of the welcome, and any other
	// machine answers 4007 with the certificate it follows. It is how a
	// Tower finds the Citadel after a change of Citadel when the broadcast
	// does not reach it (docs/conectividade/descoberta.md#checagem-de-identidade).
	AskOthers bool
}

// Target is the Citadel the Locator found.
type Target struct {
	// Addr is "host:port" of the WSS port.
	Addr      string
	MachineID string
	Source    Source
}

// Locator finds the Citadel, keeps its address, and hands it to the protocol:
// Target is what protocol.TransportDialer asks, Rediscover is the hook the
// Tower calls after three failures in a row, and Connected is called when a
// connection opens, to update the cache.
type Locator struct {
	cfg LocatorConfig

	mu     sync.Mutex
	target Target
	found  bool
	known  bool   // OnState was called at least once
	asked  string // the address of the last hint, so the next one is another
	// hint is the target of a higher epoch the last pass took only as a
	// hint, with the epoch known then; refuted are the hints that did not
	// prove it (the 4007 that came brought no newer certificate), skipped
	// until their time.
	hint    *pendingHint
	refuted map[string]refutation
}

type pendingHint struct {
	addr  string
	epoch uint64
}

type refutation struct {
	until time.Time
	delay time.Duration
}

// Backoff of a refuted hint: 30 s, doubling to 30 min.
const (
	hintBackoffMin = 30 * time.Second
	hintBackoffMax = 30 * time.Minute
)

// ErrRefutedHint: the address was a hint of a higher epoch that did not
// prove it the last time; it is skipped for a while.
var ErrRefutedHint = errors.New("discovery: a hint of a higher epoch that did not hold, skipped for now")

// NewLocator checks cfg.
func NewLocator(cfg LocatorConfig) (*Locator, error) {
	if cfg.Clock == nil || cfg.Known == nil || cfg.Verifier == nil {
		return nil, errors.New("discovery: LocatorConfig needs Clock, Known and Verifier")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Cache == nil {
		cfg.Cache = NewMemCache()
	}
	if cfg.CacheMaxAge <= 0 {
		cfg.CacheMaxAge = DefaultCacheMaxAge
	}
	if cfg.VerifyTimeout <= 0 {
		cfg.VerifyTimeout = DefaultVerifyTimeout
	}
	if cfg.BackoffMin <= 0 {
		cfg.BackoffMin = DefaultBackoffMin
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = DefaultBackoffMax
	}
	if cfg.Jitter <= 0 {
		cfg.Jitter = DefaultJitter
	}
	if cfg.Rand == nil {
		cfg.Rand = rand.Float64
	}
	cfg.Search.Clock = cfg.Clock
	if cfg.Search.Logger == nil {
		cfg.Search.Logger = cfg.Logger
	}
	return &Locator{cfg: cfg}, nil
}

// Target is the address of the Citadel for the dialer of the protocol, with
// the route of the engines. It is empty until Find or Run succeeded.
func (l *Locator) Target() (addr, route string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.target.Addr, transport.RouteTower
}

// Current is the Citadel found last, if any.
func (l *Locator) Current() (Target, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.target, l.found
}

// Rediscover is the hook of protocol.Hooks.Rediscover: it looks for the
// Citadel until it is found or ctx is done. The protocol calls it when the
// target did not work (three failures, or a 4007); if that target was only a
// hint of a higher epoch and the epoch known has not grown since (the 4007
// brought no newer certificate), the hint is refuted: it is skipped for 30 s,
// doubling to 30 min each time it fails again, so a forged answer cannot
// keep the machine away from the real Citadel.
func (l *Locator) Rediscover(ctx context.Context) {
	l.refuteHint()
	if _, err := l.Run(ctx); err != nil && ctx.Err() == nil {
		l.cfg.Logger.Error("discovery: rediscovery ended", "err", err)
	}
}

// Connected records that a connection to the current Citadel opened: the
// cache learns the address worked, and addresses unused for 30 days leave
// it. It is the function behind protocol.Hooks.OnConnect.
func (l *Locator) Connected(ctx context.Context) {
	t, ok := l.Current()
	if !ok {
		return
	}
	l.mu.Lock()
	if l.hint != nil && l.hint.addr == t.Addr {
		// the connection opened: the welcome proved the epoch
		delete(l.refuted, t.Addr)
		l.hint = nil
	}
	l.mu.Unlock()
	now := l.cfg.Clock.Now()
	if err := l.cfg.Cache.Success(ctx, t.Addr, t.MachineID, t.Source, now); err != nil {
		l.cfg.Logger.Error("discovery: updating the cache", "err", err)
		return
	}
	if err := l.cfg.Cache.Prune(ctx, now.Add(-l.cfg.CacheMaxAge)); err != nil {
		l.cfg.Logger.Error("discovery: pruning the cache", "err", err)
	}
}

// MachineAddrs are the addresses of a machine as guild.config carries them.
type MachineAddrs struct {
	MachineID string
	Addrs     []string
	Port      int
}

// RememberMachines puts the addresses of the machines of guild.config in the
// cache, for a machine outside the local network to try them after a change
// of Citadel.
func (l *Locator) RememberMachines(ctx context.Context, ms []MachineAddrs) error {
	now := l.cfg.Clock.Now()
	for _, m := range ms {
		for _, a := range m.Addrs {
			e := CacheEntry{Addr: hostPort(a, m.Port), MachineID: m.MachineID, Source: SourceGuildConfig, AddedAt: now}
			if err := l.cfg.Cache.Add(ctx, e); err != nil {
				return err
			}
		}
	}
	return nil
}

// Run repeats Find, with the backoff of the reconnection, until a Citadel is
// found or ctx is done.
func (l *Locator) Run(ctx context.Context) (Target, error) {
	delay := l.cfg.BackoffMin
	for {
		t, err := l.Find(ctx)
		if err == nil {
			return t, nil
		}
		if ctx.Err() != nil {
			return Target{}, ctx.Err()
		}
		if !errors.Is(err, ErrNotFound) {
			return Target{}, err
		}
		wait := time.Duration(float64(delay) * (1 + (2*l.cfg.Rand()-1)*l.cfg.Jitter))
		delay = min(delay*2, l.cfg.BackoffMax)
		if err := clock.Sleep(ctx, l.cfg.Clock, wait); err != nil {
			return Target{}, err
		}
	}
}

// Find makes one pass through the order of the documentation: the configured
// address, the cache, then the broadcast and mDNS search; each address is
// checked by TLS and the first that passes is the Citadel. It returns
// ErrNotFound when none does.
func (l *Locator) Find(ctx context.Context) (Target, error) {
	tried := map[string]bool{}
	var hint *pendingHint // set when the target passed only as a hint
	try := func(c Candidate) (Target, bool) {
		if tried[c.Addr] {
			return Target{}, false
		}
		tried[c.Addr] = true
		id, h, err := l.check(ctx, c)
		if err != nil {
			l.cfg.Logger.Info("discovery: address discarded", "addr", c.Addr, "source", c.Source, "err", err)
			return Target{}, false
		}
		hint = h
		return Target{Addr: c.Addr, MachineID: id, Source: c.Source}, true
	}
	done := func(t Target) (Target, error) {
		now := l.cfg.Clock.Now()
		if err := l.cfg.Cache.Success(ctx, t.Addr, t.MachineID, t.Source, now); err != nil {
			l.cfg.Logger.Error("discovery: updating the cache", "err", err)
		}
		l.mu.Lock()
		l.hint = hint
		l.mu.Unlock()
		l.setFound(t)
		return t, nil
	}

	for _, a := range l.cfg.Configured {
		if t, ok := try(Candidate{Addr: withDefaultPort(a), Source: SourceConfigured}); ok {
			return done(t)
		}
		if ctx.Err() != nil {
			return Target{}, ctx.Err()
		}
	}
	for _, c := range l.cacheCandidates(ctx) {
		if t, ok := try(c); ok {
			return done(t)
		}
		if ctx.Err() != nil {
			return Target{}, ctx.Err()
		}
	}
	if !l.cfg.Disabled && (l.cfg.Search.Net != nil || l.cfg.Search.Browser != nil) {
		sc := l.cfg.Search
		sc.GuildID = l.guildID()
		seen, err := Search(ctx, sc)
		if err != nil {
			return Target{}, err
		}
		for _, c := range CandidatesFrom(seen, l.cfg.Known()) {
			if t, ok := try(c); ok {
				return done(t)
			}
			if ctx.Err() != nil {
				return Target{}, ctx.Err()
			}
		}
	}
	if l.cfg.AskOthers {
		if t, ok := l.askOther(ctx); ok {
			l.mu.Lock()
			l.hint = nil // askOther rotates by itself
			l.mu.Unlock()
			l.setFound(t)
			return t, nil
		}
	}
	l.setNotFound()
	return Target{}, ErrNotFound
}

// askOther picks, after the last one it gave, the next address of a machine
// of the Guild that answers the TLS check, whoever it is.
func (l *Locator) askOther(ctx context.Context) (Target, bool) {
	var addrs []string
	source := map[string]Source{} // where each address came from
	add := func(a string, src Source) {
		if _, ok := source[a]; !ok {
			source[a] = src
			addrs = append(addrs, a)
		}
	}
	for _, a := range l.cfg.Configured {
		add(withDefaultPort(a), SourceConfigured)
	}
	if es, err := l.cfg.Cache.List(ctx); err == nil {
		SortForTrying(es)
		for _, e := range es {
			add(e.Addr, SourceCache)
		}
	}
	if len(addrs) == 0 {
		return Target{}, false
	}
	l.mu.Lock()
	start := 0
	for i, a := range addrs {
		if a == l.asked {
			start = i + 1
			break
		}
	}
	l.mu.Unlock()
	for k := range addrs {
		a := addrs[(start+k)%len(addrs)]
		vctx, cancel := clock.WithTimeout(ctx, l.cfg.Clock, l.cfg.VerifyTimeout)
		id, err := l.cfg.Verifier.Verify(vctx, a)
		cancel()
		if err != nil {
			continue
		}
		l.mu.Lock()
		l.asked = a
		l.mu.Unlock()
		l.cfg.Logger.Info("discovery: asking another machine of the Guild for the Citadel", "addr", a, "machine", id)
		return Target{Addr: a, MachineID: id, Source: source[a]}, true
	}
	return Target{}, false
}

func (l *Locator) guildID() string { return l.cfg.Search.GuildID }

// cacheCandidates lists the cache as the discovery tries it, without the
// entries too old or known to be another machine than the Citadel.
func (l *Locator) cacheCandidates(ctx context.Context) []Candidate {
	es, err := l.cfg.Cache.List(ctx)
	if err != nil {
		l.cfg.Logger.Error("discovery: reading the cache", "err", err)
		return nil
	}
	cutoff := l.cfg.Clock.Now().Add(-l.cfg.CacheMaxAge)
	known := l.cfg.Known()
	var live []CacheEntry
	for _, e := range es {
		if e.activity().Before(cutoff) {
			continue
		}
		if e.MachineID != "" && known.CitadelMachineID != "" && e.MachineID != known.CitadelMachineID {
			continue
		}
		live = append(live, e)
	}
	SortForTrying(live)
	out := make([]Candidate, 0, len(live))
	for _, e := range live {
		out = append(out, Candidate{Addr: e.Addr, Source: SourceCache, MachineID: e.MachineID})
	}
	return out
}

// check is the identity check of one address: TLS to the Guild CA with the
// identity of a machine, and that machine has to be the Citadel of the
// highest epoch certificate. A hint from a higher epoch than the one known
// is let through: the welcome of the protocol brings the certificate that
// proves it, and the protocol refuses the connection if it does not.
//
// A hint must lead to the machine it named, and one refuted lately is
// skipped without opening TLS (Rediscover).
func (l *Locator) check(ctx context.Context, c Candidate) (string, *pendingHint, error) {
	known := l.cfg.Known()
	isHint := known.CitadelMachineID != "" && c.Epoch > known.Epoch && c.MachineID != known.CitadelMachineID
	if isHint {
		l.mu.Lock()
		r, ok := l.refuted[c.Addr]
		l.mu.Unlock()
		if ok && l.cfg.Clock.Now().Before(r.until) {
			return "", nil, fmt.Errorf("%w: %s until %s", ErrRefutedHint, c.Addr, r.until.Format(time.RFC3339))
		}
	}
	vctx, cancel := clock.WithTimeout(ctx, l.cfg.Clock, l.cfg.VerifyTimeout)
	defer cancel()
	id, err := l.cfg.Verifier.Verify(vctx, c.Addr)
	if err != nil {
		return "", nil, err
	}
	switch {
	case known.CitadelMachineID == "" || id == known.CitadelMachineID:
		return id, nil, nil
	case c.Epoch > known.Epoch:
		if c.MachineID != "" && id != c.MachineID {
			l.refute(c.Addr)
			return "", nil, fmt.Errorf("%w: %s is %s, not %s as the answer of epoch %d said",
				ErrNotTheCitadel, c.Addr, id, c.MachineID, c.Epoch)
		}
		return id, &pendingHint{addr: c.Addr, epoch: known.Epoch}, nil
	}
	return "", nil, fmt.Errorf("%w: %s is %s and the certificate of epoch %d names %s",
		ErrNotTheCitadel, c.Addr, id, known.Epoch, known.CitadelMachineID)
}

// refuteHint marks the last hint as refuted when the epoch known has not
// grown since it was taken: the machine there was not the Citadel of a newer
// epoch. Each refusal in a row doubles the time it is skipped.
func (l *Locator) refuteHint() {
	l.mu.Lock()
	h := l.hint
	l.hint = nil
	l.mu.Unlock()
	if h == nil || l.cfg.Known().Epoch > h.epoch {
		return
	}
	l.refute(h.addr)
}

// refute skips the hint at addr for a while, twice as long as the last time.
func (l *Locator) refute(addr string) {
	now := l.cfg.Clock.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.refuted == nil {
		l.refuted = map[string]refutation{}
	}
	for a, r := range l.refuted {
		if now.Sub(r.until) > 2*hintBackoffMax {
			delete(l.refuted, a) // long forgiven
		}
	}
	r := l.refuted[addr]
	r.delay = min(max(r.delay*2, hintBackoffMin), hintBackoffMax)
	r.until = now.Add(r.delay)
	l.refuted[addr] = r
	l.cfg.Logger.Info("discovery: a hint of a higher epoch did not hold", "addr", addr, "skipped_for", r.delay)
}

func (l *Locator) setFound(t Target) { l.setState(true, t) }

func (l *Locator) setNotFound() { l.setState(false, Target{}) }

func (l *Locator) setState(found bool, t Target) {
	l.mu.Lock()
	changed := !l.known || l.found != found
	l.known, l.found = true, found
	if found {
		l.target = t
	}
	l.mu.Unlock()
	if changed && l.cfg.OnState != nil {
		l.cfg.OnState(found)
	}
}

// NotFound says whether the last search ended without a Citadel.
func (l *Locator) NotFound() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.known && !l.found
}
