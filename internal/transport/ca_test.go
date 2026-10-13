package transport_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/store"
	guilddb "github.com/lucasndas/mestre/internal/store/guild"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

var day0 = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func verify(t *testing.T, ca *transport.CA, cert *x509.Certificate, at time.Time, usage x509.ExtKeyUsage) error {
	t.Helper()
	_, err := cert.Verify(x509.VerifyOptions{Roots: ca.Pool(), CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{usage}})
	return err
}

// CA-PROTO-15
func TestGuildCAAndCertificates(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	ca, err := transport.NewCA("guild-1", clk)
	if err != nil {
		t.Fatal(err)
	}
	names := transport.Names{
		MachineID: "01JTOWER", Hostname: "Desktop-Ana",
		IPs: []net.IP{net.ParseIP("192.168.1.20"), net.ParseIP("fd00::20")},
	}

	t.Run("the CA is ECDSA P-256 for 10 years and signs only leaves", func(t *testing.T) {
		t.Parallel()
		c := ca.Certificate()
		pub, ok := c.PublicKey.(*ecdsa.PublicKey)
		if !ok || pub.Curve != elliptic.P256() {
			t.Fatalf("CA key %T, want ECDSA P-256", c.PublicKey)
		}
		if !c.IsCA || !c.MaxPathLenZero || c.KeyUsage&x509.KeyUsageCertSign == 0 {
			t.Fatalf("CA flags: IsCA=%v MaxPathLenZero=%v usage=%v", c.IsCA, c.MaxPathLenZero, c.KeyUsage)
		}
		if want := day0.AddDate(10, 0, 0); !c.NotAfter.Equal(want) {
			t.Fatalf("CA NotAfter = %v, want %v", c.NotAfter, want)
		}
	})

	t.Run("engine certificate carries the SANs of the security table", func(t *testing.T) {
		t.Parallel()
		cert, _ := issueCert(t, ca, transport.KindMachine, names)
		if got := cert.URIs; len(got) != 1 || got[0].String() != "mestre://machine/01JTOWER" {
			t.Fatalf("URIs = %v", got)
		}
		want := []string{"desktop-ana.local"} // never mestre.local: only the Citadel's has it
		if !slices.Equal(cert.DNSNames, want) {
			t.Fatalf("DNSNames = %v, want %v", cert.DNSNames, want)
		}
		if len(cert.IPAddresses) != 2 || !cert.IPAddresses[0].Equal(net.ParseIP("192.168.1.20")) {
			t.Fatalf("IPs = %v", cert.IPAddresses)
		}
		if want := day0.AddDate(1, 0, 0); !cert.NotAfter.Equal(want) {
			t.Fatalf("NotAfter = %v, want 1 year: %v", cert.NotAfter, want)
		}
		for _, u := range []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth} {
			if err := verify(t, ca, cert, day0, u); err != nil {
				t.Fatalf("verify usage %v: %v", u, err)
			}
		}
		if id, err := transport.IdentityOf(cert); err != nil || id != (transport.Identity{Kind: transport.KindMachine, MachineID: "01JTOWER"}) {
			t.Fatalf("IdentityOf = %v, %v", id, err)
		}
	})

	t.Run("only the certificate of the Citadel has mestre.local, for 7 days", func(t *testing.T) {
		t.Parallel()
		key, _ := transport.GenerateKey()
		csr, _ := transport.NewCSR(key)
		cert, err := ca.IssueCitadel(csr, names, []string{"Painel.Casa.Home.Arpa"})
		if err != nil {
			t.Fatal(err)
		}
		if want := []string{"desktop-ana.local", "mestre.local", "painel.casa.home.arpa"}; !slices.Equal(cert.DNSNames, want) {
			t.Fatalf("DNSNames = %v, want %v", cert.DNSNames, want)
		}
		if id, _ := transport.IdentityOf(cert); id != (transport.Identity{Kind: transport.KindMachine, MachineID: "01JTOWER"}) {
			t.Fatalf("identity = %v", id)
		}
		if want := day0.Add(7 * 24 * time.Hour); !cert.NotAfter.Equal(want) {
			t.Fatalf("NotAfter = %v, want 7 days: %v", cert.NotAfter, want)
		}
		if err := verify(t, ca, cert, day0, x509.ExtKeyUsageServerAuth); err != nil {
			t.Fatal(err)
		}
		if transport.NeedsRenewal(cert, cert.NotAfter.Add(-49*time.Hour)) || !transport.NeedsRenewal(cert, cert.NotAfter.Add(-48*time.Hour)) {
			t.Fatal("the certificate of the Citadel renews 2 days before its end")
		}
		if transport.CitadelNamesChanged(cert, names, []string{"painel.casa.home.arpa"}) || !transport.CitadelNamesChanged(cert, names, nil) {
			t.Fatal("CitadelNamesChanged must follow the extra names")
		}
		// A machine that stops being the Citadel renews at once into a
		// certificate without the name of the panel.
		if !transport.NamesChanged(cert, names) {
			t.Fatal("the certificate of the Citadel is not a machine certificate")
		}
		csr2, _ := transport.NewCSR(mustKey(t))
		back, err := ca.Reissue(cert, csr2, names)
		if err != nil {
			t.Fatal(err)
		}
		if slices.Contains(back.DNSNames, transport.PanelName) || !back.NotAfter.Equal(day0.AddDate(1, 0, 0)) {
			t.Fatalf("reissued as a machine: %v until %v", back.DNSNames, back.NotAfter)
		}
		if _, err := ca.IssueCitadel(csr, names, []string{"*.home.arpa"}); err == nil {
			t.Fatal("a wildcard extra name was signed")
		}
	})

	t.Run("Guardian certificate: guardian URI, host names and IPs only", func(t *testing.T) {
		t.Parallel()
		cert, _ := issueCert(t, ca, transport.KindGuardian, names)
		if cert.URIs[0].String() != "mestre://guardian/01JTOWER" {
			t.Fatalf("URI = %v", cert.URIs[0])
		}
		if want := []string{"desktop-ana.local"}; !slices.Equal(cert.DNSNames, want) {
			t.Fatalf("DNSNames = %v, want %v", cert.DNSNames, want)
		}
		if err := verify(t, ca, cert, day0, x509.ExtKeyUsageClientAuth); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("renewal 30 days before expiry, with a new key and the same identity", func(t *testing.T) {
		t.Parallel()
		cert, _ := issueCert(t, ca, transport.KindMachine, names)
		if transport.NeedsRenewal(cert, cert.NotAfter.Add(-31*24*time.Hour)) {
			t.Fatal("31 days before expiry is not yet the renewal window")
		}
		if !transport.NeedsRenewal(cert, cert.NotAfter.Add(-30*24*time.Hour)) {
			t.Fatal("30 days before expiry must renew")
		}
		key, _ := transport.GenerateKey()
		csr, _ := transport.NewCSR(key)
		n := names
		n.IPs = []net.IP{net.ParseIP("192.168.1.99")}
		if !transport.NamesChanged(cert, n) || transport.NamesChanged(cert, names) {
			t.Fatal("NamesChanged must follow the IPs")
		}
		renewed, err := ca.Reissue(cert, csr, n)
		if err != nil {
			t.Fatal(err)
		}
		if renewed.SerialNumber.Cmp(cert.SerialNumber) == 0 || !renewed.PublicKey.(*ecdsa.PublicKey).Equal(&key.PublicKey) {
			t.Fatal("renewal must have a new serial and the new key")
		}
		before, _ := transport.IdentityOf(cert)
		if after, err := transport.IdentityOf(renewed); err != nil || after != before {
			t.Fatalf("renewal changed the identity from %v to %v (%v)", before, after, err)
		}
		if _, err := ca.Reissue(renewed, csr, n); !errors.Is(err, transport.ErrSameKey) {
			t.Fatalf("renewal with the key already in use = %v, want ErrSameKey", err)
		}
		if !renewed.IPAddresses[0].Equal(net.ParseIP("192.168.1.99")) {
			t.Fatalf("renewed IPs = %v", renewed.IPAddresses)
		}
		other := n
		other.MachineID = "01JOTHER"
		if _, err := ca.Reissue(cert, csr, other); err == nil {
			t.Fatal("renewing into another machine must be refused")
		}
	})

	t.Run("an expired certificate is not renewed", func(t *testing.T) {
		t.Parallel()
		fclk := clock.NewFake(day0)
		ca2, _ := transport.NewCA("guild-2", fclk)
		cert, key := issueCert(t, ca2, transport.KindMachine, names)
		fclk.Advance(366 * 24 * time.Hour)
		csr, _ := transport.NewCSR(key)
		if _, err := ca2.Reissue(cert, csr, names); err == nil {
			t.Fatal("an expired certificate needs a new entry, not a renewal")
		}
	})

	t.Run("a CSR with another key type or a forged signature is refused", func(t *testing.T) {
		t.Parallel()
		p384, _ := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		csr, _ := transport.NewCSR(p384)
		if _, err := ca.IssueMachine(csr, names); err == nil {
			t.Fatal("P-384 CSR accepted")
		}
		key, _ := transport.GenerateKey()
		good, _ := transport.NewCSR(key)
		bad := slices.Clone(good)
		bad[len(bad)-3] ^= 0xff
		if _, err := ca.IssueMachine(bad, names); err == nil {
			t.Fatal("CSR with a broken signature accepted")
		}
		if _, err := ca.IssueMachine(good, transport.Names{MachineID: "bad/id"}); err == nil {
			t.Fatal("invalid machine id accepted")
		}
	})
}

func TestIdentityOfRefusesAmbiguousCertificates(t *testing.T) {
	t.Parallel()
	u := func(s string) *url.URL { v, _ := url.Parse(s); return v }
	for name, uris := range map[string][]*url.URL{
		"none":         nil,
		"two":          {u("mestre://machine/a"), u("mestre://guardian/b")},
		"unknown kind": {u("mestre://browser/a")},
		"empty id":     {u("mestre://machine/")},
		"nested path":  {u("mestre://machine/a/b")},
		"query":        {u("mestre://machine/a?x=1")},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if id, err := transport.IdentityOf(&x509.Certificate{URIs: uris}); !errors.Is(err, transport.ErrNoIdentity) {
				t.Fatalf("IdentityOf(%v) = %v, %v; want ErrNoIdentity", uris, id, err)
			}
		})
	}
}

