//go:build full

package transport_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-PROTO-18, on Linux and on Windows: the same routes over real TCP
// sockets on 127.0.0.1.
func TestRealSocketsCarryTheMTLSRoutes(t *testing.T) {
	t.Parallel()
	g := newGuild(t, clock.Real{})
	cert, key := issueCert(t, g.ca, transport.KindMachine, transport.Names{
		MachineID: "citadel", Hostname: "localhost", IPs: []net.IP{net.ParseIP("127.0.0.1")},
	})
	g.citadel.Set(cert, key)
	sys := transport.SystemNetwork{Suite: os.Getenv("MESTRE_TEST_SUITE")}

	s, err := transport.NewServer(transport.ServerConfig{CA: g.ca.Certificate(), Cert: g.citadel, Credentials: g.creds})
	if err != nil {
		t.Fatal(err)
	}
	towers := s.Route(transport.RouteTower, transport.KindMachine)
	guardians := s.Route(transport.RouteGuardian, transport.KindGuardian)
	ln, err := sys.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ln) }()
	t.Cleanup(func() {
		_ = s.Close(transport.StatusGoingAway, "test over")
		if err := <-done; err != nil {
			t.Errorf("serve: %v", err)
		}
	})
	dialer := func(c *transport.CertSource) *transport.Client {
		return g.clientWith(t, transport.ClientConfig{Cert: c, Network: sys})
	}
	ctx := context.Background()

	cl, sv := connectAt(t, dialer(g.tower), towers, addr, transport.RouteTower)
	if sv.Peer().MachineID != "tower1" || cl.Peer().MachineID != "citadel" {
		t.Fatalf("peers: %+v / %+v", sv.Peer(), cl.Peer())
	}
	// The other side always reads while one writes (a socket buffer is
	// small on Windows): a message bigger than it needs a reader.
	data := bytes.Repeat([]byte{0x01, 0xfe}, 300<<10)
	type msg struct {
		typ transport.MessageType
		p   []byte
		err error
	}
	got := make(chan msg, 1)
	go func() {
		typ, p, err := sv.Read(ctx)
		got <- msg{typ, p, err}
	}()
	if err := cl.Write(ctx, transport.MessageBinary, data); err != nil {
		t.Fatal(err)
	}
	if m := <-got; m.err != nil || m.typ != transport.MessageBinary || !bytes.Equal(m.p, data) {
		t.Fatalf("binary over TCP: %v, %d bytes, %v", m.typ, len(m.p), m.err)
	}
	if err := sv.Write(ctx, transport.MessageText, []byte(`{"type":"welcome"}`)); err != nil {
		t.Fatal(err)
	}
	if _, p, err := cl.Read(ctx); err != nil || string(p) != `{"type":"welcome"}` {
		t.Fatalf("text over TCP: %q, %v", p, err)
	}

	gcl, _ := connectAt(t, dialer(g.guard), guardians, addr, transport.RouteGuardian)
	clErr, gclErr := readLoop(cl), readLoop(gcl)
	g.creds.RevokeMachine("tower1")
	if n := s.CloseRevoked(); n != 2 {
		t.Fatalf("CloseRevoked = %d, want 2", n)
	}
	for _, ch := range []<-chan error{clErr, gclErr} {
		if err := <-ch; transport.CloseStatus(err) != transport.StatusRevoked {
			t.Fatalf("read after revocation = %v, want 4002", err)
		}
	}
	if _, err := dialer(g.tower).Dial(ctx, addr, transport.RouteTower); err == nil {
		t.Fatal("a revoked machine reconnected over TCP")
	}

	// A standard TLS client that trusts the Guild CA (the browser after the
	// first access) downloads the CA without a client certificate.
	hc := &http.Client{Transport: &http.Transport{
		DialContext:     func(ctx context.Context, _, a string) (net.Conn, error) { return sys.DialContext(ctx, a) },
		TLSClientConfig: &tls.Config{RootCAs: g.ca.Pool(), MinVersion: tls.VersionTLS13},
	}}
	defer hc.CloseIdleConnections()
	resp, err := hc.Get("https://" + addr + transport.RouteCACert)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	der, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(der, g.ca.DER()) {
		t.Fatalf("/ca.crt over TCP: %d bytes", len(der))
	}
}

// The deadline of a context made by clock.WithTimeout is a time of the
// injected clock: the real dial must not read it as wall time, or a test clock
// that is behind makes every dial fail with an i/o timeout.
func TestDialIgnoresTheDeadlineOfAnInjectedClock(t *testing.T) {
	t.Parallel()
	sys := transport.SystemNetwork{Suite: os.Getenv("MESTRE_TEST_SUITE")}
	ln, err := sys.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	behind := clock.NewFake(time.Date(2001, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx, cancel := clock.WithTimeout(context.Background(), behind, time.Second)
	defer cancel()
	conn, err := sys.DialContext(ctx, ln.Addr().String())
	if err != nil {
		t.Fatalf("dial with a deadline that is long past in wall time: %v", err)
	}
	_ = conn.Close()
}
