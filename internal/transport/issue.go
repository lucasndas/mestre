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

// PanelName is the name of the panel. Only the certificate of the acting
// Citadel carries it (IssueCitadel), with a short validity: the browsers do
// not see the revocation list of the Guild, so a machine that is excluded,
// removed or no longer the Citadel must not keep a name they trust for the
// panel (docs/arquitetura/seguranca.md#tls-e-ca-interna).
const PanelName = "mestre.local"

// Names are the SANs of a machine besides its identity URI
// (docs/arquitetura/seguranca.md#nomes-dos-certificados). Check validates them;
// the CA refuses to sign names that do not pass.
type Names struct {
	MachineID string
	// Hostname is the computer name, one label; the certificate carries
	// <hostname>.local.
	Hostname string
	// IPs are the machine's addresses on the local network: private,
	// loopback or link-local, at most MaxIPs.
	IPs []net.IP
}

// dnsNames lists the DNS SANs, sorted: <host>.local and, on the certificate
// of the Citadel (panel not nil), mestre.local and the extra names.
func (n Names) dnsNames(panel []string) []string {
	var out []string
	if h := strings.ToLower(n.Hostname); h != "" {
		out = append(out, h+".local")
	}
	if panel != nil {
		out = append(out, PanelName)
		out = append(out, panel...)
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
// mTLS routes and server of port 7443) for the key in csrDER. It never
// carries mestre.local: that is IssueCitadel.
func (ca *CA) IssueMachine(csrDER []byte, n Names) (*x509.Certificate, error) {
	return ca.issue(KindMachine, csrDER, n, nil, ca.leafNotAfter())
}

// IssueGuardian issues the Guardian certificate of a machine (client of the
// Guardian route and server of ports 7444 and 7446).
func (ca *CA) IssueGuardian(csrDER []byte, n Names) (*x509.Certificate, error) {
	return ca.issue(KindGuardian, csrDER, n, nil, ca.leafNotAfter())
}

// IssueCitadel issues the engine certificate of the acting Citadel: the
// machine's names plus mestre.local and the extra names of the Guild (checked
// by CheckExtraNames), valid for CitadelValidity. The Citadel, which holds the
// CA, issues it to itself when it takes the command and again before it
// expires (CitadelRenewBefore); a machine that stops being the Citadel loses
// the name in at most CitadelValidity.
func (ca *CA) IssueCitadel(csrDER []byte, n Names, extra []string) (*x509.Certificate, error) {
	panel, err := CheckExtraNames(extra)
	if err != nil {
		return nil, err
	}
	return ca.issue(KindMachine, csrDER, n, panel, ca.clk.Now().Add(CitadelValidity))
}

// ErrSameKey: a renewal must come with a new key.
var ErrSameKey = errors.New("transport: the renewal needs a new key")

// Reissue renews old for the new key in csrDER and the current names: the
// identity must stay the same, so a machine never renews into another one,
// and the key must change. The result never carries mestre.local. Used 30
// days before expiry and when the machine's names change; an expired
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
	pub, err := csrKey(csrDER)
	if err != nil {
		return nil, err
	}
	if oldPub, ok := old.PublicKey.(*ecdsa.PublicKey); ok && oldPub.Equal(pub) {
		return nil, fmt.Errorf("%w: %s", ErrSameKey, id)
	}
	return ca.issue(id.Kind, csrDER, n, nil, ca.leafNotAfter())
}

func (ca *CA) leafNotAfter() time.Time { return ca.clk.Now().AddDate(LeafValidityYears, 0, 0) }

func (ca *CA) issue(k Kind, csrDER []byte, n Names, panel []string, notAfter time.Time) (*x509.Certificate, error) {
	id := Identity{Kind: k, MachineID: n.MachineID}
	if err := id.validate(); err != nil {
		return nil, err
	}
	if err := n.Check(); err != nil {
		return nil, fmt.Errorf("transport: issue %s: %w", id, err)
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
		DNSNames:     n.dnsNames(panel),
		IPAddresses:  n.ips(),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, pub, ca.key)
	if err != nil {
		return nil, fmt.Errorf("transport: issue %s: %w", id, err)
	}
	return x509.ParseCertificate(der)
}

// IsCitadelCertificate says whether leaf is a certificate of the acting
// Citadel, the one with mestre.local.
func IsCitadelCertificate(leaf *x509.Certificate) bool {
	return slices.Contains(leaf.DNSNames, PanelName)
}

// NeedsRenewal reports whether leaf is inside the renewal window at now: 30
// days before the end for a machine certificate, CitadelRenewBefore for the
// short one of the Citadel.
func NeedsRenewal(leaf *x509.Certificate, now time.Time) bool {
	before := RenewBefore
	if IsCitadelCertificate(leaf) {
		before = CitadelRenewBefore
	}
	return !now.Before(leaf.NotAfter.Add(-before))
}

// NamesChanged reports whether the machine certificate leaf no longer
// carries exactly the names n gives (an IP or the host name changed, or it is
// a certificate of the Citadel the machine no longer is): the certificate is
// then reissued at once.
func NamesChanged(leaf *x509.Certificate, n Names) bool {
	return namesChanged(leaf, n, nil)
}

// CitadelNamesChanged is NamesChanged for the certificate of the acting
// Citadel, which also carries mestre.local and the extra names.
func CitadelNamesChanged(leaf *x509.Certificate, n Names, extra []string) bool {
	panel, err := CheckExtraNames(extra)
	if err != nil {
		return true
	}
	return namesChanged(leaf, n, panel)
}

func namesChanged(leaf *x509.Certificate, n Names, panel []string) bool {
	id, err := IdentityOf(leaf)
	if err != nil || id.MachineID != n.MachineID {
		return true
	}
	have := slices.Clone(leaf.DNSNames)
	slices.Sort(have)
	if !slices.Equal(have, n.dnsNames(panel)) {
		return true
	}
	return !slices.EqualFunc(Names{IPs: leaf.IPAddresses}.ips(), n.ips(), net.IP.Equal)
}
