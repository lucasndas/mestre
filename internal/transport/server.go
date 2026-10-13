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
	"slices"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/lucasndas/mestre/pkg/clock"
)

// Routes of port 7443 (docs/arquitetura/componentes.md#portas).
const (
	RouteTower    = "/ws/tower"
	RouteGuardian = "/ws/guardian"
	RouteCACert   = "/ca.crt"
	RouteHealth   = "/healthz"
)

// ErrListenerClosed is returned by Accept after Close.
var ErrListenerClosed = errors.New("transport: listener closed")

// Listener yields the connections accepted on one mTLS route.
type Listener interface {
	// Accept waits for the next connection; ctx only bounds the wait.
	Accept(ctx context.Context) (Conn, error)
	// Close stops the route: new upgrades are refused with 503.
	Close() error
}

// ServerConfig wires a Server.
type ServerConfig struct {
	// CA is the Guild CA certificate, the only trusted root.
	CA *x509.Certificate
	// Cert is the engine certificate this machine serves.
	Cert *CertSource
	// Credentials is the revocation hook; nil revokes nothing.
	Credentials CredentialChecker
	Clock       clock.Clock  // nil means clock.Real
	Logger      *slog.Logger // nil discards
	// MaxMessageSize defaults to MaxMessageSize.
	MaxMessageSize int64
	Keepalive      Keepalive
}

// Server is the TLS server of the engine's port: the mTLS WebSocket routes,
// /ca.crt and /healthz, and any plain route added with Handle.
type Server struct {
	cfg ServerConfig
	tls *tls.Config
	mux *http.ServeMux
	hs  *http.Server

	ctx    context.Context // canceled by Close
	cancel context.CancelFunc

	mu    sync.Mutex
	conns map[*wsConn]struct{}
	lns   []*listener
}

// NewServer builds a server; Serve runs it.
func NewServer(cfg ServerConfig) (*Server, error) {
	if cfg.CA == nil || cfg.Cert == nil {
		return nil, errors.New("transport: ServerConfig needs CA and Cert")
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Server{
		cfg:    cfg,
		tls:    serverTLS(cfg.CA, cfg.Cert, cfg.Credentials, cfg.Clock),
		mux:    http.NewServeMux(),
		ctx:    ctx,
		cancel: cancel,
		conns:  map[*wsConn]struct{}{},
	}
	der := cfg.CA.Raw
	s.mux.HandleFunc("GET "+RouteCACert, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pkix-cert")
		w.Header().Set("Content-Disposition", `attachment; filename="mestre-ca.crt"`)
		_, _ = w.Write(der)
	})
	s.mux.HandleFunc("GET "+RouteHealth, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	s.hs = &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		ErrorLog:          slog.NewLogLogger(cfg.Logger.Handler(), slog.LevelInfo), // refused handshakes
		BaseContext:       func(net.Listener) context.Context { return ctx },
	}
	return s, nil
}

// Handle adds a plain HTTPS route (the panel, /join), outside mTLS.
func (s *Server) Handle(pattern string, h http.Handler) { s.mux.Handle(pattern, h) }

// Route opens the mTLS WebSocket route at path, for client certificates of
// the given kinds; the engine opens RouteTower for KindMachine and
// RouteGuardian for KindGuardian.
func (s *Server) Route(path string, kinds ...Kind) Listener {
	l := &listener{s: s, ch: make(chan *wsConn), closed: make(chan struct{})}
	s.mu.Lock()
	s.lns = append(s.lns, l)
	s.mu.Unlock()
	s.mux.HandleFunc("GET "+path, func(w http.ResponseWriter, r *http.Request) { s.upgrade(w, r, kinds, l) })
	return l
}

// Serve accepts connections on ln (plain TCP; TLS is done here) until Close.
func (s *Server) Serve(ln net.Listener) error {
	err := s.hs.Serve(tls.NewListener(ln, s.tls))
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) upgrade(w http.ResponseWriter, r *http.Request, kinds []Kind, l *listener) {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		http.Error(w, "client certificate required", http.StatusUnauthorized)
		return
	}
	p, err := checkPeer(r.TLS.PeerCertificates[0], kinds, s.cfg.Credentials)
	if err != nil {
		s.cfg.Logger.Warn("transport: route refused", "route", r.URL.Path, "remote", r.RemoteAddr, "err", err)
		http.Error(w, "certificate not allowed on this route", http.StatusForbidden)
		return
	}
	p.RemoteAddr = r.RemoteAddr
	select {
	case <-l.closed:
		http.Error(w, "route closed", http.StatusServiceUnavailable)
		return
	default:
	}
	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		s.cfg.Logger.Warn("transport: upgrade failed", "route", r.URL.Path, "remote", r.RemoteAddr, "err", err)
		return
	}
	c := newWSConn(ws, p, s.cfg.MaxMessageSize, s.cfg.Keepalive, s.cfg.Clock)
	if !s.track(c) {
		_ = c.Close(StatusGoingAway, "server closing")
		return
	}
	select {
	case l.ch <- c:
	case <-l.closed:
		_ = c.Close(StatusGoingAway, "route closed")
	case <-s.ctx.Done():
	case <-c.Done():
	}
}

func (s *Server) track(c *wsConn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ctx.Err() != nil {
		return false
	}
	s.conns[c] = struct{}{}
	go func() {
		<-c.Done()
		s.mu.Lock()
		delete(s.conns, c)
		s.mu.Unlock()
	}()
	return true
}

func (s *Server) open() []*wsConn {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*wsConn, 0, len(s.conns))
	for c := range s.conns {
		out = append(out, c)
	}
	return out
}

// CloseRevoked checks every open connection against the revocation hook
// again and closes the revoked ones with StatusRevoked (4002). The Guild
// calls it after a revocation; it returns how many were closed.
func (s *Server) CloseRevoked() int {
	if s.cfg.Credentials == nil {
		return 0
	}
	var wg sync.WaitGroup
	n := 0
	for _, c := range s.open() {
		if s.cfg.Credentials.CheckCredential(c.peer.Identity, c.peer.Serial) == nil {
			continue
		}
		n++
		wg.Go(func() { _ = c.Close(StatusRevoked, "credential revoked") })
	}
	wg.Wait()
	return n
}

// Close stops listening, closes every route and every open connection with
// code and reason, and returns when they are closed.
func (s *Server) Close(code StatusCode, reason string) error {
	s.mu.Lock()
	s.cancel()
	lns := slices.Clone(s.lns)
	s.mu.Unlock()
	err := s.hs.Close()
	for _, l := range lns {
		_ = l.Close()
	}
	var wg sync.WaitGroup
	for _, c := range s.open() {
		wg.Go(func() { _ = c.Close(code, reason) })
	}
	wg.Wait()
	if err != nil {
		return fmt.Errorf("transport: close server: %w", err)
	}
	return nil
}

type listener struct {
	s      *Server
	ch     chan *wsConn
	once   sync.Once
	closed chan struct{}
}

func (l *listener) Accept(ctx context.Context) (Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.closed:
		return nil, ErrListenerClosed
	case <-l.s.ctx.Done():
		return nil, ErrListenerClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *listener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}
