package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/mestre-ia/mestre/pkg/clock"
)

// Server is the Citadel side: it accepts the connections of the Towers (or
// of the Guardians, with their profile), opens each one, keeps one session
// per machine and a Link per machine that outlives the sessions.
type Server struct {
	cfg *Config

	mu       sync.Mutex
	links    map[string]*Link
	sessions map[*Session]struct{}
}

// NewServer checks the configuration and returns a Server.
func NewServer(cfg Config) (*Server, error) {
	if err := cfg.prepare(); err != nil {
		return nil, err
	}
	return &Server{cfg: &cfg, links: map[string]*Link{}, sessions: map[*Session]struct{}{}}, nil
}

// Link returns the relation with a machine, created on first use: a Citadel
// can send to a Tower that is not connected, and the message waits in the
// outbox.
func (srv *Server) Link(machineID string) *Link {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	l, ok := srv.links[machineID]
	if !ok {
		l = newLink(srv.cfg, machineID, SideCitadel)
		srv.links[machineID] = l
	}
	return l
}

// Serve accepts connections until ctx is done or the listener fails. It also
// keeps the processed ids and the temporary files of the transfers in check
// (pruneLoop).
func (srv *Server) Serve(ctx context.Context, l Listener) error {
	// the transfers the sessions began to finish are done (or canceled) before
	// Serve returns: the owner closes the databases and folders they write
	defer srv.drainTransfers()
	var wg sync.WaitGroup
	defer wg.Wait()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wg.Go(func() { pruneLoop(ctx, srv.cfg, srv.allLinks) })
	// The listener is closed before Serve returns, not only by the goroutine
	// that unblocks Accept: a caller that starts another Serve at the same
	// address right after this one returned (a restart, a promoted Citadel)
	// must not find it still taken.
	defer func() { _ = l.Close() }()
	go func() {
		<-ctx.Done()
		_ = l.Close()
	}()
	for {
		conn, err := l.Accept(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return fmt.Errorf("protocol: accept: %w", err)
		}
		wg.Go(func() { srv.handle(ctx, conn) })
	}
}

func (srv *Server) drainTransfers() {
	for _, l := range srv.allLinks() {
		l.xfer.drain()
	}
}

func (srv *Server) allLinks() []*Link {
	srv.mu.Lock()
	defer srv.mu.Unlock()
	out := make([]*Link, 0, len(srv.links))
	for _, l := range srv.links {
		out = append(out, l)
	}
	return out
}

// Shutdown closes every connection with 4005, so the Towers reconnect soon.
func (srv *Server) Shutdown() { srv.CloseAll(CloseShuttingDown, "the Citadel is shutting down") }

// CloseAll closes every connection with code: 4007 when this machine stops
// being the Citadel (docs/conectividade/protocolo.md#época).
func (srv *Server) CloseAll(code int, reason string) {
	srv.mu.Lock()
	sessions := make([]*Session, 0, len(srv.sessions))
	for s := range srv.sessions {
		sessions = append(sessions, s)
	}
	srv.mu.Unlock()
	for _, s := range sessions {
		s.Close(code, reason)
	}
}

func (srv *Server) handle(ctx context.Context, conn Conn) {
	s, err := srv.open(ctx, conn)
	if err != nil {
		srv.cfg.Logger.Info("protocol: opening refused", "peer", conn.Peer().ID, "err", err)
		return
	}
	srv.mu.Lock()
	srv.sessions[s] = struct{}{}
	srv.mu.Unlock()
	cause := s.serve(ctx)
	srv.mu.Lock()
	delete(srv.sessions, s)
	srv.mu.Unlock()
	srv.cfg.Logger.Info("protocol: connection ended", "peer", conn.Peer().ID, "cause", cause)
}

