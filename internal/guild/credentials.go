package guild

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/transport"
)

// Credentials is the transport.CredentialChecker of the Guild, consulted on
// every handshake: it refuses the revoked certificates, by serial, and every
// certificate of a machine that is no longer active (removed by the Owner or
// left), by machine_id, so a certificate the registry never knew (a renewal
// lost in a change of Citadel) falls with its machine. It is a list of
// refusals, not of permissions: a Tower whose replica is behind must still
// accept the renewed certificate of the Citadel it replicates from
// (docs/arquitetura/seguranca.md#tls-e-ca-interna). The Citadel loads it from
// the Guild database and revokes through it; a Tower loads it from its
// replica and adds the list of guild.config, so the revocation holds after a
// change of Citadel as well.
type Credentials struct {
	mu       sync.RWMutex
	revoked  map[string]struct{}
	machines map[string]struct{} // machines that are not active
	// what the last guild.config said (a Tower): kept across Reload, so a
	// replica that is behind does not forget it
	learnedRevoked  map[string]struct{}
	learnedMachines map[string]struct{}
}

var _ transport.CredentialChecker = (*Credentials)(nil)

// NewCredentials returns a list that revokes the given serials.
func NewCredentials(revoked ...string) *Credentials {
	c := &Credentials{machines: map[string]struct{}{}}
	c.Replace(revoked)
	return c
}

// LoadCredentials reads the refusals of the Guild database.
func LoadCredentials(ctx context.Context, db *store.DB) (*Credentials, error) {
	c := NewCredentials()
	if err := c.Reload(ctx, db); err != nil {
		return nil, err
	}
	return c, nil
}

// Reload replaces the list with the refusals of the database: the revoked
// serials and the machines that are not active. A replica without the schema
// yet (a Tower that just entered, before its full copy) refuses nothing more
// than the list held: it learns the rest from guild.config.
func (c *Credentials) Reload(ctx context.Context, db *store.DB) error {
	if db.IsReplica() {
		if v, err := db.Version(ctx); err != nil {
			return fmt.Errorf("guild: read the revoked credentials: %w", err)
		} else if v == 0 {
			return nil
		}
	}
	serials, err := column(ctx, db, `SELECT serial FROM machine_credentials WHERE revoked_at IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("guild: read the revoked credentials: %w", err)
	}
	retired, err := column(ctx, db, `SELECT machine_id FROM machines WHERE state <> 'active'`)
	if err != nil {
		return fmt.Errorf("guild: read the retired machines: %w", err)
	}
	m := make(map[string]struct{}, len(retired))
	for _, id := range retired {
		m[id] = struct{}{}
	}
	r := make(map[string]struct{}, len(serials))
	for _, s := range serials {
		r[s] = struct{}{}
	}
	// the union with the last guild.config: the replica may not have caught
	// up with a removal the Citadel announced
	c.mu.Lock()
	for s := range c.learnedRevoked {
		r[s] = struct{}{}
	}
	for id := range c.learnedMachines {
		m[id] = struct{}{}
	}
	c.revoked, c.machines = r, m
	c.mu.Unlock()
	return nil
}

// Learn takes the refusals of a guild.config: the revoked serials and the
// machines that left. They hold until a later guild.config, across Reload.
func (c *Credentials) Learn(serials, machineIDs []string) {
	c.mu.Lock()
	c.learnedRevoked = make(map[string]struct{}, len(serials))
	for _, s := range serials {
		c.learnedRevoked[s] = struct{}{}
		c.revoked[s] = struct{}{}
	}
	c.learnedMachines = make(map[string]struct{}, len(machineIDs))
	for _, id := range machineIDs {
		c.learnedMachines[id] = struct{}{}
		c.machines[id] = struct{}{}
	}
	c.mu.Unlock()
}

func column(ctx context.Context, db *store.DB, q string) ([]string, error) {
	rows, err := db.Reader().QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// Replace sets the revoked serials to exactly serials; the retired machines
// stay.
func (c *Credentials) Replace(serials []string) {
	m := make(map[string]struct{}, len(serials))
	for _, s := range serials {
		m[s] = struct{}{}
	}
	c.mu.Lock()
	c.revoked = m
	c.mu.Unlock()
}

// Add revokes more serials.
func (c *Credentials) Add(serials ...string) {
	c.mu.Lock()
	for _, s := range serials {
		c.revoked[s] = struct{}{}
	}
	c.mu.Unlock()
}

// Retire refuses every certificate of these machines from now on.
func (c *Credentials) Retire(machineIDs ...string) {
	c.mu.Lock()
	for _, id := range machineIDs {
		c.machines[id] = struct{}{}
	}
	c.mu.Unlock()
}

// Retired lists the machines that are not active, sorted: what guild.config
// carries so a Tower refuses their certificates too.
func (c *Credentials) Retired() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.machines))
	for id := range c.machines {
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// Revoked lists the revoked serials, sorted: what guild.config carries.
func (c *Credentials) Revoked() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make([]string, 0, len(c.revoked))
	for s := range c.revoked {
		out = append(out, s)
	}
	slices.Sort(out)
	return out
}

// CheckCredential implements transport.CredentialChecker.
func (c *Credentials) CheckCredential(id transport.Identity, serial string) error {
	c.mu.RLock()
	_, bad := c.revoked[serial]
	_, gone := c.machines[id.MachineID]
	c.mu.RUnlock()
	switch {
	case bad:
		return fmt.Errorf("%w: %s (serial %s)", transport.ErrRevoked, id, serial)
	case gone:
		return fmt.Errorf("%w: %s (the machine left the Guild)", transport.ErrRevoked, id)
	}
	return nil
}
