package transport

import (
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Kind is what a Guild certificate identifies: the engine of a machine or
// its Guardian (docs/arquitetura/seguranca.md#tls-e-ca-interna).
type Kind string

// Certificate kinds, the host part of the URI SAN.
const (
	KindMachine  Kind = "machine"
	KindGuardian Kind = "guardian"
)

// uriScheme is the scheme of the identity SAN: mestre://<kind>/<machine_id>.
const uriScheme = "mestre"

// ErrNoIdentity is wrapped when a certificate carries no valid Mestre
// identity SAN.
var ErrNoIdentity = errors.New("certificate has no mestre:// identity")

// Identity is who a certificate speaks for.
type Identity struct {
	Kind      Kind
	MachineID string
}

// URI is the identity SAN, mestre://<kind>/<machine_id>.
func (id Identity) URI() *url.URL {
	return &url.URL{Scheme: uriScheme, Host: string(id.Kind), Path: "/" + id.MachineID}
}

func (id Identity) String() string { return id.URI().String() }

func (id Identity) validate() error {
	if id.Kind != KindMachine && id.Kind != KindGuardian {
		return fmt.Errorf("transport: unknown certificate kind %q", id.Kind)
	}
	if !validMachineID(id.MachineID) {
		return fmt.Errorf("transport: invalid machine id %q", id.MachineID)
	}
	return nil
}

// validMachineID accepts the ULIDs the Guild hands out and, for tests and
// names chosen by hand, letters, digits, '-' and '_' (1 to 64 bytes).
func validMachineID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		ok := r == '-' || r == '_' || ('0' <= r && r <= '9') || ('a' <= r && r <= 'z') || ('A' <= r && r <= 'Z')
		if !ok {
			return false
		}
	}
	return true
}

// IdentityOf reads the identity SAN of a Guild certificate. Exactly one
// mestre:// URI is accepted: two would let one certificate speak for two
// machines.
func IdentityOf(cert *x509.Certificate) (Identity, error) {
	var found []Identity
	for _, u := range cert.URIs {
		if u.Scheme != uriScheme {
			continue
		}
		id := Identity{Kind: Kind(u.Host), MachineID: strings.TrimPrefix(u.Path, "/")}
		if u.User != nil || u.RawQuery != "" || u.Fragment != "" || id.validate() != nil {
			return Identity{}, fmt.Errorf("%w: malformed %q", ErrNoIdentity, u.String())
		}
		found = append(found, id)
	}
	switch len(found) {
	case 0:
		return Identity{}, ErrNoIdentity
	case 1:
		return found[0], nil
	default:
		return Identity{}, fmt.Errorf("%w: %d identity SANs", ErrNoIdentity, len(found))
	}
}

// Serial is the hexadecimal serial number of a certificate, the credential
// identifier kept in machine_credentials (docs/arquitetura/persistencia.md).
func Serial(cert *x509.Certificate) string { return fmt.Sprintf("%x", cert.SerialNumber) }