// openVault creates a real vault over a fresh Guild database.
func openVault(t *testing.T) (*vault.Vault, *[]vault.UseEvent) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := guilddb.Open(ctx, filepath.Join(dir, "mestre.db"), store.Options{Synchronous: "OFF"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	rk, err := vault.NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	uses := &[]vault.UseEvent{}
	v, err := vault.Create(ctx, vault.Config{
		DB: db, Keys: &memKeys{}, EnvelopePath: filepath.Join(dir, "cofre-envelope.bin"),
		RecoveryPublicKey: rk.PublicKey(), Masker: logging.NewMasker(), Clock: clock.NewFake(day0),
		OnUse: func(u vault.UseEvent) { mu.Lock(); *uses = append(*uses, u); mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	return v, uses
}

type memKeys struct {
	mu   sync.Mutex
	data []byte
}

func (m *memKeys) Load(context.Context) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.data == nil {
		return nil, vault.ErrNoKey
	}
	return slices.Clone(m.data), nil
}

func (m *memKeys) Save(_ context.Context, d []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.data = slices.Clone(d)
	return nil
}

// CA-PROTO-16
func TestCAKeyLivesInTheVault(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	v, uses := openVault(t)
	ca, err := transport.NewCA("guild-1", clock.NewFake(day0))
	if err != nil {
		t.Fatal(err)
	}
	if err := ca.Store(ctx, v); err != nil {
		t.Fatal(err)
	}
	info, err := v.Info(ctx, transport.CAKeySecret)
	if err != nil || info.Type != vault.TypeCAKey {
		t.Fatalf("vault info = %+v, %v; want type ca_key", info, err)
	}

	loaded, err := transport.LoadCA(ctx, v, ca.DER(), "issue a machine certificate", clock.NewFake(day0))
	if err != nil {
		t.Fatal(err)
	}
	if len(*uses) != 1 || (*uses)[0].Purpose != "issue a machine certificate" {
		t.Fatalf("vault reads = %+v, want one audited read", *uses)
	}
	cert, _ := issueCert(t, loaded, transport.KindMachine, transport.Names{MachineID: "m1", Hostname: "m1"})
	if err := verify(t, ca, cert, day0, x509.ExtKeyUsageClientAuth); err != nil {
		t.Fatalf("a CA loaded from the vault must issue certificates of the same CA: %v", err)
	}
	other, _ := transport.NewCA("guild-2", clock.NewFake(day0))
	if _, err := transport.LoadCA(ctx, v, other.DER(), "issue", clock.NewFake(day0)); err == nil {
		t.Fatal("the vault key of guild-1 must not open the CA of guild-2")
	}
}

func TestTheCASignsAndOnlyItsCertificateVerifies(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	ca, err := transport.NewCA("guild-1", clk)
	if err != nil {
		t.Fatal(err)
	}
	other, err := transport.NewCA("guild-2", clk)
	if err != nil {
		t.Fatal(err)
	}
	msg := []byte("epoch 2")
	sig, err := ca.Sign(msg)
	if err != nil {
		t.Fatal(err)
	}
	if err := transport.VerifySignature(ca.Certificate(), msg, sig); err != nil {
		t.Fatalf("own signature: %v", err)
	}
	for name, c := range map[string]struct {
		cert *x509.Certificate
		msg  []byte
	}{
		"another message": {ca.Certificate(), []byte("epoch 3")},
		"another CA":      {other.Certificate(), msg},
	} {
		if err := transport.VerifySignature(c.cert, c.msg, sig); !errors.Is(err, transport.ErrBadSignature) {
			t.Errorf("%s: err = %v, want ErrBadSignature", name, err)
		}
	}
}
