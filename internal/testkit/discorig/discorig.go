// Package discorig puts the discovery (internal/discovery) on the fake
// network of the fast suite (docs/qualidade/dubles.md#rede-falsa): the
// PacketNetwork of one machine, with the broadcast and the ports shared by
// the instances, and the mDNS Browser that hears the announcements of the
// fake network.
package discorig

import (
	"context"

	"github.com/mestre-ia/mestre/internal/discovery"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
)

// Packets is the discovery.PacketNetwork of a host of the fake network.
type Packets struct{ Host fakenet.Host }

var _ discovery.PacketNetwork = Packets{}

// ListenShared implements discovery.PacketNetwork.
func (p Packets) ListenShared(port int) (discovery.PacketConn, error) {
	return p.Host.ListenPacket(port, true)
}

// Open implements discovery.PacketNetwork.
func (p Packets) Open() (discovery.PacketConn, error) { return p.Host.ListenPacket(0, false) }

// Broadcasts implements discovery.PacketNetwork.
func (p Packets) Broadcasts(port int) []string { return p.Host.Broadcasts(port) }

// Browser is the discovery.Browser of a host of the fake network.
type Browser struct{ Host fakenet.Host }

var _ discovery.Browser = Browser{}

// Browse implements discovery.Browser: it yields what is announced now and
// what is announced while ctx lasts.
func (b Browser) Browse(ctx context.Context, service string, found func(discovery.MDNSEntry)) error {
	for e := range b.Host.WatchMDNS(ctx, service) {
		found(discovery.MDNSEntry{Instance: e.Instance, Host: e.Host, Addrs: e.Addrs, Port: e.Port, TXT: e.TXT})
	}
	return nil
}

// Announce announces one instance of the engine on the fake mDNS the way the
// Guardian does: a _mestre._tcp record with the TXT of the documentation.
// The returned function withdraws it.
func Announce(n *fakenet.Network, host string, a discovery.Announcement) (withdraw func()) {
	txt := map[string]string{}
	for _, kv := range a.TXT() {
		for i := range kv {
			if kv[i] == '=' {
				txt[kv[:i]] = kv[i+1:]
				break
			}
		}
	}
	return n.AnnounceMDNS(host, fakenet.MDNSEntry{
		Instance: txt["instance"] + "@" + host, Service: discovery.ServiceEngine, Host: host + ".local",
		Port: a.EnginePort, TXT: txt,
	})
}
