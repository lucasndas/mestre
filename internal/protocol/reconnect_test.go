package protocol_test

import (
	"context"
	"errors"
	"slices"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/protocol"
)

// CA-DUBLES-08
func TestTheTowerReconnectsWithTheConfiguredBackoffMeasuredInFakeTime(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		rediscovered := 0
		w.cfgT.Hooks.Rediscover = func(context.Context) { rediscovered++ }
		w.start()
		defer w.stop()
		settle()
		if !w.toTower().Connected() {
			t.Fatal("not connected")
		}

		// the connection is cut: the first attempt is 1 s later and works
		w.net.Cut(towerHost, citadelID)
		advance(10 * time.Second)
		if !w.toTower().Connected() {
			t.Fatal("did not reconnect")
		}
		if g := w.gaps(); len(g) != 1 || g[0] != time.Second {
			t.Fatalf("gaps after a cut = %v, want [1s]", g)
		}

		// the Citadel is unreachable: 1 s, doubling to 60 s
		w.net.Partition(towerHost, citadelID)
		advance(10 * time.Minute)
		// reconnected at 1 s, partitioned at 10 s: the first attempt is 1 s
		// after the partition (10 s after the last dial), then 2, 4, ... 60 s
		want := []time.Duration{10 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, 60 * time.Second, 60 * time.Second}
		if g := w.gaps(); len(g) < 1+len(want) || !slices.Equal(g[1:1+len(want)], want) {
			t.Fatalf("gaps = %v, want [1s] followed by %v", g, want)
		}
		// after 3 failures in a row it looks for the Guild again, then at every 3
		if rediscovered < 3 {
			t.Fatalf("rediscovered %d times", rediscovered)
		}

		// and it connects at the next attempt after the network heals
		w.net.Heal(towerHost, citadelID)
		advance(2 * time.Minute)
		if !w.toTower().Connected() {
			t.Fatal("did not reconnect after the heal")
		}
	})
}

func TestTheBackoffVariesByTwentyPercent(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		rnd  float64
		want time.Duration
	}{
		{"lowest", 0, 800 * time.Millisecond},
		{"middle", 0.5, time.Second},
		{"highest", 0.9999999, 1200 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			synctest.Test(t, func(t *testing.T) {
				w := newWorld(t, opts{})
				w.cfgT.Rand = func() float64 { return tc.rnd }
				w.start()
				defer w.stop()
				settle()
				w.net.Cut(towerHost, citadelID)
				advance(5 * time.Second)
				g := w.gaps()
				if len(g) != 1 || g[0] < tc.want-time.Millisecond || g[0] > tc.want+time.Millisecond {
					t.Fatalf("gaps = %v, want about %v", g, tc.want)
				}
			})
		})
	}
}

func TestAConnectionThatWentHalfOpenIsGivenAsDeadWhenThePongDoesNotComeIn45Seconds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.cfgC.Timing.Ping = 24 * time.Hour // only the Tower pings: the test is about its detection
		type ended struct {
			at    time.Time
			cause error
		}
		gone := make(chan ended, 4)
		w.cfgT.Hooks.OnDisconnect = func(_ *protocol.Session, cause error) { gone <- ended{w.clk.Now(), cause} }
		w.start()
		defer w.stop()
		settle()
		t0 := w.clk.Now()
		w.net.HalfOpen(towerHost, citadelID) // nothing arrives, nothing says so
		advance(2 * time.Minute)
		select {
		case e := <-gone:
			if !errors.Is(e.cause, protocol.ErrDead) {
				t.Fatalf("cause = %v, want ErrDead", e.cause)
			}
			// ping at 15 s, no pong by 15 + 45 s
			if d := e.at.Sub(t0); d != 60*time.Second {
				t.Fatalf("declared dead after %v, want 60s", d)
			}
		default:
			t.Fatal("a half-open connection was never noticed")
		}
		w.net.Heal(towerHost, citadelID)
		advance(2 * time.Minute)
		if !w.toTower().Connected() {
			t.Fatal("did not come back")
		}
	})
}

func TestAHealthyConnectionSurvivesManyPings(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.start()
		defer w.stop()
		settle()
		advance(30 * time.Minute)
		if g := w.gaps(); len(g) != 0 {
			t.Fatalf("reconnected %d times on a healthy connection", len(g))
		}
	})
}

