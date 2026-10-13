package transport

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Default validity of the Guild certificates (docs/arquitetura/seguranca.md#tls-e-ca-interna).
const (
	CAValidityYears   = 10
	LeafValidityYears = 1
	// RenewBefore is how long before expiry a certificate is renewed.
	RenewBefore = 30 * 24 * time.Hour
	// CitadelValidity is the validity of the certificate of the acting
	// Citadel, the only one with mestre.local; CitadelRenewBefore is when the
	// Citadel issues the next one.
	CitadelValidity    = 7 * 24 * time.Hour
	CitadelRenewBefore = 2 * 24 * time.Hour
	// backdate absorbs small clock differences between machines (up to 5 s
	// is only a checklist warning, docs/conectividade/protocolo.md).
	backdate = time.Hour
)

// CAKeySecret is the name of the vault secret, of type vault.TypeCAKey, that
// holds the private key of the Guild CA (docs/plataforma/cofre.md).
const CAKeySecret = "guild_ca_key"

// CA is the Guild certificate authority. The certificate is public and goes
// to every machine; the key lives only in the vault and, while the Citadel
// issues certificates, in this value.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	clk  clock.Clock
}

// NewCA creates the Guild CA, at the creation of the Guild: ECDSA P-256,
// valid for CAValidityYears, with critical Name Constraints that allow only
// PermittedDomains and PermittedNets. The browsers that trust the CA (and
// Go) then refuse any other name it signs, so a leaked CA key cannot
// impersonate a site. Store saves its key in the vault.
func NewCA(guildID string, clk clock.Clock) (*CA, error) {
	if guildID == "" {
		return nil, errors.New("transport: the Guild CA needs the guild_id")
	}
	if clk == nil {
		clk = clock.Real{}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("transport: generate CA key: %w", err)
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	now := clk.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Mestre Guild CA " + guildID, Organization: []string{"Mestre"}},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.AddDate(CAValidityYears, 0, 0),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		// SubjectKeyId is derived from the key by x509 for a CA.
		// RFC 5280 asks for the extension to be critical: a verifier that
		// does not know it refuses the CA instead of trusting it unbounded.
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         slices.Clone(PermittedDomains),
		PermittedIPRanges:           slices.Clone(PermittedNets),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("transport: create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("transport: parse CA certificate: %w", err)
	}
	return &CA{cert: cert, key: key, clk: clk}, nil
}

// Certificate is the public CA certificate.
func (ca *CA) Certificate() *x509.Certificate { return ca.cert }

// DER is the CA certificate as served at /ca.crt.
func (ca *CA) DER() []byte { return bytes.Clone(ca.cert.Raw) }

// Fingerprint is the SHA-256 of DER, the fingerprint the installer shows.
func Fingerprint(der []byte) [32]byte { return sha256.Sum256(der) }

// Pool trusts only this CA, ignoring the system store.
func (ca *CA) Pool() *x509.CertPool { return poolOf(ca.cert) }

func poolOf(cert *x509.Certificate) *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(cert)
	return p
}

// SecretStore is the part of the vault the CA uses (*vault.Vault).
type SecretStore interface {
	Put(ctx context.Context, name string, typ vault.Type, value []byte, site *vault.SiteMeta) error
	Use(ctx context.Context, name, purpose string, fn func(value []byte) error) error
}

// Store saves the CA private key in the vault as CAKeySecret, its only
// place at rest.
func (ca *CA) Store(ctx context.Context, v SecretStore) error {
	der, err := x509.MarshalECPrivateKey(ca.key)
	if err != nil {
		return fmt.Errorf("transport: marshal CA key: %w", err)
	}
	block := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
	clear(der)
	defer clear(block)
	if err := v.Put(ctx, CAKeySecret, vault.TypeCAKey, block, nil); err != nil {
		return fmt.Errorf("transport: store CA key: %w", err)
	}
	return nil
}

// LoadCA opens the Guild CA on the Citadel: the public certificate (from the
// Guild database) and the key, read from the vault for purpose (audited).
// The key must match the certificate.
func LoadCA(ctx context.Context, v SecretStore, certDER []byte, purpose string, clk clock.Clock) (*CA, error) {
	if clk == nil {
		clk = clock.Real{}
	}
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("transport: parse CA certificate: %w", err)
	}
	if !cert.IsCA {
		return nil, errors.New("transport: the Guild CA certificate is not a CA")
	}
	var key *ecdsa.PrivateKey
	err = v.Use(ctx, CAKeySecret, purpose, func(value []byte) error {
		block, _ := pem.Decode(value)
		if block == nil || block.Type != "EC PRIVATE KEY" {
			return errors.New("the CA key is not an EC PRIVATE KEY block")
		}
		k, err := x509.ParseECPrivateKey(block.Bytes)
		if err != nil {
			return err
		}
		key = k
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("transport: load CA key: %w", err)
	}
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || !pub.Equal(&key.PublicKey) {
		return nil, errors.New("transport: the CA key in the vault does not match the CA certificate")
	}
	return &CA{cert: cert, key: key, clk: clk}, nil
}

func newSerial() (*big.Int, error) {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return nil, fmt.Errorf("transport: serial number: %w", err)
	}
	return n.Add(n, big.NewInt(1)), nil
}
