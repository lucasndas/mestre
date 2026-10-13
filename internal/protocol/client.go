package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/lucasndas/mestre/pkg/clock"
)

// CitadelKey is the key of the outbox of a Tower: its single peer is "the
// Citadel", whichever machine holds that role now, so the messages that wait
// for an ack follow a change of Citadel and are reconciled by the new one.
const CitadelKey = "citadel"

// Client is the Tower side: it keeps a connection to the current Citadel,
// opening it again, with backoff, whenever it ends.
type Client struct {
	cfg    *Config
	dialer Dialer
	link   *Link
}

// NewClient checks the configuration and returns a Client. Run starts it.
func NewClient(cfg Config, d Dialer) (*Client, error) {
	if err := cfg.prepare(); err != nil {
		return nil, err
	}
	if cfg.Hello == nil {
		return nil, fmt.Errorf("%w: Hello", ErrBadConfig)
	}
	c := &Client{cfg: &cfg, dialer: d}
	c.link = newLink(c.cfg, CitadelKey, SideTower)
	return c, nil
}

// Link is the relation with the Citadel; the application sends through it.
func (c *Client) Link() *Link { return c.link }

// Run keeps the connection until ctx is done or the credential is revoked
// (4002). Between attempts it waits 1 s, doubling to 60 s with a variation of
// 20%; a connection that opened resets the delay. After 3 failures in a row
// it asks Hooks.Rediscover to find the Guild again.
func (c *Client) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	defer wg.Wait()
	pctx, stop := context.WithCancel(ctx)
	defer stop()
	wg.Go(func() { pruneLoop(pctx, c.cfg) })

	b := newBackoff(c.cfg.Timing, c.cfg.Rand)
	failures := 0
	var peer Peer
	for {
		sess, err := c.connect(ctx)
		var cause error
		if err == nil {
			failures = 0
			b.reset()
			peer = sess.peer
			cause = sess.serve(ctx)
			if errors.Is(cause, ErrStaleEpoch) { // the Citadel we reached is an old one
				failures++
			}
		} else {
			failures++
			cause = err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		c.cfg.Logger.Info("protocol: connection to the Citadel ended", "cause", cause)
		if err := c.afterEnd(ctx, peer, cause); err != nil {
			return err
		}
		// Three failures in a row, or a machine of the Guild that answered it
		// is not the Citadel (4007, with the certificate): look for the
		// Citadel again.
		var notCitadel *NotCitadelError
		if (failures > 0 && failures%c.cfg.Timing.RediscoverAfter == 0) || errors.As(cause, &notCitadel) {
			if h := c.cfg.Hooks.Rediscover; h != nil {
				h(ctx)
			}
		}
		if err := clock.Sleep(ctx, c.cfg.Clock, b.next()); err != nil {
			return err
		}
	}
}

// afterEnd reacts to the way a connection ended. It returns an error to stop
// the Client.
func (c *Client) afterEnd(ctx context.Context, peer Peer, cause error) error {
	var ce *CloseError
	if errors.As(cause, &ce) {
		switch ce.Code {
		case CloseRevoked:
			if h := c.cfg.Hooks.Revoked; h != nil {
				h()
			}
			return fmt.Errorf("%w: %s", ErrRevoked, ce.Reason)
		case CloseIncompatible:
			if r, ok := ParseVersionReason(ce.Reason); ok {
				c.incompatible(r)
			}
		}
	}
	var ie *IncompatibleError
	if errors.As(cause, &ie) {
		c.incompatible(ie.Remote)
	}
	var re *remoteError
	if errors.As(cause, &re) && re.e.Code == CodeStaleEpoch {
		c.cfg.Epoch.Stale(ctx, re.peer)
	}
	return nil
}

func (c *Client) incompatible(remote Range) {
	if h := c.cfg.Hooks.Incompatible; h != nil {
		h(c.cfg.Profile.Versions, remote)
	}
}

