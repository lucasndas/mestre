package guild

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"
)

// ErrOutsideLocalNetwork: the machine is not reached by the others through the
// local network (it entered by an address), so it is never promoted
// (docs/guilda/cidadela-e-torres.md#promoção).
var ErrOutsideLocalNetwork = errors.New("guild: the machine is outside the local network")

// ErrBadPriority and ErrBadTimezone are the refusals of the settings the Owner
// changes in the panel.
var (
	ErrBadPriority = errors.New("guild: the priority is out of range")
	ErrBadTimezone = errors.New("guild: not a time zone of the IANA database")
)

// The bounds of the priority of a machine.
const (
	MinPriority = 0
	MaxPriority = 1000
)

// LocalNetworks are the networks of the interfaces of this machine, which is
// where "the local network" of the Guild is told from.
func LocalNetworks() []*net.IPNet {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			out = append(out, n)
		}
	}
	return out
}

// OutsideLocalNetwork says whether none of the addresses the machine reported
// is in one of the networks; a machine that reported none counts as outside,
// since nobody knows how to reach it.
func (m Machine) OutsideLocalNetwork(local []*net.IPNet) bool {
	for _, ip := range m.IPs {
		for _, n := range local {
			if n.Contains(ip) {
				return false
			}
		}
	}
	return true
}

// PassTarget says whether the Citadel may be passed to the machine (the planned
// switch of the panel): active, not the Citadel, not excluded and in the local
// network. The refusal wraps the reason (ErrNotActive, ErrCitadelProtected,
// ErrExcludedMachine, ErrOutsideLocalNetwork).
func (c *Citadel) PassTarget(ctx context.Context, machineID string, local []*net.IPNet) error {
	m, err := c.activeNotCitadel(ctx, machineID)
	if err != nil {
		return err
	}
	if m.Excluded {
		return ErrExcludedMachine
	}
	if m.OutsideLocalNetwork(local) {
		return ErrOutsideLocalNetwork
	}
	return nil
}

// SetPriority sets the priority of a machine for the choice of the leading
// Tower (the Owner, in the panel). guild.config carries it: sending it belongs
// to the caller.
func (c *Citadel) SetPriority(ctx context.Context, machineID string, priority int) error {
	if priority < MinPriority || priority > MaxPriority {
		return fmt.Errorf("%w: %d", ErrBadPriority, priority)
	}
	if _, err := loadMachine(ctx, c.db.Reader(), machineID); err != nil {
		return err
	}
	res, err := c.db.Writer().ExecContext(ctx, `UPDATE machines SET priority = ? WHERE machine_id = ? AND state = 'active'`, priority, machineID)
	if err != nil {
		return fmt.Errorf("guild: set the priority: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: %s", ErrNotActive, machineID)
	}
	return nil
}

// Timezone is the time zone of the Guild.
func (c *Citadel) Timezone(ctx context.Context) (string, error) {
	g, err := readGuild(ctx, c.db.Reader())
	return g.Timezone, err
}

// SetTimezone changes the time zone of the Guild (the Owner, in the panel). It
// does not change with a switch of the Citadel; guild.config carries it.
func (c *Citadel) SetTimezone(ctx context.Context, name string) error {
	if name == "" || strings.EqualFold(name, "local") {
		return fmt.Errorf("%w: %q", ErrBadTimezone, name)
	}
	if _, err := time.LoadLocation(name); err != nil {
		return fmt.Errorf("%w: %q", ErrBadTimezone, name)
	}
	if _, err := c.db.Writer().ExecContext(ctx, `UPDATE guild SET timezone = ? WHERE id = 1`, name); err != nil {
		return fmt.Errorf("guild: set the time zone: %w", err)
	}
	return nil
}
