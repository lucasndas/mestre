package guild_test

import (
	"crypto/x509"
	"errors"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// renewMotor asks the Citadel for the next engine certificate of a machine.
func renewMotor(t *testing.T, r *rig, id string) *x509.Certificate {
	t.Helper()
	key, err := transport.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	csr, err := transport.NewCSR(key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.citadel.Renew(t.Context(), id, transport.KindMachine, csr)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func hasPanelName(c *x509.Certificate) bool { return slices.Contains(c.DNSNames, transport.PanelName) }

// CA-GUILDA-12, on the Guild's side: the erasure of the vault copy on the
// excluded machine (vault.drop) and sending the vault again are the
// replication's, and the Owner's PIN is the bot's; what the Guild does is the
// flag, the hook those two plug into, the certificates and the promotion rule.
func TestExcludingAMachineFiresTheHookDropsThePanelNameAndBarsThePromotionUntilItIsUndone(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	ctx := t.Context()
	n := r.newNode("tower1")
	j := r.enroll(n)
	if hasPanelName(j.Motor) {
		t.Fatal("the certificate of a Tower carries mestre.local, which only the acting Citadel's has")
	}
	if err := r.citadel.Promotable(ctx, j.MachineID); err != nil {
		t.Fatalf("a Tower is not promotable: %v", err)
	}
	events := func() []bool {
		r.hooks.mu.Lock()
		defer r.hooks.mu.Unlock()
		var out []bool
		for _, m := range r.hooks.exclusion {
			out = append(out, m.Excluded)
		}
		return out
	}

	if err := r.citadel.SetExcluded(ctx, j.MachineID, true); err != nil {
		t.Fatal(err)
	}
	if got := events(); !slices.Equal(got, []bool{true}) {
		t.Fatalf("exclusion events = %v", got)
	}
	if err := r.citadel.Promotable(ctx, j.MachineID); !errors.Is(err, guild.ErrExcludedMachine) {
		t.Errorf("Promotable of an excluded machine = %v", err)
	}
	if hasPanelName(renewMotor(t, r, j.MachineID)) {
		t.Error("the certificate of an excluded machine carries mestre.local")
	}
	cfg, err := r.citadel.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var seen bool
	for _, m := range cfg.Machines {
		if m.MachineID == j.MachineID {
			seen = m.Excluded
		}
	}
	if !seen {
		t.Error("guild.config does not say the machine is excluded")
	}
	if err := r.citadel.SetExcluded(ctx, j.MachineID, true); err != nil || len(events()) != 1 {
		t.Errorf("excluding twice: %v, events %v", err, events())
	}

	if err := r.citadel.SetExcluded(ctx, j.MachineID, false); err != nil {
		t.Fatal(err)
	}
	if got := events(); !slices.Equal(got, []bool{true, false}) {
		t.Fatalf("exclusion events = %v, want the exclusion and its undoing", got)
	}
	if err := r.citadel.Promotable(ctx, j.MachineID); err != nil {
		t.Errorf("Promotable after undoing = %v", err)
	}
	if hasPanelName(renewMotor(t, r, j.MachineID)) {
		t.Error("after undoing the exclusion the certificate of a Tower carries mestre.local")
	}

	// the Citadel cannot be excluded, and a machine that is not in the Guild
	// cannot either
	if err := r.citadel.SetExcluded(ctx, r.founded.MachineID, true); !errors.Is(err, guild.ErrCitadelProtected) {
		t.Errorf("excluding the Citadel = %v", err)
	}
	if err := r.citadel.SetExcluded(ctx, "nobody", true); err == nil {
		t.Error("excluded a machine that does not exist")
	}
}

func TestTheRevokedListSurvivesARestartAndAMachineThatIsNotTheCitadelReceivesIt(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	ctx := t.Context()
	j := r.enroll(r.newNode("tower1"))
	if _, err := r.citadel.Remove(ctx, j.MachineID, "dono"); err != nil {
		t.Fatal(err)
	}
	serials := r.citadel.Credentials().Revoked()
	if len(serials) != 2 {
		t.Fatalf("revoked = %v", serials)
	}

	// a Citadel that starts later reads the same list from the database
	again, err := guild.LoadCredentials(ctx, r.gdb)
	if err != nil || !slices.Equal(again.Revoked(), serials) {
		t.Fatalf("after a restart: %v, %v", again.Revoked(), err)
	}
	// and a Tower holds the list that guild.config carries
	tower := guild.NewCredentials()
	id, _ := transport.IdentityOf(j.Motor)
	if err := tower.CheckCredential(id, transport.Serial(j.Motor)); err != nil {
		t.Fatalf("an empty list refused: %v", err)
	}
	tower.Replace(serials)
	if err := tower.CheckCredential(id, transport.Serial(j.Motor)); !errors.Is(err, transport.ErrRevoked) {
		t.Errorf("a revoked certificate = %v, want ErrRevoked", err)
	}
	tower.Replace(nil)
	if err := tower.CheckCredential(id, transport.Serial(j.Motor)); err != nil {
		t.Errorf("after the list was replaced by an empty one: %v", err)
	}
	// a machine that was removed does not get a new certificate either
	key, _ := transport.GenerateKey()
	csr, _ := transport.NewCSR(key)
	if _, err := r.citadel.Renew(ctx, j.MachineID, transport.KindMachine, csr); !errors.Is(err, guild.ErrNotActive) {
		t.Errorf("renewing a removed machine = %v", err)
	}
}
