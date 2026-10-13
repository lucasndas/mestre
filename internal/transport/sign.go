package transport

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"errors"
	"fmt"
)

// ErrBadSignature is wrapped when a signature does not verify.
var ErrBadSignature = errors.New("signature does not verify")

// Sign signs msg with the key of the CA: ECDSA P-256 over the SHA-256 of
// msg, ASN.1 encoded. The Guild signs the certificate of epoch with it
// (docs/guilda/replicacao.md#certidão-de-época); the caller owns the
// domain separation of msg.
func (ca *CA) Sign(msg []byte) ([]byte, error) {
	h := sha256.Sum256(msg)
	sig, err := ecdsa.SignASN1(rand.Reader, ca.key, h[:])
	if err != nil {
		return nil, fmt.Errorf("transport: sign: %w", err)
	}
	return sig, nil
}

// VerifySignature checks a signature made by Sign against the public key of
// the CA certificate. It wraps ErrBadSignature when it does not verify.
func VerifySignature(ca *x509.Certificate, msg, sig []byte) error {
	pub, ok := ca.PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return errors.New("transport: the CA key is not ECDSA")
	}
	h := sha256.Sum256(msg)
	if !ecdsa.VerifyASN1(pub, h[:], sig) {
		return ErrBadSignature
	}
	return nil
}
