package fakenet_test

import (
	"context"
	"errors"
	"io"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/pkg/clock"
)

// recv returns what the socket has received, without blocking.
func recv(t *testing.T, c *fakenet.PacketConn) []string {
	t.Helper()
	synctest.Wait()
	var out []string
	buf := make([]byte, 64)
	for {
		got := make(chan string, 1)
		go func() {
			n, from, err := c.ReadFrom(buf)
			if err != nil {
				got <- "err"
				return
			}
			got <- from + " " + string(buf[:n])
		}()
		synctest.Wait()
		select {
		case s := <-got:
			out = append(out, s)
		default:
			_ = c.Close() // wakes the reader that found nothing
			<-got
			return out
		}
	}
}

func TestABroadcastReachesEverySharedSocketOfEveryHostAndAUnicastOnlyOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := fakenet.New(clock.Real{})
		n.SetIP("a", "192.168.1.1")
		n.SetIP("b", "192.168.1.2")
		a1, _ := n.Host("a").ListenPacket(7445, true)
		a2, _ := n.Host("a").ListenPacket(7445, true)
		b1, _ := n.Host("b").ListenPacket(7445, true)
		if _, err := n.Host("b").ListenPacket(7445, false); !errors.Is(err, fakenet.ErrAddressInUse) {
			t.Fatalf("a socket that does not share took a shared port: %v", err)
		}
		src, _ := n.Host("b").ListenPacket(0, false)
		if err := src.WriteTo([]byte("hi"), "192.168.1.255:7445"); err != nil {
			t.Fatal(err)
		}
		if err := src.WriteTo([]byte("uni"), "192.168.1.1:7445"); err != nil {
			t.Fatal(err)
		}
		from := src.LocalAddr()
		if got := recv(t, a1); len(got) != 2 || got[0] != from+" hi" || got[1] != from+" uni" {
			t.Fatalf("a1 got %v", got)
		}
		if got := recv(t, a2); len(got) != 1 || got[0] != from+" hi" {
			t.Fatalf("a2 got %v: the unicast must reach one socket only", got)
		}
		if got := recv(t, b1); len(got) != 1 {
			t.Fatalf("the sender's host did not hear its own broadcast: %v", got)
		}
	})
}

func TestBroadcastCanBeBlockedLostOrPartitionedAway(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := fakenet.New(clock.Real{})
		n.SetIP("a", "192.168.1.1")
		rx, _ := n.Host("a").ListenPacket(7445, true)
		tx, _ := n.Host("b").ListenPacket(0, false)
		send := func() { _ = tx.WriteTo([]byte("x"), "255.255.255.255:7445") }

		n.BlockBroadcast(true)
		send()
		n.BlockBroadcast(false)
		n.LoseNextDatagrams(1)
		send()
		n.Partition("a", "b")
		send()
		n.Heal("a", "b")
		send()
		if got := recv(t, rx); len(got) != 1 {
			t.Fatalf("got %v, want only the last broadcast", got)
		}
		if n.Datagrams() != 4 {
			t.Fatalf("datagrams = %d", n.Datagrams())
		}
	})
}

func TestAnIPAnswersToDialsUntilTheMachineMoves(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := fakenet.New(clock.Real{})
		n.SetIP("citadel", "192.168.1.10")
		l, err := n.Listen("citadel:7443")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func() { _, _ = io.Copy(io.Discard, c) }()
			}
		}()
		c, err := n.Dial(t.Context(), "tower", "192.168.1.10:7443")
		if err != nil {
			t.Fatal(err)
		}
		n.SetIP("citadel", "192.168.1.99")
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Fatal("the connection survived the change of IP")
		}
		if _, err := n.Dial(t.Context(), "tower", "192.168.1.10:7443"); !errors.Is(err, fakenet.ErrConnectionRefused) {
			t.Fatalf("dial to the old IP = %v", err)
		}
		c2, err := n.Dial(t.Context(), "tower", "192.168.1.99:7443")
		if err != nil {
			t.Fatalf("dial to the new IP = %v", err)
		}
		_ = c2.Close()
	})
}

func TestMDNSAnnouncementsAreSeenNowAndLaterUnlessHidden(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := fakenet.New(clock.Real{})
		n.SetIP("citadel", "192.168.1.10")
		entry := func(inst string) fakenet.MDNSEntry {
			return fakenet.MDNSEntry{Instance: inst, Service: "_mestre._tcp", Port: 7443, TXT: map[string]string{"role": "cidadela"}}
		}
		n.AnnounceMDNS("citadel", entry("early"))
		n.AnnounceMDNS("citadel", fakenet.MDNSEntry{Instance: "other", Service: "_other._tcp"})
		ctx, cancel := context.WithCancel(t.Context())
		ch := n.Host("tower").WatchMDNS(ctx, "_mestre._tcp")
		synctest.Wait()
		e := <-ch
		if e.Instance != "early" || e.Addrs[0] != "192.168.1.10" || e.TXT["role"] != "cidadela" {
			t.Fatalf("entry = %+v", e)
		}
		n.AnnounceMDNS("citadel", entry("late"))
		if e := <-ch; e.Instance != "late" {
			t.Fatalf("entry = %+v", e)
		}
		n.BlockMDNS(true)
		n.AnnounceMDNS("citadel", entry("hidden"))
		n.BlockMDNS(false)
		withdraw := n.AnnounceMDNS("citadel", entry("gone"))
		<-ch
		withdraw()
		n.Partition("tower", "citadel")
		n.AnnounceMDNS("citadel", entry("partitioned"))
		cancel()
		synctest.Wait()
		if e, ok := <-ch; ok {
			t.Fatalf("got %+v after the browse ended", e)
		}
	})
}
