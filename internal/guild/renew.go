package guild

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// DefaultRenewTimeout is how long Renew waits for the new certificate.
const DefaultRenewTimeout = 30 * time.Second

// RenewerConfig wires a Renewer.
type RenewerConfig struct {
	// Kind is the certificate this renews: the engine's or the Guardian's.
	Kind transport.Kind
	// CA is the Guild CA certificate the new certificate must chain to.
	CA *x509.Certificate
	// Source holds the certificate in use; the new one is put in it, so new
	// handshakes use it and open connections stay up.
	Source *transport.CertSource
	// Names says which names the machine has now (its host name and the IPs
	// it has on the network); a change renews at once, and so does a
	// certificate of the Citadel (with mestre.local) on a machine that is a
	// Tower again.
	Names func() transport.Names
	// Link is the connection to the Citadel.
	Link Caller
	// Save keeps the new certificate and key where the machine keeps them.
	Save    func(ctx context.Context, cert *x509.Certificate, key *ecdsa.PrivateKey) error
	Clock   clock.Clock
	Timeout time.Duration
	Logger  *slog.Logger // nil discards
}

// Renewer renews the certificate of a machine over the connection it already
// has with the Citadel, 30 days before it expires and whenever its names
// change, with a new key and the same identity (cert.renew, cert.issued;
// docs/guilda/guilda.md#credenciais).
type Renewer struct {
	cfg RenewerConfig
	ids *protocol.IDGen

	mu      sync.Mutex
	pending map[string]*renewal // by the id of the cert.renew; only the last one
}

type renewal struct {
	key  *ecdsa.PrivateKey
	done chan error
}

// ErrRenewal: the new certificate was not accepted.
var ErrRenewal = errors.New("guild: the renewed certificate was not accepted")

// NewRenewer checks cfg.
func NewRenewer(cfg RenewerConfig) (*Renewer, error) {
	if cfg.CA == nil || cfg.Source == nil || cfg.Link == nil || cfg.Names == nil {
		return nil, errors.New("guild: RenewerConfig needs CA, Source, Link and Names")
	}
	if cfg.Kind != transport.KindMachine && cfg.Kind != transport.KindGuardian {
		return nil, fmt.Errorf("guild: RenewerConfig has the kind %q", cfg.Kind)
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.Real{}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultRenewTimeout
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Renewer{cfg: cfg, ids: protocol.NewIDGen(cfg.Clock), pending: map[string]*renewal{}}, nil
}

// Due says whether the certificate is inside its renewal window or no longer
// carries the names of the machine.
func (r *Renewer) Due() bool {
	leaf := r.cfg.Source.Leaf()
	return transport.NeedsRenewal(leaf, r.cfg.Clock.Now()) || transport.NamesChanged(leaf, r.cfg.Names())
}

// Renew asks for a new certificate and installs it when it arrives. The
// connection is not touched.
func (r *Renewer) Renew(ctx context.Context) error {
	key, err := transport.GenerateKey()
	if err != nil {
		return err
	}
	der, err := transport.NewCSR(key)
	if err != nil {
		return err
	}
	id := r.ids.NewString()
	rn := &renewal{key: key, done: make(chan error, 1)}
	r.mu.Lock()
	r.pending = map[string]*renewal{id: rn}
	r.mu.Unlock()

	wctx, cancel := clock.WithTimeout(ctx, r.cfg.Clock, r.cfg.Timeout)
	defer cancel()
	// The ack comes after the handler of the Citadel finished, so a refusal
	// (an expired certificate, a removed machine) is known at once.
	res, err := r.cfg.Link.Call(wctx, protocol.Outgoing{
		Type: protocol.TypeCertRenew, ID: id,
		Payload: protocol.CertRenew{Role: roleOf(r.cfg.Kind), CSR: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: der}))},
	})
	if err != nil {
		return fmt.Errorf("guild: ask for a new certificate: %w", err)
	}
	if res.Status == protocol.AckError {
		return fmt.Errorf("%w: the Citadel refused: %v", ErrRenewal, res.Error)
	}
	select {
	case err := <-rn.done:
		return err
	case <-wctx.Done():
		return fmt.Errorf("guild: no new certificate in %s: %w", r.cfg.Timeout, wctx.Err())
	}
}

// HandleIssued is the handler of cert.issued: it checks the certificate
// against the CA, the identity it replaces and the key made for it, and puts
// it in use.
func (r *Renewer) HandleIssued(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
	p, ok := m.Payload.(*protocol.CertIssued)
	if !ok {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "cert.issued without payload")
	}
	r.mu.Lock()
	rn := r.pending[m.Env.ReplyTo]
	delete(r.pending, m.Env.ReplyTo)
	r.mu.Unlock()
	if rn == nil {
		return nil, protocol.NewError(protocol.CodeNotFound, "no renewal waits for this certificate")
	}
	cert, err := r.check(p.Cert, rn.key)
	if err == nil {
		err = r.install(ctx, cert, rn.key)
	}
	rn.done <- err
	if err != nil {
		return nil, protocol.NewError(protocol.CodeInvalidPayload, "%v", err)
	}
	return nil, nil
}

func (r *Renewer) check(certPEM string, key *ecdsa.PrivateKey) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("%w: not a PEM certificate", ErrRenewal)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRenewal, err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(r.cfg.CA)
	if _, err := cert.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: r.cfg.Clock.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrRenewal, err)
	}
	old, err := transport.IdentityOf(r.cfg.Source.Leaf())
	if err != nil {
		return nil, err
	}
	got, err := transport.IdentityOf(cert)
	if err != nil || got != old {
		return nil, fmt.Errorf("%w: the identity changed from %s to %v", ErrRenewal, old, got)
	}
	if pub, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok || !pub.Equal(&key.PublicKey) {
		return nil, fmt.Errorf("%w: it is not for the key that was made", ErrRenewal)
	}
	return cert, nil
}

func (r *Renewer) install(ctx context.Context, cert *x509.Certificate, key *ecdsa.PrivateKey) error {
	if r.cfg.Save != nil {
		if err := r.cfg.Save(ctx, cert, key); err != nil {
			return fmt.Errorf("guild: keep the renewed certificate: %w", err)
		}
	}
	r.cfg.Source.Set(cert, key)
	r.cfg.Logger.Info("guild: certificate renewed", "kind", r.cfg.Kind, "serial", transport.Serial(cert), "not_after", cert.NotAfter)
	return nil
}

// Run checks every interval (an hour by default) whether the certificate is
// due, and renews it, until ctx ends. A failure is logged and tried again at
// the next check.
func (r *Renewer) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = time.Hour
	}
	t := r.cfg.Clock.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C():
			if !r.Due() {
				continue
			}
			if err := r.Renew(ctx); err != nil && ctx.Err() == nil {
				r.cfg.Logger.Warn("guild: renewing the certificate", "err", err)
			}
		}
	}
}