func TestTheTowerSendsAHeartbeatNowAndEvery30Seconds(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		var seen atomic.Int32
		w.cfgC.Router.HandleFunc(protocol.TypeMachineHeartbeat, func(_ context.Context, m *protocol.Message) (*protocol.Reply, error) {
			if hb := m.Payload.(*protocol.MachineHeartbeat); hb.DiskFreeBytes != 1<<30 || hb.Terminals != 2 {
				t.Errorf("heartbeat = %+v", hb)
			}
			seen.Add(1)
			return nil, nil
		})
		w.cfgT.Heartbeat = func(context.Context) (protocol.MachineHeartbeat, error) {
			return protocol.MachineHeartbeat{CPUPct: 10, MemPct: 20, DiskFreeBytes: 1 << 30, Terminals: 2}, nil
		}
		w.start()
		defer w.stop()
		settle()
		if seen.Load() != 1 {
			t.Fatalf("heartbeats at the start = %d, want 1", seen.Load())
		}
		advance(95 * time.Second)
		if seen.Load() != 4 { // 0 s, 30 s, 60 s, 90 s
			t.Fatalf("heartbeats after 95 s = %d, want 4", seen.Load())
		}
	})
}

func TestTheCitadelConsidersATowerOfflineAfter90SecondsWithoutHeartbeat(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		var lost atomic.Int32
		var at atomic.Int64
		w.cfgC.Hooks.HeartbeatLost = func(p protocol.Peer) {
			if p.ID != towerID {
				t.Errorf("peer = %v", p)
			}
			lost.Add(1)
			at.Store(w.clk.Now().UnixNano())
		}
		// the Tower is connected and well, but sends no heartbeat
		w.cfgT.Heartbeat = func(context.Context) (protocol.MachineHeartbeat, error) {
			return protocol.MachineHeartbeat{}, errors.New("no numbers")
		}
		w.start()
		defer w.stop()
		settle()
		t0 := w.clk.Now()
		advance(89 * time.Second)
		if lost.Load() != 0 {
			t.Fatal("offline too soon")
		}
		advance(2 * time.Second)
		if lost.Load() != 1 {
			t.Fatalf("HeartbeatLost called %d times", lost.Load())
		}
		if d := time.Unix(0, at.Load()).Sub(t0); d != 90*time.Second {
			t.Fatalf("offline after %v, want 90s", d)
		}
	})
}

func TestAServerThatShutsDownMakesTheTowerReconnectSoon(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		causes := make(chan error, 4)
		w.cfgT.Hooks.OnDisconnect = func(_ *protocol.Session, cause error) { causes <- cause }
		w.start()
		defer w.stop()
		settle()
		w.server.Shutdown()
		advance(3 * time.Second)
		var ce *protocol.CloseError
		if err := <-causes; !errors.As(err, &ce) || ce.Code != protocol.CloseShuttingDown {
			t.Fatalf("cause = %v, want a close with 4005", err)
		}
		if !w.toTower().Connected() {
			t.Fatal("did not reconnect soon")
		}
	})
}

func TestANewConnectionOfTheSameMachineReplacesTheOldOne(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.startServer()
		defer w.stop()
		settle()
		first := rawTower(t, w)
		defer first.Close(1000, "")
		openRaw(t, w, first, protocol.DefaultRange())
		// The welcome leaves before the Citadel attaches the connection to
		// the link. Without waiting, the second connection could attach
		// first and the older one would replace the newer.
		settle()
		second := rawTower(t, w)
		defer second.Close(1000, "")
		openRaw(t, w, second, protocol.DefaultRange())
		settle()
		var ce *protocol.CloseError
		for {
			_, _, err := first.Read(t.Context())
			if err == nil {
				continue // a frame still in flight
			}
			if !errors.As(err, &ce) || ce.Code != protocol.CloseReplaced {
				t.Fatalf("first connection ended with %v, want 4004", err)
			}
			break
		}
		if !w.toTower().Connected() {
			t.Fatal("the new connection is not the current one")
		}
	})
}

func TestACitadelOnlyOpensConnectionsOfTheRoleOfItsProfile(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		w.startServer()
		defer w.stop()
		settle()
		c, err := w.nw.Dialer(towerHost, citadelAddr, protocol.Peer{Role: protocol.RoleGuardian, ID: towerID}).Dial(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close(1000, "")
		_, _, err = c.Read(t.Context())
		var ce *protocol.CloseError
		if !errors.As(err, &ce) || ce.Code != protocol.CloseBadCredential {
			t.Fatalf("Recv = %v, want a close with 4001", err)
		}
	})
}