// NotCitadelError is why a connection did not open: the machine reached is of
// the Guild but is not the Citadel (close 4007). The certificate it answered
// with is the current one, already followed if it was newer, and names the
// Citadel to look for (docs/conectividade/protocolo.md#códigos-de-fechamento).
type NotCitadelError struct {
	Peer        Peer
	Certificate Certificate
}

func (e *NotCitadelError) Error() string {
	return fmt.Sprintf("%v: %s is not the Citadel; epoch %d names %s", ErrOpening, e.Peer.ID, e.Certificate.Epoch, e.Certificate.CitadelMachineID)
}

// remoteError is an `error` message that came instead of the welcome.
type remoteError struct {
	e    *Error
	peer Peer
}

func (r *remoteError) Error() string {
	return fmt.Sprintf("%v: the Citadel answered %v", ErrOpening, r.e)
}

// connect dials and opens one connection.
func (c *Client) connect(ctx context.Context) (*Session, error) {
	dctx, cancel := clock.WithTimeout(ctx, c.cfg.Clock, c.cfg.Timing.Handshake)
	defer cancel()
	conn, err := c.dialer.Dial(dctx)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	s, err := c.open(ctx, conn)
	if err != nil {
		_ = conn.Close(1000, "")
		return nil, err
	}
	return s, nil
}

