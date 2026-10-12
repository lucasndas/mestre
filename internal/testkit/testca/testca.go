// Package testca is the test certificate authority used to run real TLS and
// mTLS over the fake network (docs/qualidade/dubles.md#rede-falsa).
// Certificates are valid from 1999 to 2100 so they work inside the
// testing/synctest bubble, whose clock starts in 2000.
package testca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"sync/atomic"
	"time"
)

var (
	notBefore = time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)
	notAfter  = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)
)

// CA is an in-memory ECDSA P-256 authority.
type CA struct {
	cert   *x509.Certificate
	key    *ecdsa.PrivateKey
	serial atomic.Int64
	// Pool trusts only this CA.
	Pool *x509.CertPool
}

// New creates a CA.
func New() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "mestre test CA"},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse CA certificate: %w", err)
	}
	ca := &CA{cert: cert, key: key, Pool: x509.NewCertPool()}
	ca.serial.Store(1)
	ca.Pool.AddCert(cert)
	return ca, nil
}

// Issue returns a leaf certificate for the given DNS names, usable as server
// and client certificate.
func (ca *CA) Issue(names ...string) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("generate key: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(ca.serial.Add(1)),
		Subject:      pkix.Name{CommonName: names[0]},
		DNSNames:     names,
		NotBefore:    notBefore,
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return tls.Certificate{}, fmt.Errorf("issue %v: %w", names, err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, nil
}
