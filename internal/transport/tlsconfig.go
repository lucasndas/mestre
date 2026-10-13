package transport

import (
	"crypto"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// CertSource holds the certificate a machine presents, as server and as
// client. Set swaps it after a renewal: new handshakes use the new one and
// open connections stay up.
type CertSource struct {
	v atomic.Pointer[tls.Certificate]
}

// NewCertSource returns a source holding leaf and its private key.
func NewCertSource(leaf *x509.Certificate, key crypto.Signer) *CertSource {
	s := &CertSource{}
	s.Set(leaf, key)
	return s
}

// Set replaces the certificate.
func (s *CertSource) Set(leaf *x509.Certificate, key crypto.Signer) {
	s.v.Store(&tls.Certificate{Certificate: [][]byte{leaf.Raw}, PrivateKey: key, Leaf: leaf})
}

// Leaf is the current certificate.
func (s *CertSource) Leaf() *x509.Certificate { return s.v.Load().Leaf }

// Key is the private key of the current certificate: the engine of the
// Citadel issues its certificate of mestre.local for the same key.
func (s *CertSource) Key() crypto.Signer {
	k, _ := s.v.Load().PrivateKey.(crypto.Signer)
	return k
}

func (s *CertSource) get() (*tls.Certificate, error) {
	if c := s.v.Load(); c != nil {
		return c, nil
	}
	return nil, errors.New("transport: no certificate")
}

// Peer is the authenticated other side of a connection.
type Peer struct {
	Identity
	// Serial identifies the certificate (machine_credentials).
	Serial   string
	NotAfter time.Time
	// RemoteAddr is informative: identity never depends on the address.
	RemoteAddr string
}

func peerOf(leaf *x509.Certificate, remote string) (Peer, error) {
	id, err := IdentityOf(leaf)
	if err != nil {
		return Peer{}, err
	}
	return Peer{Identity: id, Serial: Serial(leaf), NotAfter: leaf.NotAfter, RemoteAddr: remote}, nil
}

// checkPeer applies the identity rules shared by both directions: a mestre://
// identity of an allowed kind and a credential that is not revoked.
func checkPeer(leaf *x509.Certificate, kinds []Kind, creds CredentialChecker) (Peer, error) {
	p, err := peerOf(leaf, "")
	if err != nil {
		return Peer{}, err
	}
	if len(kinds) > 0 && !slices.Contains(kinds, p.Kind) {
		return Peer{}, fmt.Errorf("transport: %s is not a %v certificate", p.Identity, kinds)
	}
	if creds != nil {
		if err := creds.CheckCredential(p.Identity, p.Serial); err != nil {
			return Peer{}, err
		}
	}
	return p, nil
}

// serverTLS is the configuration of a Guild TLS server: TLS 1.3 only, the
// certificate from src, client certificates verified against the Guild CA
// alone (the system store is ignored) with the injected clock. A client
// certificate is optional at this level, because /ca.crt and /healthz need
// none; the mTLS routes require one. A presented certificate without a
// valid identity, or revoked, fails the handshake.
func serverTLS(ca *x509.Certificate, src *CertSource, creds CredentialChecker, clk clock.Clock) *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS13,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) { return src.get() },
		ClientAuth:     tls.VerifyClientCertIfGiven,
		ClientCAs:      poolOf(ca),
		Time:           clk.Now,
		NextProtos:     []string{"http/1.1"},
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return nil
			}
			_, err := checkPeer(cs.PeerCertificates[0], nil, creds)
			return err
		},
	}
}

// clientTLS is the configuration of a Guild TLS client. The server is
// verified by chain to the Guild CA (with the injected clock), by its
// identity SAN of an allowed kind and by the revocation hook; host names and
// IPs never enter the check (docs/conectividade/descoberta.md#checagem-de-identidade).
// The verified peer is stored in *out.
func clientTLS(ca *x509.Certificate, src *CertSource, kinds []Kind, creds CredentialChecker, clk clock.Clock, out *Peer) *tls.Config {
	roots := poolOf(ca)
	return &tls.Config{
		MinVersion:           tls.VersionTLS13,
		GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return src.get() },
		// The standard check compares the host name; ours replaces it.
		InsecureSkipVerify: true,
		NextProtos:         []string{"http/1.1"},
		Time:               clk.Now,
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("transport: the server sent no certificate")
			}
			leaf := cs.PeerCertificates[0]
			inter := x509.NewCertPool()
			for _, c := range cs.PeerCertificates[1:] {
				inter.AddCert(c)
			}
			if _, err := leaf.Verify(x509.VerifyOptions{
				Roots: roots, Intermediates: inter, CurrentTime: clk.Now(),
				KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			}); err != nil {
				return fmt.Errorf("transport: server certificate: %w", err)
			}
			p, err := checkPeer(leaf, kinds, creds)
			if err != nil {
				return err
			}
			*out = p
			return nil
		},
	}
}
