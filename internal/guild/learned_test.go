package guild_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// guild.config carries the machines that were removed, and a Tower keeps what
// it said across a reload from a replica that is behind (the union of the
// replica and the last guild.config), so the certificate of a removed machine
// that the registry never knew is refused until the replica catches up.
func TestAGuildConfigCarriesTheRemovedMachinesAndATowerKeepsThemAcrossAReload(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	ctx := t.Context()
	j := r.enroll(r.newNode("tower1"))
	key, _ := transport.GenerateKey()
	csr, _ := transport.NewCSR(key)
	unknown, err := r.founded.CA.IssueMachine(csr, transport.Names{MachineID: j.MachineID, Hostname: "tower1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.citadel.Remove(ctx, j.MachineID, "dono"); err != nil {
		t.Fatal(err)
	}
	cfg, err := r.citadel.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(cfg.Retired, []string{j.MachineID}) {
		t.Fatalf("guild.config retires %v, want [%s]", cfg.Retired, j.MachineID)
	}

	// a Tower whose replica does not show the removal yet (nothing to reload)
	tower := guild.NewCredentials()
	id, _ := transport.IdentityOf(unknown)
	tower.Learn(cfg.Revoked, cfg.Retired)
	if err := tower.CheckCredential(id, transport.Serial(unknown)); !errors.Is(err, transport.ErrRevoked) {
		t.Fatalf("after guild.config, an unrecorded certificate of the removed machine = %v, want ErrRevoked", err)
	}
	// a reload from a database that is behind keeps what guild.config said
	behind, err := guild.LoadCredentials(ctx, r.gdb)
	if err != nil {
		t.Fatal(err)
	}
	behind.Learn(nil, []string{"another"})
	if err := behind.Reload(ctx, r.gdb); err != nil {
		t.Fatal(err)
	}
	other, _ := transport.IdentityOf(r.founded.Motor)
	other.MachineID = "another"
	if err := behind.CheckCredential(other, "serial"); !errors.Is(err, transport.ErrRevoked) {
		t.Errorf("a machine named by guild.config, after a reload = %v, want ErrRevoked", err)
	}
	if err := behind.CheckCredential(id, transport.Serial(unknown)); !errors.Is(err, transport.ErrRevoked) {
		t.Errorf("the machine the database retired, after a reload = %v, want ErrRevoked", err)
	}
}
