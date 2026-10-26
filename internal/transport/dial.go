package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/coder/websocket"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// Dialer opens connections to a route of another machine.
type Dialer interface {
	// Dial connects to route ("/ws/tower") at addr ("host:port").
	Dial(ctx context.Context, addr, route string) (Conn, error)
}

// ClientConfig wires a Client.
type ClientConfig struct {
	// CA is the Guild CA certificate, the only trusted root.
	CA *x509.Certificate
	// Cert is this side's certificate: the engine's or the Guardian's.
	Cert    *CertSource
	Network Network
	// ServerKinds the server certificate may have; default KindMachine
	// (the engine of the Citadel). Which machine must be the Citadel is
	// decided above, by the epoch certificate.
	ServerKinds []Kind
	// Credentials is the revocation hook (the replicated revoked list);
	// nil revokes nothing.
	Credentials CredentialChecker
	Clock       clock.Clock  // nil means clock.Real
	Logger      *slog.Logger // nil discards
	// MaxMessageSize defaults to MaxMessageSize.
	MaxMessageSize int64
	Keepalive      Keepalive
}

// Client dials the mTLS WebSocket routes.
type Client struct{ cfg ClientConfig }

var _ Dialer = (*Client)(nil)

// NewClient checks cfg.
func NewClient(cfg ClientConfig) (*Client, error) {
	if cfg.CA == nil || cfg.Cert == nil || cfg.Network == nil {
		return nil, errors.New("transport: ClientConfig needs CA, Cert and Network")
	}
	if len(cfg.ServerKinds) == 0 {
		cfg.ServerKinds = []Kind{KindMachine}
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Client{cfg: cfg}, nil
}

// Dial implements Dialer.
func (c *Client) Dial(ctx context.Context, addr, route string) (Conn, error) {
	var peer Peer
	tcfg := clientTLS(c.cfg.CA, c.cfg.Cert, c.cfg.ServerKinds, c.cfg.Credentials, c.cfg.Clock, &peer)
	if host, _, err := net.SplitHostPort(addr); err == nil && net.ParseIP(host) == nil {
		tcfg.ServerName = host
	}
	tr := &http.Transport{
		DialTLSContext: func(ctx context.Context, _, a string) (net.Conn, error) {
			raw, err := c.cfg.Network.DialContext(ctx, a)
			if err != nil {
				return nil, err
			}
			tc := tls.Client(raw, tcfg)
			if err := tc.HandshakeContext(ctx); err != nil {
				_ = raw.Close()
				return nil, err
			}
			peer.RemoteAddr = raw.RemoteAddr().String()
			return tc, nil
		},
	}
	defer tr.CloseIdleConnections()
	hc := &http.Client{
		Transport: tr,
		// A Tower's port answers 307 to the browser; a machine never
		// follows it: finding the Citadel is discovery's job.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	ws, resp, err := websocket.Dial(ctx, "wss://"+addr+route, &websocket.DialOptions{
		HTTPClient:      hc,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("transport: dial %s%s: %s: %w", addr, route, resp.Status, err)
		}
		return nil, fmt.Errorf("transport: dial %s%s: %w", addr, route, err)
	}
	return newWSConn(ws, peer, c.cfg.MaxMessageSize, c.cfg.Keepalive, c.cfg.Clock), nil
}

// Prober checks who answers at an address without opening a route.
type Prober interface {
	// Probe opens TLS to addr ("host:port"), verifies the server and closes.
	Probe(ctx context.Context, addr string) (Peer, error)
}

var _ Prober = (*Client)(nil)

// Probe opens TLS to addr, verifies the server exactly as Dial does (chain to
// the Guild CA, the mestre:// identity of an allowed kind, the revocation
// hook) and closes the connection: it answers "which machine of the Guild is
// there?" for the discovery, which treats addresses as hints and trusts only
// this check (docs/conectividade/descoberta.md#checagem-de-identidade).
func (c *Client) Probe(ctx context.Context, addr string) (Peer, error) {
	var peer Peer
	tcfg := clientTLS(c.cfg.CA, c.cfg.Cert, c.cfg.ServerKinds, c.cfg.Credentials, c.cfg.Clock, &peer)
	if host, _, err := net.SplitHostPort(addr); err == nil && net.ParseIP(host) == nil {
		tcfg.ServerName = host
	}
	raw, err := c.cfg.Network.DialContext(ctx, addr)
	if err != nil {
		return Peer{}, fmt.Errorf("transport: probe %s: %w", addr, err)
	}
	defer raw.Close()
	stop := context.AfterFunc(ctx, func() { _ = raw.Close() })
	defer stop()
	tc := tls.Client(raw, tcfg)
	if err := tc.HandshakeContext(ctx); err != nil {
		return Peer{}, fmt.Errorf("transport: probe %s: %w", addr, err)
	}
	peer.RemoteAddr = raw.RemoteAddr().String()
	return peer, nil
}
