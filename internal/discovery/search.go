package discovery

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Timing of the search (docs/conectividade/descoberta.md#ordem-de-tentativa).
const (
	DefaultWindow   = 3 * time.Second
	DefaultRequests = 3
	DefaultInterval = time.Second
)

// MDNSService is the DNS-SD type the machines announce.
const MDNSService = "_mestre._tcp"

// MDNSEntry is one service instance that mDNS found.
type MDNSEntry struct {
	Instance string
	Host     string
	Addrs    []string
	Port     int
	TXT      map[string]string
}

// Browser asks mDNS (DNS-SD) for the instances of a service type.
type Browser interface {
	// Browse calls found for each instance until ctx is done, then returns.
	// found may be called from several goroutines.
	Browse(ctx context.Context, service string, found func(MDNSEntry)) error
}

// Seen is a machine of the Guild that answered the search.
type Seen struct {
	Source Source
	// Addrs are the IPs the machine answered from (broadcast) or announced
	// (mDNS); Port is the WSS port of its engine.
	Addrs     []string
	Port      int
	GuildID   string
	MachineID string
	Name      string
	Role      string
	Epoch     uint64
	// Citadel is the Citadel the machine follows, when it said so.
	Citadel  *CitadelRef
	Machines int
	CAFp     string
}

// SearchConfig is what the search needs. Net and Browser are optional: a
// search without them finds nothing by that way.
type SearchConfig struct {
	Net     PacketNetwork
	Browser Browser
	Clock   clock.Clock
	// GuildID is the Guild looked for; empty only to join one.
	GuildID string
	// Port is the discovery port; zero means DefaultPort.
	Port int
	// Window, Requests and Interval default to 3 s, 3 and 1 s.
	Window   time.Duration
	Requests int
	Interval time.Duration
	// Rand is the source of the nonce; nil means crypto/rand.
	Rand   io.Reader
	Logger *slog.Logger // nil discards
}

func (c *SearchConfig) prepare() error {
	if c.Clock == nil {
		return errors.New("discovery: SearchConfig needs a Clock")
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.Window <= 0 {
		c.Window = DefaultWindow
	}
	if c.Requests <= 0 {
		c.Requests = DefaultRequests
	}
	if c.Interval <= 0 {
		c.Interval = DefaultInterval
	}
	if c.Rand == nil {
		c.Rand = rand.Reader
	}
	if c.Logger == nil {
		c.Logger = slog.New(slog.DiscardHandler)
	}
	return nil
}

// Search asks the local network for the Guild by UDP broadcast and by mDNS,
// in parallel, and returns what answered when the window ends. The
// broadcast part sends Requests requests Interval apart to every broadcast
// address and keeps the answers that carry its nonce. A way that fails does
// not stop the other: the error is logged.
func Search(ctx context.Context, cfg SearchConfig) ([]Seen, error) {
	if err := cfg.prepare(); err != nil {
		return nil, err
	}
	wctx, cancel := clock.WithTimeout(ctx, cfg.Clock, cfg.Window)
	defer cancel()

	var (
		mu   sync.Mutex
		seen []Seen
		wg   sync.WaitGroup
	)
	add := func(s Seen) {
		mu.Lock()
		defer mu.Unlock()
		for _, o := range seen {
			if o.Source == s.Source && o.MachineID == s.MachineID && o.Port == s.Port {
				return
			}
		}
		seen = append(seen, s)
	}
	if cfg.Net != nil {
		wg.Go(func() {
			if err := broadcast(wctx, &cfg, add); err != nil && ctx.Err() == nil {
				cfg.Logger.Warn("discovery: broadcast search", "err", err)
			}
		})
	}
	if cfg.Browser != nil {
		wg.Go(func() {
			err := cfg.Browser.Browse(wctx, MDNSService, func(e MDNSEntry) {
				if s, ok := seenFromMDNS(e, cfg.GuildID); ok {
					add(s)
				}
			})
			if err != nil && wctx.Err() == nil {
				cfg.Logger.Warn("discovery: mDNS search", "err", err)
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return seen, nil
}

// broadcast sends the requests and collects the answers until the window
// context ends.
func broadcast(ctx context.Context, cfg *SearchConfig, add func(Seen)) error {
	conn, err := cfg.Net.Open()
	if err != nil {
		return err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	defer conn.Close()

	nonce, err := NewNonce(cfg.Rand)
	if err != nil {
		return err
	}
	req, err := Request{Nonce: nonce, GuildID: cfg.GuildID}.Encode()
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	wg.Go(func() {
		buf := make([]byte, 2*MaxMessage)
		failures := 0
		for {
			n, from, err := conn.ReadFrom(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				if failures++; failures >= maxReadFailures {
					return
				}
				continue
			}
			failures = 0
			if s, ok := seenFromResponse(buf[:n], from, nonce, cfg.GuildID); ok {
				add(s)
			}
		}
	})
	targets := cfg.Net.Broadcasts(cfg.Port)
	for i := range cfg.Requests {
		for _, to := range targets {
			if err := conn.WriteTo(req, to); err != nil {
				cfg.Logger.Debug("discovery: sending the request", "to", to, "err", err)
			}
		}
		if i == cfg.Requests-1 {
			break
		}
		if err := clock.Sleep(ctx, cfg.Clock, cfg.Interval); err != nil {
			break
		}
	}
	<-ctx.Done() // the answers keep arriving until the window ends
	_ = conn.Close()
	wg.Wait()
	return nil
}

// seenFromResponse reads an answer: only one with the nonce of the request,
// of the Guild asked for, counts.
func seenFromResponse(b []byte, from, nonce, guildID string) (Seen, bool) {
	r, err := DecodeResponse(b)
	if err != nil || r.Nonce != nonce || r.MachineID == "" {
		return Seen{}, false
	}
	if guildID != "" && r.GuildID != guildID {
		return Seen{}, false
	}
	host, _, err := net.SplitHostPort(from)
	if err != nil {
		return Seen{}, false
	}
	return Seen{
		Source: SourceBroadcast, Addrs: []string{host}, Port: r.Port, GuildID: r.GuildID,
		MachineID: r.MachineID, Name: r.Name, Role: r.Role, Epoch: r.Epoch,
		Citadel: r.Citadel, Machines: r.Machines, CAFp: r.CAFp,
	}, true
}

// seenFromMDNS reads the TXT record of an instance of _mestre._tcp.
func seenFromMDNS(e MDNSEntry, guildID string) (Seen, bool) {
	t := e.TXT
	if t["v"] != strconv.Itoa(Version) || t["machine_id"] == "" || t["guild_id"] == "" {
		return Seen{}, false
	}
	if guildID != "" && t["guild_id"] != guildID {
		return Seen{}, false
	}
	if t["role"] != RoleCitadel && t["role"] != RoleTower {
		return Seen{}, false
	}
	epoch, err := strconv.ParseUint(t["epoch"], 10, 64)
	if err != nil || e.Port < 1 || e.Port > 65535 {
		return Seen{}, false
	}
	var addrs []string
	for _, a := range e.Addrs {
		if usableAddr(a) {
			addrs = append(addrs, a)
		}
	}
	if len(addrs) == 0 {
		return Seen{}, false
	}
	return Seen{
		Source: SourceMDNS, Addrs: addrs, Port: e.Port, GuildID: t["guild_id"],
		MachineID: t["machine_id"], Name: t["instance"], Role: t["role"], Epoch: epoch,
	}, true
}

// usableAddr drops what cannot be dialed from another machine: unspecified
// addresses and IPv6 link-local ones (they need a zone).
func usableAddr(a string) bool {
	ip := net.ParseIP(a)
	return ip != nil && !ip.IsUnspecified() && !ip.IsLinkLocalUnicast() && !ip.IsMulticast()
}

// ByGuild groups the answers by guild_id, for the installer that asks with an
// empty guild_id to learn how many Guilds there are and whether the Citadel
// of each answered (docs/conectividade/descoberta.md#broadcast-udp).
func ByGuild(seen []Seen) map[string][]Seen {
	out := map[string][]Seen{}
	for _, s := range seen {
		out[s.GuildID] = append(out[s.GuildID], s)
	}
	return out
}

// CitadelAnswered says whether a Citadel is among the answers.
func CitadelAnswered(seen []Seen) bool {
	for _, s := range seen {
		if s.Role == RoleCitadel {
			return true
		}
	}
	return false
}
