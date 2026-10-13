package transport

import (
	"crypto/ecdsa"
	"crypto/tls"
)

// KeyForTest exposes the key of the CA, so a test can sign what the CA itself
// would refuse and see the Name Constraints stop it.
func (ca *CA) KeyForTest() *ecdsa.PrivateKey { return ca.key }

// MaxDiscard is the size over which a message closes the connection (1009).
const MaxDiscard = maxDiscard

// TLSForTest is the certificate and key the source presents.
func (s *CertSource) TLSForTest() *tls.Certificate { c, _ := s.get(); return c }
