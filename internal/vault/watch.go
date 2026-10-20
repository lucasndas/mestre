package vault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lucasndas/mestre/pkg/clock"
)

// The defaults of the validity notices (docs/plataforma/cofre.md#credenciais-de-site).
const (
	// ExpiryLead is how long before expires_at the owner is told.
	ExpiryLead = 7 * 24 * time.Hour
	// StaleAfter is how long a credential with no validity goes without being
	// replaced before the owner is reminded to replace it.
	StaleAfter = 90 * 24 * time.Hour
	// WatchEvery is how often the watcher looks.
	WatchEvery = time.Hour
)

// NoticeKind is the reason of a notice.
type NoticeKind string

// The reasons.
const (
	// NoticeExpiring: expires_at is near (or past).
	NoticeExpiring NoticeKind = "expiring"
	// NoticeStale: a credential with no validity was not replaced for StaleAfter.
	NoticeStale NoticeKind = "stale"
)

// Notice is what the owner of a credential is told: which one and why, never
// the value.
type Notice struct {
	Name string
	Kind NoticeKind
	// Owner is the owner_user of the credential.
	Owner string
	// Domains are the sites of the credential.
	Domains []string
	// Expires is expires_at (NoticeExpiring), Since is the last replacement
	// (NoticeStale).
	Expires, Since time.Time
	// Days are the days left (NoticeExpiring; zero or negative once it expired)
	// or gone since the replacement (NoticeStale).
	Days int
}

// ExpiryWatcher tells the owner of a site credential and the Owner when it is
// about to expire, and reminds them to replace the ones that never expire. Each
// notice is made once: the memory is in the Guild database (vault_notices), so a
// restart does not repeat it, and it is for the expiry (or the replacement) it
// was made for: a credential that is replaced starts over.
type ExpiryWatcher struct {
	Vault *Vault
	Clock clock.Clock
	// Notify tells the people. A notice it fails to deliver is tried again at
	// the next look; one it delivers is not made again.
	Notify func(ctx context.Context, n Notice) error
	// Lead and Stale default to ExpiryLead and StaleAfter.
	Lead, Stale time.Duration
	Logger      *slog.Logger
}

func (w *ExpiryWatcher) lead() time.Duration {
	if w.Lead > 0 {
		return w.Lead
	}
	return ExpiryLead
}

func (w *ExpiryWatcher) stale() time.Duration {
	if w.Stale > 0 {
		return w.Stale
	}
	return StaleAfter
}

func (w *ExpiryWatcher) clock() clock.Clock {
	if w.Clock != nil {
		return w.Clock
	}
	return clock.Real{}
}

func (w *ExpiryWatcher) log() *slog.Logger {
	if w.Logger != nil {
		return w.Logger
	}
	return slog.New(slog.DiscardHandler)
}

// Scan looks at every site credential and sends the notices that are due, and
// returns how many were sent. It reads only the clear metadata, so it works while
// the vault is closed too.
func (w *ExpiryWatcher) Scan(ctx context.Context) (int, error) {
	if w.Vault == nil || w.Notify == nil {
		return 0, errors.New("vault: the watcher needs the vault and a way to notify")
	}
	infos, err := w.Vault.List(ctx)
	if err != nil {
		return 0, err
	}
	now := w.clock().Now()
	sent := 0
	live := map[string]bool{}
	for _, i := range infos {
		if i.Type != TypeSiteCredential || i.Site == nil {
			continue
		}
		live[i.Name] = true
		n, ref, due, err := w.due(ctx, i, now)
		if err != nil {
			return sent, err
		}
		if !due {
			continue
		}
		if err := w.Notify(ctx, n); err != nil {
			w.log().Warn("vault: a notice about a credential was not delivered, it is tried again", "secret", i.Name, "err", err)
			continue
		}
		if err := w.remember(ctx, i.Name, n.Kind, ref, now); err != nil {
			return sent, err
		}
		sent++
	}
	return sent, w.forget(ctx, live)
}

