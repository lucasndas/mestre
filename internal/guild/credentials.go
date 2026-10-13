package guild

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/transport"
)

// Credentials is the transport.CredentialChecker of the Guild: the list of
// the revoked certificates, by serial, consulted on every handshake. The
// Citadel loads it from machine_credentials and revokes through it; a Tower
// receives the same list in guild.config (Replace), so the revocation holds
// after a change of Citadel as well (docs/arquitetura/seguranca.md#tls-e-ca-interna).
type Credentials struct {
	mu      sync.RWMutex
	revoked map[string]struct{}
}

var _ transport.CredentialChecker = (*Credentials)(nil)

// NewCredentials returns a list that revokes the given serials.
func NewCredentials(revoked ...string) *Credentials {
	c := &Credentials{}
	c.Replace(revoked)
	return c
}

// LoadCredentials reads the revoked serials of the Guild database.
func LoadCredentials(ctx context.Context, db *store.DB) (*Credentials, error) {
	c := NewCredentials()
	if err := c.Reload(ctx, db); err != nil {
		return nil, err
	}
	return c, nil
}

// Reload replaces the list with the revoked serials of the database, for a
// machine that has just become the Citadel and replicated the table.
func (c *Credentials) Reload(ctx context.Context, db *store.DB) error {
	rows, err := db.Reader().QueryContext(ctx, `SELECT serial FROM machine_credentials WHERE revoked_at IS NOT NULL`)
	if err != nil {
		return fmt.Errorf("guild: read the revoked credentials: %w", err)
	}
	defer rows.Close()
	var serials []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return fmt.Errorf("guild: read the revoked credentials: %w", err)
		}
		serials = append(serials, s)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("guild: read the revoked credentials: %w", err)
	}
	c.Replace(serials)
	return nil
}

// Replace sets the list to exactly serials.
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
	c.mu.RUnlock()
	if bad {
		return fmt.Errorf("%w: %s (serial %s)", transport.ErrRevoked, id, serial)
	}
	return nil
}