func (c *Client) open(ctx context.Context, conn Conn) (*Session, error) {
	cfg := c.cfg
	peer := conn.Peer()
	gen := c.link.gen
	hctx, cancel := clock.WithTimeout(ctx, cfg.Clock, cfg.Timing.Handshake)
	defer cancel()

	info, err := cfg.Hello(hctx)
	if err != nil {
		return nil, fmt.Errorf("%w: building the hello: %v", ErrOpening, err)
	}
	raw, err := cfg.Registry.Marshal(cfg.Profile.HelloType, Hello{
		Protocol: cfg.Profile.Versions, Capabilities: cfg.Capabilities, Version: info.Version, Sheet: info.Sheet,
		Certificate: cfg.Epoch.Certificate(), ReplicaLSN: info.ReplicaLSN, Terminals: info.Terminals,
	}, false)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpening, err)
	}
	hello, err := cfg.sendNow(hctx, conn, gen, cfg.Profile.HelloType, raw, "")
	if err != nil {
		return nil, fmt.Errorf("%w: sending the hello: %v", ErrOpening, err)
	}
	kind, data, err := conn.Read(hctx)
	if err != nil {
		var ce *CloseError
		if errors.As(err, &ce) && ce.Code == CloseIncompatible {
			r, _ := ParseVersionReason(ce.Reason)
			return nil, errors.Join(err, &IncompatibleError{Local: cfg.Profile.Versions, Remote: r})
		}
		return nil, fmt.Errorf("%w: waiting for the welcome: %w", ErrOpening, err)
	}
	if kind != FrameText {
		return nil, fmt.Errorf("%w: binary frame before the welcome", ErrOpening)
	}
	env, perr := Decode(data)
	if perr == nil {
		perr = env.CheckSender(peer)
	}
	if perr != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpening, perr)
	}
	if env.Type == TypeError {
		var e Error
		if err := json.Unmarshal(env.Payload, &e); err != nil {
			return nil, fmt.Errorf("%w: unreadable error: %v", ErrOpening, err)
		}
		return nil, &remoteError{e: &e, peer: peer}
	}
	if env.Type == TypeGuildEpoch && env.ReplyTo == hello.ID {
		return nil, c.notCitadel(hctx, peer, env)
	}
	if env.Type != cfg.Profile.WelcomeType || env.ReplyTo != hello.ID {
		return nil, fmt.Errorf("%w: expected %s answering %s, got %s answering %q", ErrOpening, cfg.Profile.WelcomeType, hello.ID, env.Type, env.ReplyTo)
	}
	payload, perr := cfg.Registry.Decode(env)
	if perr != nil {
		return nil, fmt.Errorf("%w: %v", ErrOpening, perr)
	}
	w := payload.(*Welcome)
	if w.Protocol < cfg.Profile.Versions.Min || w.Protocol > cfg.Profile.Versions.Max {
		_ = conn.Close(CloseIncompatible, VersionReason(cfg.Profile.Versions))
		return nil, &IncompatibleError{Local: cfg.Profile.Versions}
	}

	// The certificate of the welcome is the proof of the epoch of the
	// Citadel. An older one means the Citadel is stale: this Tower does not
	// talk to it. A newer one is followed.
	cert := *w.Certificate
	if err := cfg.Certs.Verify(cert); err != nil {
		_ = conn.Close(CloseBadCredential, "certificate refused")
		return nil, fmt.Errorf("%w: the certificate of the welcome was refused: %v", ErrOpening, err)
	}
	// The machine that answers has to be the one the certificate names:
	// being in the Guild is not being the Citadel.
	if cert.CitadelMachineID != peer.ID {
		_ = conn.Close(CloseBadCredential, "not the Citadel of the certificate")
		return nil, fmt.Errorf("%w: %s presented the certificate of epoch %d, which names %s", ErrOpening, peer.ID, cert.Epoch, cert.CitadelMachineID)
	}
	if cert.Epoch != env.Epoch {
		_ = conn.Close(1002, "epoch mismatch")
		return nil, fmt.Errorf("%w: the welcome says epoch %d and its certificate %d", ErrOpening, env.Epoch, cert.Epoch)
	}
	if mine := cfg.Epoch.Epoch(); cert.Epoch < mine {
		_ = conn.Close(CloseStaleEpoch, "the Citadel is stale")
		return nil, fmt.Errorf("%w: the Citadel %s is on epoch %d and this machine follows %d", ErrStaleEpoch, peer.ID, cert.Epoch, mine)
	}
	if perr := cfg.follow(hctx, cert, cert.Epoch); perr != nil {
		_ = conn.Close(CloseBadCredential, "certificate refused")
		return nil, fmt.Errorf("%w: %v", ErrOpening, perr)
	}
	if skew := w.CitadelTime.Sub(cfg.Clock.Now()); skew > ClockSkewLimit || -skew > ClockSkewLimit {
		if h := cfg.Hooks.ClockSkew; h != nil {
			h(skew)
		}
	}
	caps := cfg.Capabilities.Intersect(w.Capabilities)
	return newSession(cfg, c.link, conn, c.link.admit(), SideTower, w.Protocol, caps), nil
}

// notCitadel handles the answer of a machine that is not the Citadel: its
// certificate, if valid, is followed when newer.
func (c *Client) notCitadel(ctx context.Context, peer Peer, env Envelope) error {
	var cert Certificate
	if err := json.Unmarshal(env.Payload, &cert); err != nil {
		return fmt.Errorf("%w: unreadable certificate from %s: %v", ErrOpening, peer.ID, err)
	}
	if perr := c.cfg.follow(ctx, cert, cert.Epoch); perr != nil {
		return fmt.Errorf("%w: %v", ErrOpening, perr)
	}
	return &NotCitadelError{Peer: peer, Certificate: cert}
}

// pruneLoop forgets the processed ids older than the TTL.
func pruneLoop(ctx context.Context, cfg *Config) {
	t := cfg.Clock.NewTicker(cfg.Timing.ProcessedPrune)
	defer t.Stop()
	for {
		cutoff := cfg.Clock.Now().Add(-cfg.Timing.ProcessedTTL)
		if _, err := cfg.Processed.Prune(ctx, cutoff); err != nil && ctx.Err() == nil {
			cfg.Logger.Error("protocol: prune processed commands", "err", err)
		}
		select {
		case <-t.C():
		case <-ctx.Done():
			return
		}
	}
}
