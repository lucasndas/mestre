package guild_test

import (
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-CIDADELA-19 (the writes of the Owner and the planned switch): once the
// Citadel drains for a switch, an exclusion, a removal, a priority or a time
// zone written after the last LSN would never reach the new Citadel (an
// excluded machine could get the vault back, a removed one stay). They go
// through the same gate as the decision on an entry and are refused with
// ErrSuspended while it drains, changing nothing; when it admits again they work.
func TestTheWritesOfTheOwnerAreRefusedWhileTheCitadelDrainsForASwitch(t *testing.T) {
	t.Parallel()
	var draining bool
	r := newRig(t, clock.NewFake(day0), func(c *guild.CitadelConfig) {
		c.Admit = func() (func(), bool) { return func() {}, !draining }
	})
	ctx := t.Context()
	j := r.enroll(r.newNode("tower1"))
	zone, err := r.citadel.Timezone(ctx)
	if err != nil {
		t.Fatal(err)
	}

	draining = true
	if err := r.citadel.SetExcluded(ctx, j.MachineID, true); !errors.Is(err, guild.ErrSuspended) {
		t.Errorf("SetExcluded while draining = %v", err)
	}
	if err := r.citadel.SetPriority(ctx, j.MachineID, 5); !errors.Is(err, guild.ErrSuspended) {
		t.Errorf("SetPriority while draining = %v", err)
	}
	if err := r.citadel.SetTimezone(ctx, "Asia/Tokyo"); !errors.Is(err, guild.ErrSuspended) {
		t.Errorf("SetTimezone while draining = %v", err)
	}
	if _, err := r.citadel.Remove(ctx, j.MachineID, "dono"); !errors.Is(err, guild.ErrSuspended) {
		t.Errorf("Remove while draining = %v", err)
	}
	ms, err := r.citadel.Machines(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range ms {
		if m.ID == j.MachineID {
			found = true
			if m.Excluded || m.Priority == 5 {
				t.Errorf("the machine changed while the Citadel drained: %+v", m)
			}
		}
	}
	if !found {
		t.Fatal("the machine was removed while the Citadel drained")
	}
	if got, _ := r.citadel.Timezone(ctx); got != zone {
		t.Errorf("time zone = %q, want %q", got, zone)
	}

	draining = false
	if err := r.citadel.SetExcluded(ctx, j.MachineID, true); err != nil {
		t.Errorf("SetExcluded after the gate opened: %v", err)
	}
	if err := r.citadel.SetPriority(ctx, j.MachineID, 5); err != nil {
		t.Errorf("SetPriority after the gate opened: %v", err)
	}
	if err := r.citadel.SetTimezone(ctx, "Asia/Tokyo"); err != nil {
		t.Errorf("SetTimezone after the gate opened: %v", err)
	}
	if _, err := r.citadel.Remove(ctx, j.MachineID, "dono"); err != nil {
		t.Errorf("Remove after the gate opened: %v", err)
	}
}
