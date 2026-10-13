package fakenet_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"testing/synctest"
	"time"

	"github.com/lucasndas/mestre/internal/testkit/fakenet"
	"github.com/lucasndas/mestre/internal/testkit/testca"
	"github.com/lucasndas/mestre/pkg/clock"
)

func TestDialRefusedWithoutListener(t *testing.T) {
	t.Parallel()
	n := fakenet.New(clock.Real{})
	_, err := n.Dial(context.Background(), "tower", "citadel:7443")
	if !errors.Is(err, fakenet.ErrConnectionRefused) {
		t.Fatalf("err = %v", err)
	}
}

func TestEchoAndCloseGivesEOF(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		n := fakenet.New(clock.Real{})
		l, err := n.Listen("citadel:7443")
		if err != nil {
			t.Fatal(err)
		}
		defer l.Close()
		go func() {
			s, err := l.Accept()
			if err != nil {
				return
			}
			_, _ = io.Copy(s, s)
			_ = s.Close()
		}()
		c, err := n.Dial(t.Context(), "tower", "citadel:7443")
		if err != nil {
			t.Fatal(err)
		}
		if c.RemoteAddr().String() != "citadel:7443" {
			t.Fatalf("remote = %v", c.RemoteAddr())
		}
		_, _ = c.Write([]byte("ping"))
		buf := make([]byte, 4)
		if _, err := io.ReadFull(c, buf); err != nil || string(buf) != "ping" {
			t.Fatalf("read %q, %v", buf, err)
		}
		_ = c.Close()
		if _, err := c.Read(buf); err == nil {
			t.Fatal("read after close succeeded")
		}
	})
}

func TestLatencyFollowsTheClock(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		n := fakenet.New(clk)
		n.SetLatency(250 * time.Millisecond)
		l, _ := n.Listen("citadel:1")
		defer l.Close()
		go func() {
			c, err := n.Dial(t.Context(), "tower", "citadel:1")
			if err == nil {
				_, _ = c.Write([]byte("x"))
			}
		}()
		s, _ := l.Accept()
		start := clk.Now()
		buf := make([]byte, 1)
		if _, err := s.Read(buf); err != nil {
			t.Fatal(err)
		}
		if got := clock.Since(clk, start); got != 250*time.Millisecond {
			t.Fatalf("latency = %v", got)
		}
	})
}

// CA-DUBLES-01
func TestHeartbeatLossDetectedInFakeTime(t *testing.T) {
	t.Parallel()
	const interval, timeout = 30 * time.Second, 90 * time.Second
	wall := clock.Real{}
	realStart := wall.Now()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{} // fake time comes from the bubble
		n := fakenet.New(clk)
		l, _ := n.Listen("citadel:7443")
		defer l.Close()
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		go func() { // the other side: one heartbeat every 30 s
			c, err := n.Dial(ctx, "tower", "citadel:7443")
			if err != nil {
				return
			}
			defer c.Close()
			tk := clk.NewTicker(interval)
			defer tk.Stop()
			for {
				if _, err := c.Write([]byte("hb\n")); err != nil {
					return
				}
				select {
				case <-tk.C():
				case <-ctx.Done():
					return
				}
			}
		}()
		s, _ := l.Accept()
		defer s.Close()
		r := bufio.NewReader(s)
		var last time.Time
		for beats := 0; ; beats++ {
			if beats == 2 {
				n.HalfOpen("tower", "citadel") // stops answering, no FIN
			}
			_ = s.SetReadDeadline(clk.Now().Add(timeout))
			if _, err := r.ReadString('\n'); err != nil {
				var ne net.Error
				if !errors.As(err, &ne) || !ne.Timeout() || !errors.Is(err, os.ErrDeadlineExceeded) {
					t.Fatalf("read error %v, want timeout", err)
				}
				break
			}
			last = clk.Now()
		}
		if got := clock.Since(clk, last); got != timeout {
			t.Fatalf("loss detected %v after the last heartbeat, want %v", got, timeout)
		}
	})
	if real := clock.Since(wall, realStart); real >= 100*time.Millisecond {
		t.Fatalf("took %v of real time, want < 100ms", real)
	}
}

