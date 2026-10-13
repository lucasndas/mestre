package discovery

import (
	"context"
	"errors"

	"github.com/brutella/dnssd"
)

// SystemBrowser is the DNS-SD query of the binary, on the multicast groups of
// the network interfaces (port 5353, shared with Avahi or the Windows
// responder). It only asks: the announcement of the services is the job of
// Avahi on Linux and of the Guardian on Windows
// (docs/conectividade/descoberta.md#mdns).
type SystemBrowser struct {
	// Suite is the value of MESTRE_TEST_SUITE ("" in production); the fast
	// suite is refused.
	Suite string
}

var _ Browser = SystemBrowser{}

// Browse implements Browser.
func (b SystemBrowser) Browse(ctx context.Context, service string, found func(MDNSEntry)) error {
	if b.Suite == "fast" {
		return ErrNetworkInFastSuite
	}
	// The library stops its reader goroutines only when its context is
	// canceled; a context that ended by a deadline would leave them spinning.
	// So the library gets a context that only a cancel can end.
	lctx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	stop := context.AfterFunc(ctx, cancel)
	defer stop()
	add := func(e dnssd.BrowseEntry) {
		m := MDNSEntry{Instance: e.Name, Host: e.Host, Port: e.Port, TXT: e.Text}
		for _, ip := range e.IPs {
			m.Addrs = append(m.Addrs, ip.String())
		}
		found(m)
	}
	err := dnssd.LookupType(lctx, service+".local.", add, func(dnssd.BrowseEntry) {})
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
