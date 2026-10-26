package guild_test

import (
	"errors"
	"net"
	"testing"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/pkg/clock"
)

func lan(t *testing.T) []*net.IPNet {
	t.Helper()
	_, n, err := net.ParseCIDR("192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	return []*net.IPNet{n}
}

// CA-ADM-11
// A Tower that entered by an address (outside the local network) and an excluded
// one are refused as the target of the planned switch, with the reason; a Tower
// in the local network that is not excluded is accepted.
func TestPassTargetRefusesTheExcludedAndTheOneOutsideTheLocalNetwork(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	ctx := t.Context()
	near, far, excluded := r.enroll(r.newNode("near")), r.enroll(r.newNode("far")), r.enroll(r.newNode("excluded"))
	for id, ip := range map[string]string{near.MachineID: "192.168.1.20", far.MachineID: "10.9.9.9", excluded.MachineID: "192.168.1.21"} {
		if err := r.citadel.UpdateNames(ctx, id, "tower", []net.IP{net.ParseIP(ip)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.citadel.SetExcluded(ctx, excluded.MachineID, true); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		id   string
		want error
	}{{far.MachineID, guild.ErrOutsideLocalNetwork}, {excluded.MachineID, guild.ErrExcludedMachine}, {r.founded.MachineID, guild.ErrCitadelProtected}, {"nobody", guild.ErrNoMachine}} {
		if err := r.citadel.PassTarget(ctx, c.id, lan(t)); !errors.Is(err, c.want) {
			t.Errorf("PassTarget(%s) = %v, want %v", c.id, err, c.want)
		}
	}
	if err := r.citadel.PassTarget(ctx, near.MachineID, lan(t)); err != nil {
		t.Errorf("a Tower in the local network: %v", err)
	}
}

func TestPriorityAndTimezoneAreValidatedAndReachGuildConfig(t *testing.T) {
	t.Parallel()
	r := newRig(t, clock.NewFake(day0))
	ctx := t.Context()
	j := r.enroll(r.newNode("tower1"))
	for _, bad := range []int{-1, guild.MaxPriority + 1} {
		if err := r.citadel.SetPriority(ctx, j.MachineID, bad); !errors.Is(err, guild.ErrBadPriority) {
			t.Errorf("priority %d: %v", bad, err)
		}
	}
	if err := r.citadel.SetPriority(ctx, j.MachineID, 7); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "Local", "Marte/Olimpo"} {
		if err := r.citadel.SetTimezone(ctx, bad); !errors.Is(err, guild.ErrBadTimezone) {
			t.Errorf("zone %q: %v", bad, err)
		}
	}
	if err := r.citadel.SetTimezone(ctx, "America/Sao_Paulo"); err != nil {
		t.Fatal(err)
	}
	cfg, err := r.citadel.Config(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Timezone != "America/Sao_Paulo" {
		t.Errorf("guild.config zone = %q", cfg.Timezone)
	}
	for _, m := range cfg.Machines {
		if m.MachineID == j.MachineID && m.Priority != 7 {
			t.Errorf("guild.config priority = %d", m.Priority)
		}
	}
}
