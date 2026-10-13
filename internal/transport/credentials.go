package transport

import (
	"errors"
	"fmt"
	"sync"
)

// ErrRevoked is wrapped when a credential was revoked.
var ErrRevoked = errors.New("credential revoked")

// CredentialChecker is the revocation hook, consulted on every TLS handshake
// in both directions and again before a route accepts a connection. The
// Guild implements it over machine_credentials, whose revoked list also
// goes to the Towers in the replica (docs/arquitetura/seguranca.md#tls-e-ca-interna).
type CredentialChecker interface {
	// CheckCredential returns nil when the certificate with this identity
	// and serial may connect, and an error wrapping ErrRevoked when not.
	CheckCredential(id Identity, serial string) error
}

// MemCredentials is an in-memory revocation list.
type MemCredentials struct {
	mu       sync.RWMutex
	serials  map[string]bool
	machines map[string]bool
}

var _ CredentialChecker = (*MemCredentials)(nil)

// NewMemCredentials returns an empty list: every certificate of the CA is
// accepted.
func NewMemCredentials() *MemCredentials {
	return &MemCredentials{serials: map[string]bool{}, machines: map[string]bool{}}
}

// Revoke revokes one certificate by serial (see Serial).
func (m *MemCredentials) Revoke(serial string) {
	m.mu.Lock()
	m.serials[serial] = true
	m.mu.Unlock()
}

// RevokeMachine revokes every certificate of a removed machine, engine and
// Guardian.
func (m *MemCredentials) RevokeMachine(machineID string) {
	m.mu.Lock()
	m.machines[machineID] = true
	m.mu.Unlock()
}

// CheckCredential implements CredentialChecker.
func (m *MemCredentials) CheckCredential(id Identity, serial string) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.machines[id.MachineID] || m.serials[serial] {
		return fmt.Errorf("%w: %s (serial %s)", ErrRevoked, id, serial)
	}
	return nil
}
