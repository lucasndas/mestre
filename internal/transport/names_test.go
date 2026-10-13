package transport_test

import (
	"crypto/ecdsa"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

func mustKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := transport.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return k
}

// The CA never signs a name a machine could use to pass for another site:
// host names are one label (not mestre), addresses are private, loopback or
// link-local, and extra names stay under the reserved private domains.
func TestTheCARefusesNamesOutsideTheRules(t *testing.T) {
	t.Parallel()
	ca, err := transport.NewCA("guild-1", clock.NewFake(day0))
	if err != nil {
		t.Fatal(err)
	}
	csr, _ := transport.NewCSR(mustKey(t))
	many := make([]net.IP, transport.MaxIPs+1)
	for i := range many {
		many[i] = net.IPv4(10, 0, 0, byte(i+1))
	}
	bad := map[string]transport.Names{
		"a host name with dots":     {MachineID: "m1", Hostname: "accounts.google.com"},
		"a wildcard host name":      {MachineID: "m1", Hostname: "*"},
		"the name of the panel":     {MachineID: "m1", Hostname: "Mestre"},
		"a label with an underline": {MachineID: "m1", Hostname: "desk_top"},
		"a label ending in a dash":  {MachineID: "m1", Hostname: "desk-"},
		"a label of 64 characters":  {MachineID: "m1", Hostname: strings.Repeat("a", 64)},
		"a public IPv4":             {MachineID: "m1", Hostname: "a", IPs: []net.IP{net.ParseIP("8.8.8.8")}},
		"a public IPv6":             {MachineID: "m1", Hostname: "a", IPs: []net.IP{net.ParseIP("2001:db8::1")}},
		"the unspecified address":   {MachineID: "m1", Hostname: "a", IPs: []net.IP{net.IPv4zero}},
		"too many addresses":        {MachineID: "m1", Hostname: "a", IPs: many},
	}
	for name, n := range bad {
		for _, issue := range []func([]byte, transport.Names) (*x509.Certificate, error){ca.IssueMachine, ca.IssueGuardian} {
			if _, err := issue(csr, n); !errors.Is(err, transport.ErrBadName) {
				t.Errorf("%s: issue = %v, want ErrBadName", name, err)
			}
		}
	}
	good := transport.Names{MachineID: "m1", Hostname: "Desk-01", IPs: []net.IP{
		net.ParseIP("10.1.2.3"), net.ParseIP("172.16.0.9"), net.ParseIP("192.168.0.1"), net.ParseIP("127.0.0.1"),
		net.ParseIP("169.254.1.1"), net.ParseIP("fd12::1"), net.ParseIP("::1"), net.ParseIP("fe80::1"),
	}}
	if _, err := ca.IssueMachine(csr, good); err != nil {
		t.Fatalf("names inside the rules: %v", err)
	}
	for _, extra := range []string{"mestre.local", "outra.local", "*.home.arpa", "accounts.google.com", "home.arpa", "a..internal", "-a.internal"} {
		if _, err := ca.IssueCitadel(csr, good, []string{extra}); !errors.Is(err, transport.ErrBadName) {
			t.Errorf("extra name %q: %v, want ErrBadName", extra, err)
		}
	}
	if _, err := ca.IssueCitadel(csr, good, []string{"painel.home.arpa", "mestre.internal"}); err != nil {
		t.Fatalf("extra names under the private domains: %v", err)
	}
	for in, want := range map[string]string{" Box.LAN ": "box", "DESKTOP-ANA": "desktop-ana", "mestre": "", "desk_top": ""} {
		if got := transport.HostLabel(in); got != want {
			t.Errorf("HostLabel(%q) = %q, want %q", in, got, want)
		}
	}
	if got := transport.CertificateIPs([]net.IP{net.ParseIP("8.8.8.8"), net.ParseIP("192.168.1.2"), net.ParseIP("2001:db8::1")}); len(got) != 1 {
		t.Errorf("CertificateIPs kept %v, want only the private address", got)
	}
}

// CA-PROTO-15 (Name Constraints)
// Even a certificate the CA key signs by hand, outside the checks, does not
// verify for a public name or address: the CA carries Name Constraints.
func TestTheNameConstraintsOfTheCAStopALeakedKey(t *testing.T) {
	t.Parallel()
	ca, err := transport.NewCA("guild-1", clock.NewFake(day0))
	if err != nil {
		t.Fatal(err)
	}
	c := ca.Certificate()
	if !c.PermittedDNSDomainsCritical || len(c.PermittedDNSDomains) != 3 || len(c.PermittedIPRanges) != len(transport.PermittedNets) {
		t.Fatalf("CA constraints: critical=%v dns=%v ips=%v", c.PermittedDNSDomainsCritical, c.PermittedDNSDomains, c.PermittedIPRanges)
	}
	forge := func(dns []string, ips []net.IP) *x509.Certificate {
		t.Helper()
		k := mustKey(t)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: "forged"},
			NotBefore: day0.Add(-time.Hour), NotAfter: day0.Add(24 * time.Hour),
			KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			URIs:     []*url.URL{{Scheme: "mestre", Host: "machine", Path: "/m1"}},
			DNSNames: dns, IPAddresses: ips,
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, c, &k.PublicKey, ca.KeyForTest())
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert
	}
	for name, cert := range map[string]*x509.Certificate{
		"a public site":    forge([]string{"accounts.google.com"}, nil),
		"a public address": forge(nil, []net.IP{net.ParseIP("8.8.8.8").To4()}),
	} {
		var invalid x509.CertificateInvalidError
		if err := verify(t, ca, cert, day0, x509.ExtKeyUsageServerAuth); !errors.As(err, &invalid) || invalid.Reason != x509.CANotAuthorizedForThisName {
			t.Errorf("%s: verify = %v, want CANotAuthorizedForThisName", name, err)
		}
	}
	ok := forge([]string{"mestre.local", "box.local", "painel.home.arpa", "x.internal"}, []net.IP{net.ParseIP("10.0.0.1").To4(), net.ParseIP("fd00::1")})
	if err := verify(t, ca, ok, day0, x509.ExtKeyUsageServerAuth); err != nil {
		t.Fatalf("names inside the constraints: %v", err)
	}
}
