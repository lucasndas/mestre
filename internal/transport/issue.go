package transport

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
)

// PanelName is the name every non-excluded engine certificate carries, so
// any machine can become the Citadel and serve the panel under it.
const PanelName = "mestre.local"

// Names are the SANs of a machine besides its identity URI
// (docs/arquitetura/seguranca.md#tls-e-ca-interna).
type Names struct {
	MachineID string
	// Hostname is the computer name; it also gives <host>.local.
	Hostname string
	// IPs are the machine's addresses on the local network.
	IPs []net.IP
	// Extra are the configured extra names (engine certificate only).
	Extra []string
	// Excluded marks the excluded machine, whose engine certificate never
	// carries mestre.local because it never becomes the Citadel.
	Excluded bool
}

// dnsNames lists the DNS SANs of a certificate of kind k, sorted.
func (n Names) dnsNames(k Kind) []string {
	var out []string
	if h := strings.ToLower(strings.TrimSuffix(n.Hostname, ".")); h != "" {
		out = append(out, h)
		label, _, _ := strings.Cut(strings.TrimSuffix(h, ".local"), ".")
		out = append(out, label+".local")
	}
	if k == KindMachine {
		for _, e := range n.Extra {
			if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
				out = append(out, e)
			}
		}
		if !n.Excluded {
			out = append(out, PanelName)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (n Names) ips() []net.IP {
	var out []net.IP
	for _, ip := range n.IPs {
		if ip == nil {
			continue
		}
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		if !slices.ContainsFunc(out, ip.Equal) {
			out = append(out, ip)
		}
	}
	slices.SortFunc(out, func(a, b net.IP) int { return strings.Compare(a.String(), b.String()) })
	return out
}

// GenerateKey creates the ECDSA P-256 key of an engine or a Guardian. It is
// generated on the machine and never leaves it; the CA sees only the CSR.
func GenerateKey() (*ecdsa.PrivateKey, error) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("transport: generate key: %w", err)
	}
	return k, nil
}

// NewCSR is the certificate request that carries the public key of key to
// the Citadel. The CA decides every name: the request carries none.
func NewCSR(key crypto.Signer) ([]byte, error) {
	der, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	if err != nil {
		return nil, fmt.Errorf("transport: create CSR: %w", err)
	}
	return der, nil
}

// csrKey checks the request signature (the requester holds the key) and
// returns its ECDSA P-256 public key.
func csrKey(csrDER []byte) (*ecdsa.PublicKey, error) {
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, fmt.Errorf("transport: parse CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return nil, fmt.Errorf("transport: CSR signature: %w", err)
	}
	pub, ok := csr.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		return nil, errors.New("transport: the CSR key is not ECDSA P-256")
	}
	return pub, nil
}

// IssueMachine issues the engine certificate of a machine (client of the
// mTLS routes and server of port 7443) for the key in csrDER.
func (ca *CA) IssueMachine(csrDER []byte, n Names) (*x509.Certificate, error) {
	return ca.issue(KindMachine, csrDER, n)
}

// IssueGuardian issues the Guardian certificate of a machine (client of the
// Guardian route and server of ports 7444 and 7446).
func (ca *CA) IssueGuardian(csrDER []byte, n Names) (*x509.Certificate, error) {
	return ca.issue(KindGuardian, csrDER, n)
}

// Reissue renews old for the new key in csrDER and the current names: the
// identity must stay the same, so a machine never renews into another one.
// Used 30 days before expiry and when the machine's IPs change; an expired
// certificate is refused (the machine needs a new entry, docs/guilda/guilda.md#credenciais).
func (ca *CA) Reissue(old *x509.Certificate, csrDER []byte, n Names) (*x509.Certificate, error) {
	id, err := IdentityOf(old)
	if err != nil {
		return nil, err
	}
	if id.MachineID != n.MachineID {
		return nil, fmt.Errorf("transport: renewing %s as machine %q", id, n.MachineID)
	}
	if _, err := old.Verify(x509.VerifyOptions{
		Roots: ca.Pool(), CurrentTime: ca.clk.Now(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, fmt.Errorf("transport: renew %s: %w", id, err)
	}
	return ca.issue(id.Kind, csrDER, n)
}

func (ca *CA) issue(k Kind, csrDER []byte, n Names) (*x509.Certificate, error) {
	id := Identity{Kind: k, MachineID: n.MachineID}
	if err := id.validate(); err != nil {
		return nil, err
	}
	pub, err := csrKey(csrDER)
	if err != nil {
		return nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, err
	}
	now := ca.clk.Now()
	notAfter := now.AddDate(LeafValidityYears, 0, 0)
	if notAfter.After(ca.cert.NotAfter) {
		notAfter = ca.cert.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: id.String()},
		NotBefore:    now.Add(-backdate),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		URIs:         []*url.URL{id.URI()},
		DNSNames:     n.dnsNames(k),
		IPAddresses:  n.ips(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, fmt.Errorf("transport: issue %s: %w", id, err)
	}
	return x509.ParseCertificate(der)
}

// NeedsRenewal reports whether leaf is inside the renewal window at now.
func NeedsRenewal(leaf *x509.Certificate, now time.Time) bool {
	return !now.Before(leaf.NotAfter.Add(-RenewBefore))
}

// NamesChanged reports whether leaf no longer carries exactly the names n
// gives (an IP changed, an extra name was added, the machine was excluded):
// the certificate is then reissued at once.
func NamesChanged(leaf *x509.Certificate, n Names) bool {
	id, err := IdentityOf(leaf)
	if err != nil || id.MachineID != n.MachineID {
		return true
	}
	have := slices.Clone(leaf.DNSNames)
	slices.Sort(have)
	if !slices.Equal(have, n.dnsNames(id.Kind)) {
		return true
	}
	return !slices.EqualFunc(Names{IPs: leaf.IPAddresses}.ips(), n.ips(), net.IP.Equal)
}