// towerLoop is a test-local reconnecting client with the protocol backoff
// (1 s doubling). The real Tower replaces it in M2 (conectividade/protocolo.md).
func towerLoop(ctx context.Context, n *fakenet.Network, clk clock.Clock, attempts chan<- time.Time) {
	wait := time.Second
	for ctx.Err() == nil {
		attempts <- clk.Now()
		c, err := n.Dial(ctx, "tower", "citadel:7443")
		if err == nil {
			_, _ = io.Copy(io.Discard, c) // until the connection drops
			wait = time.Second
		} else {
			wait *= 2
		}
		if clock.Sleep(ctx, clk, wait) != nil {
			return
		}
	}
}

// CA-DUBLES-08 (double side: the real Tower replaces towerLoop in M2)
func TestCutConnectionReconnectsWithBackoff(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.Real{}
		n := fakenet.New(clk)
		l, _ := n.Listen("citadel:7443")
		defer l.Close()
		ctx, cancel := context.WithCancel(t.Context())
		attempts := make(chan time.Time, 16)
		done := make(chan struct{})
		go func() { towerLoop(ctx, n, clk, attempts); close(done) }()
		go func() {
			for {
				c, err := l.Accept()
				if err != nil {
					return
				}
				go func() { _, _ = io.Copy(io.Discard, c) }()
			}
		}()
		t0 := <-attempts
		synctest.Wait()

		n.Cut("tower", "citadel")
		cut := clk.Now()
		if got := (<-attempts).Sub(cut); got != time.Second {
			t.Fatalf("reconnected %v after the cut, want 1s", got)
		}
		synctest.Wait()

		n.Partition("tower", "citadel")
		p := clk.Now()
		var offsets []time.Duration
		for range 3 { // 1 s after the cut, then 2 s and 4 s after each failed dial
			offsets = append(offsets, (<-attempts).Sub(p))
			if len(offsets) == 2 {
				n.Heal("tower", "citadel")
			}
		}
		want := []time.Duration{time.Second, 3 * time.Second, 7 * time.Second}
		for i := range want {
			if offsets[i] != want[i] {
				t.Fatalf("attempt offsets = %v, want %v (first at %v)", offsets, want, t0)
			}
		}
		cancel()
		_ = l.Close()
		n.Cut("tower", "citadel")
		<-done
	})
}

func TestHandlerTransportAndAbort(t *testing.T) {
	t.Parallel()
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cut" {
			panic(http.ErrAbortHandler)
		}
		w.WriteHeader(http.StatusTeapot)
	})
	client := &http.Client{Transport: fakenet.HandlerTransport(h)}
	resp, err := client.Get("http://example.invalid/ok")
	if err != nil || resp.StatusCode != http.StatusTeapot {
		t.Fatalf("resp = %v, %v", resp, err)
	}
	_ = resp.Body.Close()
	if _, err := client.Get("http://example.invalid/cut"); !errors.Is(err, fakenet.ErrConnectionReset) {
		t.Fatalf("err = %v, want connection reset", err)
	}
}

func TestMutualTLSOverFakeNetwork(t *testing.T) {
	t.Parallel()
	ca, err := testca.New()
	if err != nil {
		t.Fatal(err)
	}
	serverCert, _ := ca.Issue("citadel")
	clientCert, _ := ca.Issue("tower")
	synctest.Test(t, func(t *testing.T) {
		n := fakenet.New(clock.Real{})
		raw, _ := n.Listen("citadel:7443")
		l := tls.NewListener(raw, &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientCAs:    ca.Pool,
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS13,
		})
		defer l.Close()
		go func() {
			c, err := l.Accept()
			if err != nil {
				return
			}
			defer c.Close()
			tc := c.(*tls.Conn)
			if tc.Handshake() != nil {
				return
			}
			_, _ = c.Write([]byte("hello " + tc.ConnectionState().PeerCertificates[0].Subject.CommonName))
		}()
		rawc, err := n.Dial(t.Context(), "tower", "citadel:7443")
		if err != nil {
			t.Fatal(err)
		}
		c := tls.Client(rawc, &tls.Config{
			ServerName: "citadel", RootCAs: ca.Pool,
			Certificates: []tls.Certificate{clientCert}, MinVersion: tls.VersionTLS13,
		})
		defer c.Close()
		got, err := io.ReadAll(c)
		if err != nil || string(got) != "hello tower" {
			t.Fatalf("got %q, %v", got, err)
		}
	})
}
