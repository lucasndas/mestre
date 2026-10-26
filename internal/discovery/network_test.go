package discovery_test

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/discovery"
)

func TestTheBroadcastAddressOfEachInterfaceComesFromItsMask(t *testing.T) {
	t.Parallel()
	var got []string
	for _, a := range discovery.BroadcastsOf([]netip.Prefix{
		netip.MustParsePrefix("192.168.1.10/24"),
		netip.MustParsePrefix("10.20.17.5/20"),
		netip.MustParsePrefix("172.16.0.9/16"),
		netip.MustParsePrefix("192.168.1.77/24"), // the same network again
		netip.MustParsePrefix("127.0.0.1/8"),     // the loopback has no broadcast to find a machine
		netip.MustParsePrefix("10.0.0.2/31"),     // a point to point link
		netip.MustParsePrefix("fe80::1/64"),      // IPv6 has no broadcast
	}) {
		got = append(got, a.String())
	}
	if want := "192.168.1.255 10.20.31.255 172.16.255.255"; strings.Join(got, " ") != want {
		t.Fatalf("broadcasts = %v, want %s", got, want)
	}
}
