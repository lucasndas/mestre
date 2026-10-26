package guild_test

import (
	"errors"
	"net"
	"slices"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// mestre.local goes only in the certificate of the acting Citadel, valid for
// 7 days and issued by the Citadel itself; the extra names are the Owner's,
// for the Guild (docs/arquitetura/seguranca.md#nomes-dos-certificados).
func TestOnlyTheActingCitadelGetsTheNameOfThePanelForSevenDays(t *testing.T) {
	t.Parallel()
	clk := clock.NewFake(day0)
	r := newRig(t, clk)
	ctx := t.Context()
	j := r.enroll(r.newNode("tower1"))
	self := r.founded.MachineID

	cert, err := r.citadel.CitadelCertificate(ctx, self, r.founded.MotorKey)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"mestre.local", "servidor.local"}; !slices.Equal(cert.DNSNames, want) {
		t.Fatalf("names = %v, want %v", cert.DNSNames, want)
	}
	if id, _ := transport.IdentityOf(cert); id.MachineID != self || id.Kind != transport.KindMachine {
		t.Fatalf("identity = %v", id)
	}
	if !cert.NotAfter.Equal(day0.Add(7 * 24 * time.Hour)) {
		t.Fatalf("NotAfter = %v, want 7 days", cert.NotAfter)
	}
	if n := r.countWhere("machine_credentials", "machine_id = ? AND role = 'cidadela' AND serial = ?", self, transport.Serial(cert)); n != 1 {
		t.Fatalf("the certificate of the Citadel is not in machine_credentials (%d)", n)
	}
	if due, err := r.citadel.CitadelCertificateDue(ctx, cert); err != nil || due {
		t.Fatalf("due right after issuing = %v, %v", due, err)
	}
	if due, _ := r.citadel.CitadelCertificateDue(ctx, r.founded.Motor); !due {
		t.Fatal("a machine certificate in use on the Citadel must be replaced")
	}

	// A Tower never gets it, not by its own call nor by a renewal.
	if _, err := r.citadel.CitadelCertificate(ctx, j.MachineID, r.founded.MotorKey); !errors.Is(err, guild.ErrNotCitadel) {
		t.Fatalf("certificate of the panel for a Tower = %v, want ErrNotCitadel", err)
	}
	if hasPanelName(renewMotor(t, r, j.MachineID)) {
		t.Fatal("a renewal gave mestre.local to a Tower")
	}

	// The extra names of the Owner: only under the private domains.
	for _, bad := range [][]string{{"*.home.arpa"}, {"mestre.local"}, {"torre.local"}, {"accounts.google.com"}, make([]string, transport.MaxExtraNames+1)} {
		if err := r.citadel.SetExtraNames(ctx, bad); !errors.Is(err, transport.ErrBadName) {
			t.Errorf("SetExtraNames(%v) = %v, want ErrBadName", bad, err)
		}
	}
	if err := r.citadel.SetExtraNames(ctx, []string{"Painel.Casa.Home.Arpa"}); err != nil {
		t.Fatal(err)
	}
	if got, err := r.citadel.ExtraNames(ctx); err != nil || !slices.Equal(got, []string{"painel.casa.home.arpa"}) {
		t.Fatalf("ExtraNames = %v, %v", got, err)
	}
	if due, _ := r.citadel.CitadelCertificateDue(ctx, cert); !due {
		t.Fatal("new extra names must make the Citadel issue its certificate again")
	}
	cert, err = r.citadel.CitadelCertificate(ctx, self, r.founded.MotorKey)
	if err != nil || !slices.Contains(cert.DNSNames, "painel.casa.home.arpa") {
		t.Fatalf("after the extra names: %v, %v", cert.DNSNames, err)
	}
	if hasPanelName(renewMotor(t, r, j.MachineID)) {
		t.Fatal("the extra names reached a Tower")
	}

	// Renewed by the Citadel 2 days before it ends.
	clk.Advance(5*24*time.Hour - time.Minute)
	if due, _ := r.citadel.CitadelCertificateDue(ctx, cert); due {
		t.Fatal("due before the last 2 days")
	}
	clk.Advance(time.Minute)
	if due, _ := r.citadel.CitadelCertificateDue(ctx, cert); !due {
		t.Fatal("not due 2 days before the end")
	}
}

// B1 of the audit: the names a machine reports are checked before anything
// is recorded or issued.
func TestTheNamesAMachineReportsAreCheckedBeforeTheRegistryKeepsThem(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	ctx := t.Context()
	j := r.enroll(r.newNode("tower1"))
	before, err := r.citadel.Machine(ctx, j.MachineID)
	if err != nil {
		t.Fatal(err)
	}
	ok := []net.IP{net.ParseIP("192.168.1.30")}
	for name, c := range map[string]struct {
		host string
		ips  []net.IP
	}{
		"the name of the panel":   {"mestre", ok},
		"a host name with dots":   {"accounts.google.com", ok},
		"a wildcard":              {"*", ok},
		"a public address":        {"tower1", []net.IP{net.ParseIP("203.0.113.5")}},
		"more addresses than 16":  {"tower1", slices.Repeat(ok, transport.MaxIPs+1)},
		"the unspecified address": {"tower1", []net.IP{net.IPv6unspecified}},
	} {
		if err := r.citadel.UpdateNames(ctx, j.MachineID, c.host, c.ips); !errors.Is(err, transport.ErrBadName) {
			t.Errorf("%s: UpdateNames = %v, want ErrBadName", name, err)
		}
	}
	after, _ := r.citadel.Machine(ctx, j.MachineID)
	if after.Hostname != before.Hostname || !slices.EqualFunc(after.IPs, before.IPs, net.IP.Equal) {
		t.Fatalf("a refused update changed the registry: %+v", after)
	}
	if err := r.citadel.UpdateNames(ctx, j.MachineID, "Tower1", ok); err != nil {
		t.Fatal(err)
	}
	cert := renewMotor(t, r, j.MachineID)
	if !slices.Equal(cert.DNSNames, []string{"tower1.local"}) || !cert.IPAddresses[0].Equal(ok[0]) {
		t.Fatalf("renewed names = %v %v", cert.DNSNames, cert.IPAddresses)
	}
	// A renewal must come with a new key (M5 of the audit).
	key, _ := transport.GenerateKey()
	csr, _ := transport.NewCSR(key)
	if _, err := r.citadel.Renew(ctx, j.MachineID, transport.KindMachine, csr); err != nil {
		t.Fatal(err)
	}
	if _, err := r.citadel.Renew(ctx, j.MachineID, transport.KindMachine, csr); !errors.Is(err, guild.ErrRenewRefused) {
		t.Fatalf("renewal with the key already in use = %v, want ErrRenewRefused", err)
	}
}
