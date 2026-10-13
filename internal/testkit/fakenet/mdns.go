package fakenet

import (
	"context"
	"maps"
	"sync"
)

// Fake mDNS. A machine announces a service instance; another machine that
// browses the service type receives the instances announced now and, while
// it browses, the ones announced later. A partition between the two hosts and
// BlockMDNS (a network that does not forward multicast) hide the
// announcements.

// MDNSEntry is one announced service instance.
type MDNSEntry struct {
	Instance string            // the instance name of the DNS-SD record
	Service  string            // "_mestre._tcp"
	Host     string            // the host name ("citadel.local")
	Addrs    []string          // IPs; the address of the announcing host when empty
	Port     int               // the port of the service
	TXT      map[string]string // the TXT record
}

type mdnsState struct {
	entries []*announced
	subs    []*mdnsSub
	off     bool
}

type announced struct {
	host string
	e    MDNSEntry
}

type mdnsSub struct {
	from    string
	service string
	ch      chan MDNSEntry
	mu      sync.Mutex
	done    bool
}

func (s *mdnsSub) send(e MDNSEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.done {
		return
	}
	select {
	case s.ch <- e:
	default:
	}
}

// AnnounceMDNS announces e from host and returns the function that withdraws
// it.
func (n *Network) AnnounceMDNS(host string, e MDNSEntry) (withdraw func()) {
	n.mu.Lock()
	defer n.mu.Unlock()
	host = n.resolve(host)
	e.TXT = maps.Clone(e.TXT)
	a := &announced{host: host, e: e}
	n.mdns.entries = append(n.mdns.entries, a)
	if !n.mdns.off {
		for _, s := range n.mdns.subs {
			if s.service == e.Service && n.visible(s.from, host) {
				s.send(n.entryFor(a))
			}
		}
	}
	return func() {
		n.mu.Lock()
		defer n.mu.Unlock()
		for i, x := range n.mdns.entries {
			if x == a {
				n.mdns.entries = append(n.mdns.entries[:i:i], n.mdns.entries[i+1:]...)
				return
			}
		}
	}
}

// BlockMDNS hides (or shows again) every announcement of the network.
func (n *Network) BlockMDNS(blocked bool) {
	n.mu.Lock()
	n.mdns.off = blocked
	n.mu.Unlock()
}

// visible says whether from can hear host. The caller holds n.mu.
func (n *Network) visible(from, host string) bool {
	return from == host || !n.partitions[pair(from, host)]
}

// entryFor fills in the address of the announcing host. The caller holds n.mu.
func (n *Network) entryFor(a *announced) MDNSEntry {
	e := a.e
	e.TXT = maps.Clone(e.TXT)
	if len(e.Addrs) == 0 {
		e.Addrs = []string{n.addrOf(a.host)}
	}
	return e
}

// WatchMDNS browses service from this host until ctx is done: it yields the
// instances announced now and the ones announced later.
func (h Host) WatchMDNS(ctx context.Context, service string) <-chan MDNSEntry {
	n := h.n
	s := &mdnsSub{from: h.name, service: service, ch: make(chan MDNSEntry, 64)}
	n.mu.Lock()
	if !n.mdns.off {
		for _, a := range n.mdns.entries {
			if a.e.Service == service && n.visible(h.name, a.host) {
				s.send(n.entryFor(a))
			}
		}
	}
	n.mdns.subs = append(n.mdns.subs, s)
	n.mu.Unlock()
	context.AfterFunc(ctx, func() {
		n.mu.Lock()
		for i, x := range n.mdns.subs {
			if x == s {
				n.mdns.subs = append(n.mdns.subs[:i:i], n.mdns.subs[i+1:]...)
				break
			}
		}
		n.mu.Unlock()
		s.mu.Lock()
		s.done = true
		close(s.ch)
		s.mu.Unlock()
	})
	return s.ch
}
