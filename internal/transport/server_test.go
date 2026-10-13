package transport_test

import (
	"context"
	"net"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// The server holds at most MaxConns connections: one more waits until
// another closes.
func TestTheServerHoldsAtMostMaxConnsConnections(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		g.serveWith(t, transport.ServerConfig{MaxConns: 1})
		first, err := g.net.Host("browser").DialContext(context.Background(), citadelAddr)
		if err != nil {
			t.Fatal(err)
		}
		second := make(chan net.Conn, 1)
		go func() {
			c, err := g.net.Host("browser").DialContext(context.Background(), citadelAddr)
			if err != nil {
				t.Error(err)
			}
			second <- c
		}()
		synctest.Wait()
		select {
		case <-second:
			t.Fatal("a connection over the limit was taken")
		default:
		}
		_ = first.Close()
		c := <-second
		_ = c.Close()
	})
}

// An upgraded connection that nobody accepts is closed with 1013 after
// AcceptWait, instead of holding the handler forever.
func TestAnUpgradeNobodyAcceptsIsClosedAfterAcceptWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		g := newGuild(t, clock.Real{})
		g.serve(t)                  // the routes are open, but nobody calls Accept
		start := clock.Real{}.Now() // the bubble's fake time
		c, err := g.client(t, g.tower).Dial(context.Background(), citadelAddr, transport.RouteTower)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = c.Read(context.Background())
		if transport.CloseStatus(err) != transport.StatusTryAgainLater {
			t.Fatalf("read = %v, want close 1013", err)
		}
		if d := clock.Since(clock.Real{}, start); d < transport.DefaultAcceptWait {
			t.Fatalf("closed after %v, want %v", d, transport.DefaultAcceptWait)
		}
	})
}
