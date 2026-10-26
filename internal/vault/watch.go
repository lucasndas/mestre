package vault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mestre-ia/mestre/pkg/clock"
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
// notice is made once for each person: the memory is in the Guild database
// (vault_notices, one row per credential, reason and person), so a restart does
// not repeat it, and it is for the expiry (or the replacement) it was made for: a
// credential that is replaced starts over.
type ExpiryWatcher struct {
	Vault *Vault
	Clock clock.Clock
	// People are who is told of a notice (the owner_user and the Owners); nil
	// means the owner_user alone, and none returned means nobody to tell yet (it is
	// looked at again). Each person has a memory of his own: a
	// failed delivery to one is tried again at the next look without repeating
	// the others.
	People func(ctx context.Context, n Notice) ([]string, error)
	// Notify tells one person. A notice it fails to deliver to a person is tried
	// again for that person at the next look; one it delivers is not made again.
	Notify func(ctx context.Context, n Notice, person string) error
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
		told, err := w.scanOne(ctx, i, now)
		if err != nil {
			return sent, err
		}
		if told {
			sent++
		}
	}
	return sent, w.forget(ctx, live)
}

// scanOne sends what is owed for one credential, person by person, and reports
// whether anyone was told now.
func (w *ExpiryWatcher) scanOne(ctx context.Context, i Info, now time.Time) (bool, error) {
	n, ok := w.notice(i, now)
	if !ok {
		return false, nil
	}
	people := []string{n.Owner}
	if w.People != nil {
		var err error
		if people, err = w.People(ctx, n); err != nil {
			return false, err
		}
	}
	told := false
	for _, person := range people {
		ref, due, err := w.due(ctx, i, n, person, now)
		if err != nil {
			return told, err
		}
		if !due {
			continue
		}
		if err := w.Notify(ctx, n, person); err != nil {
			w.log().Warn("vault: a notice about a credential was not delivered, it is tried again", "secret", i.Name, "person", person, "err", err)
			continue
		}
		if err := w.remember(ctx, i.Name, n.Kind, person, ref, now); err != nil {
			return told, err
		}
		told = true
	}
	return told, nil
}

// notice is the notice of a credential and whether it may be owed at all (the
// expiry is near, or there is no validity).
func (w *ExpiryWatcher) notice(i Info, now time.Time) (Notice, bool) {
	site := i.Site
	n := Notice{Name: i.Name, Owner: site.OwnerUser, Domains: site.Domains}
	if site.ExpiresAt != nil {
		n.Kind, n.Expires = NoticeExpiring, *site.ExpiresAt
		n.Days = int(site.ExpiresAt.Sub(now).Hours() / 24)
		return n, site.ExpiresAt.Sub(now) <= w.lead()
	}
	n.Kind, n.Since = NoticeStale, i.UpdatedAt
	n.Days = int(now.Sub(i.UpdatedAt).Hours() / 24)
	return n, true
}

// due says whether the person is owed the notice, and the reference the memory
// keeps of it: expires_at for the expiry, the moment the notice counts from for
// the reminder.
func (w *ExpiryWatcher) due(ctx context.Context, i Info, n Notice, person string, now time.Time) (int64, bool, error) {
	ref, at, found, err := w.noticed(ctx, i.Name, n.Kind, person)
	if err != nil {
		return 0, false, err
	}
	if n.Kind == NoticeExpiring {
		exp := i.Site.ExpiresAt.UnixMilli()
		return exp, !found || ref != exp, nil
	}
	// no validity: the reminder counts from the last replacement or the last reminder
	from := i.UpdatedAt
	if found && at.After(from) {
		from = at
	}
	return from.UnixMilli(), now.Sub(from) >= w.stale(), nil
}

// noticed reads what the memory has of a notice.
func (w *ExpiryWatcher) noticed(ctx context.Context, name string, kind NoticeKind, person string) (ref int64, at time.Time, found bool, err error) {
	var ms int64
	err = w.Vault.cfg.DB.Reader().QueryRowContext(ctx, `SELECT ref, noticed_at FROM vault_notices WHERE name = ? AND kind = ? AND person = ?`, name, string(kind), person).Scan(&ref, &ms)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, time.Time{}, false, nil
	case err != nil:
		return 0, time.Time{}, false, fmt.Errorf("vault: read the notices of %q: %w", name, err)
	}
	return ref, time.UnixMilli(ms).UTC(), true, nil
}

func (w *ExpiryWatcher) remember(ctx context.Context, name string, kind NoticeKind, person string, ref int64, now time.Time) error {
	_, err := w.Vault.cfg.DB.Writer().ExecContext(ctx, `INSERT INTO vault_notices (name, kind, person, ref, noticed_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (name, kind, person) DO UPDATE SET ref = excluded.ref, noticed_at = excluded.noticed_at`, name, string(kind), person, ref, now.UnixMilli())
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
