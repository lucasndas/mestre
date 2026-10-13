package transport_test

import (
	"context"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

func TestProbeSaysWhichMachineOfTheGuildIsAtTheAddress(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		g.serve(t)
		p, err := g.client(t, g.tower).Probe(context.Background(), citadelAddr)
		if err != nil || p.Kind != transport.KindMachine || p.MachineID != "citadel" || p.RemoteAddr != citadelAddr {
			t.Fatalf("Probe = %+v, %v", p, err)
		}

		// A server of another CA, a Guardian certificate where an engine is
		// expected, and nobody at the address are all refused.
		other := newGuild(t, clock.Real{})
		for name, tc := range map[string]struct {
			cert *transport.CertSource
			ca   *transport.CA
			want string
		}{
			"another CA":             {other.citadel, other.ca, "unknown authority"},
			"a Guardian certificate": {g.guard, g.ca, "not a [machine] certificate"},
		} {
			ln, err := g.net.Host(strings.ReplaceAll(name, " ", "-")).Listen(strings.ReplaceAll(name, " ", "-") + ":7443")
			if err != nil {
				t.Fatal(err)
			}
			srv, err := transport.NewServer(transport.ServerConfig{CA: tc.ca.Certificate(), Cert: tc.cert})
			if err != nil {
				t.Fatal(err)
			}
			go func() { _ = srv.Serve(ln) }()
			defer srv.Close(transport.StatusGoingAway, "")
			_, err = g.client(t, g.tower).Probe(context.Background(), ln.Addr().String())
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("%s: Probe = %v, want an error with %q", name, err, tc.want)
			}
		}
		if _, err := g.client(t, g.tower).Probe(context.Background(), "nobody:7443"); err == nil {
			t.Error("Probe found a machine where there is none")
		}
	})
}