// due says whether a notice is owed for the credential, and the reference the
// memory keeps of it: expires_at for the expiry, the moment the notice counts from
// for the reminder.
func (w *ExpiryWatcher) due(ctx context.Context, i Info, now time.Time) (Notice, int64, bool, error) {
	site := i.Site
	n := Notice{Name: i.Name, Owner: site.OwnerUser, Domains: site.Domains}
	if site.ExpiresAt != nil {
		n.Kind, n.Expires = NoticeExpiring, *site.ExpiresAt
		n.Days = int(site.ExpiresAt.Sub(now).Hours() / 24)
		if site.ExpiresAt.Sub(now) > w.lead() {
			return n, 0, false, nil
		}
		ref, _, found, err := w.noticed(ctx, i.Name, NoticeExpiring)
		if err != nil {
			return n, 0, false, err
		}
		exp := site.ExpiresAt.UnixMilli()
		return n, exp, !found || ref != exp, nil
	}
	// no validity: the reminder counts from the last replacement or the last reminder
	n.Kind, n.Since = NoticeStale, i.UpdatedAt
	_, at, found, err := w.noticed(ctx, i.Name, NoticeStale)
	if err != nil {
		return n, 0, false, err
	}
	from := i.UpdatedAt
	if found && at.After(from) {
		from = at
	}
	n.Days = int(now.Sub(i.UpdatedAt).Hours() / 24)
	return n, from.UnixMilli(), now.Sub(from) >= w.stale(), nil
}

// noticed reads what the memory has of a notice.
func (w *ExpiryWatcher) noticed(ctx context.Context, name string, kind NoticeKind) (ref int64, at time.Time, found bool, err error) {
	var ms int64
	err = w.Vault.cfg.DB.Reader().QueryRowContext(ctx, `SELECT ref, noticed_at FROM vault_notices WHERE name = ? AND kind = ?`, name, string(kind)).Scan(&ref, &ms)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, time.Time{}, false, nil
	case err != nil:
		return 0, time.Time{}, false, fmt.Errorf("vault: read the notices of %q: %w", name, err)
	}
	return ref, time.UnixMilli(ms).UTC(), true, nil
}

func (w *ExpiryWatcher) remember(ctx context.Context, name string, kind NoticeKind, ref int64, now time.Time) error {
	_, err := w.Vault.cfg.DB.Writer().ExecContext(ctx, `INSERT INTO vault_notices (name, kind, ref, noticed_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (name, kind) DO UPDATE SET ref = excluded.ref, noticed_at = excluded.noticed_at`, name, string(kind), ref, now.UnixMilli())
	if err != nil {
		return fmt.Errorf("vault: remember the notice of %q: %w", name, err)
	}
	return nil
}

// forget drops the memory of the credentials that no longer exist.
func (w *ExpiryWatcher) forget(ctx context.Context, live map[string]bool) error {
	rows, err := w.Vault.cfg.DB.Reader().QueryContext(ctx, `SELECT DISTINCT name FROM vault_notices`)
	if err != nil {
		return fmt.Errorf("vault: read the notices: %w", err)
	}
	var gone []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		if !live[name] {
			gone = append(gone, name)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, name := range gone {
		if _, err := w.Vault.cfg.DB.Writer().ExecContext(ctx, `DELETE FROM vault_notices WHERE name = ?`, name); err != nil {
			return fmt.Errorf("vault: forget the notices of %q: %w", name, err)
		}
	}
	return nil
}

// Run looks at once and then every WatchEvery until the context ends.
func (w *ExpiryWatcher) Run(ctx context.Context) error {
	look := func() {
		if _, err := w.Scan(ctx); err != nil && ctx.Err() == nil {
			w.log().Warn("vault: the look at the validity of the credentials failed", "err", err)
		}
	}
	look()
	t := w.clock().NewTicker(WatchEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C():
			look()
		}
	}
}