// open reads the hello, negotiates the version, checks the epoch and
// answers with the welcome. Anything wrong closes the connection with the
// code the protocol gives for it.
func (srv *Server) open(ctx context.Context, conn Conn) (*Session, error) {
	cfg := srv.cfg
	peer := conn.Peer()
	gen := NewIDGen(cfg.Clock)
	hctx, cancel := clock.WithTimeout(ctx, cfg.Clock, cfg.Timing.Handshake)
	defer cancel()

	if peer.Role != cfg.Profile.PeerRole || peer.ID == "" {
		_ = conn.Close(CloseBadCredential, "this credential cannot open this connection")
		return nil, fmt.Errorf("%w: peer %s %q is not allowed", ErrOpening, peer.Role, peer.ID)
	}
	kind, data, err := conn.Read(hctx)
	if err != nil {
		_ = conn.Close(1002, "no opening")
		return nil, fmt.Errorf("%w: reading the hello: %v", ErrOpening, err)
	}
	if kind != FrameText {
		_ = conn.Close(1002, "the opening is a text frame")
		return nil, fmt.Errorf("%w: binary frame before the hello", ErrOpening)
	}
	env, perr := Decode(data)
	if perr == nil {
		perr = env.CheckSender(peer)
	}
	if perr == nil && env.Type != cfg.Profile.HelloType {
		perr = NewError(CodeInvalidPayload, "the first message is %s, not %s", env.Type, cfg.Profile.HelloType)
	}
	if perr != nil {
		replyTo := ""
		if _, e := ParseULID(env.ID); e == nil {
			replyTo = env.ID
		}
		cfg.refuse(hctx, conn, gen, replyTo, perr, 1002, "bad opening")
		return nil, fmt.Errorf("%w: %v", ErrOpening, perr)
	}
	if _, perr := cfg.Registry.Decode(env); perr != nil {
		cfg.refuse(hctx, conn, gen, env.ID, perr, 1002, "bad opening")
		return nil, fmt.Errorf("%w: %v", ErrOpening, perr)
	}
	var hdr helloHeader
	_ = json.Unmarshal(env.Payload, &hdr) // the schema was checked above

	// Version: the highest in common, or 4003 with the range we accept.
	version, ok := Negotiate(cfg.Profile.Versions, hdr.Protocol)
	if !ok {
		_ = conn.Close(CloseIncompatible, VersionReason(cfg.Profile.Versions))
		return nil, &IncompatibleError{Local: cfg.Profile.Versions, Remote: hdr.Protocol}
	}

	// Epoch. A hello of an older epoch is not refused: the welcome carries the
	// certificate of the current one, which brings the Tower forward. A
	// higher one is proven by the certificate of the hello and followed
	// before anything else; a Citadel that followed it is no longer the
	// Citadel (4007, with the certificate).
	if env.Epoch > cfg.Epoch.Epoch() {
		if hdr.Certificate == nil {
			perr := NewError(CodeInvalidPayload, "epoch %d needs the certificate in the hello", env.Epoch)
			cfg.refuse(hctx, conn, gen, env.ID, perr, 1002, "bad opening")
			return nil, fmt.Errorf("%w: %v", ErrOpening, perr)
		}
		if perr := cfg.follow(ctx, *hdr.Certificate, env.Epoch); perr != nil {
			cfg.refuse(hctx, conn, gen, env.ID, perr, CloseBadCredential, "certificate refused")
			return nil, fmt.Errorf("%w: %v", ErrOpening, perr)
		}
	}
	if cfg.IsCitadel != nil && !cfg.IsCitadel() {
		if cert := cfg.Epoch.Certificate(); cert != nil {
			_, _ = cfg.sendNow(hctx, conn, gen, TypeGuildEpoch, jsonOf(cert), env.ID)
		}
		_ = conn.Close(CloseNotCitadel, "this machine is not the Citadel")
		return nil, fmt.Errorf("%w: this machine is not the Citadel", ErrOpening)
	}
	if h := cfg.Hooks.OnHello; h != nil && cfg.Profile.HelloType == TypeHello {
		var hello Hello
		_ = json.Unmarshal(env.Payload, &hello)
		if err := h(ctx, peer, &hello); err != nil {
			var ce *CloseError
			var pe *Error
			switch {
			case errors.As(err, &ce):
				_ = conn.Close(ce.Code, ce.Reason)
			case errors.As(err, &pe):
				cfg.refuse(hctx, conn, gen, env.ID, pe, 1002, "refused")
			default:
				cfg.refuse(hctx, conn, gen, env.ID, NewError(CodeForbidden, "%v", err), 1008, "refused")
			}
			return nil, fmt.Errorf("%w: refused: %v", ErrOpening, err)
		}
	}

	caps := cfg.Capabilities.Intersect(hdr.Capabilities)
	cert := cfg.Epoch.Certificate()
	if cert == nil {
		_ = conn.Close(1011, "no certificate")
		return nil, fmt.Errorf("%w: this machine has no certificate of epoch to answer with", ErrOpening)
	}
	raw, err := cfg.Registry.Marshal(cfg.Profile.WelcomeType, Welcome{
		Protocol: version, Capabilities: caps, CitadelVer: cfg.BinaryVersion,
		CitadelTime: Timestamp{cfg.Clock.Now().UTC()}, Certificate: cert,
	}, true)
	if err != nil {
		_ = conn.Close(1011, "internal")
		return nil, fmt.Errorf("%w: %v", ErrOpening, err)
	}
	// The order among the connections of the machine is fixed here, before
	// the welcome: from then on the Tower considers itself connected.
	link := srv.Link(peer.ID)
	order := link.admit()
	if _, err := cfg.sendNow(hctx, conn, gen, cfg.Profile.WelcomeType, raw, env.ID); err != nil {
		_ = conn.Close(1011, "internal")
		return nil, fmt.Errorf("%w: sending the welcome: %v", ErrOpening, err)
	}
	return newSession(cfg, link, conn, order, SideCitadel, version, caps), nil
}
