// Package vault keeps the Mestre secrets (docs/plataforma/cofre.md): each
// value encrypted with AES-256-GCM bound to its row, a data key protected by
// the operating system (KeyStore), a recovery envelope for backups, and an
// API that is write-only: nothing here returns a stored value except Use,
// which hands it to a callback for one purpose and wipes it afterwards.
package vault

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Type is the kind of secret (table in docs/plataforma/cofre.md#o-que-fica-no-cofre).
type Type string

// Secret types.
const (
	TypeTelegramBotToken   Type = "telegram_bot_token"
	TypeGitHubAppKey       Type = "github_app_key"
	TypeGitHubUserToken    Type = "github_user_token"
	TypeProviderAPIKey     Type = "provider_api_key"
	TypeSiteCredential     Type = "site_credential"
	TypeOperatorCaptureKey Type = "operator_capture_key"
	TypeCAKey              Type = "ca_key"
)

var knownTypes = []Type{
	TypeTelegramBotToken, TypeGitHubAppKey, TypeGitHubUserToken, TypeProviderAPIKey,
	TypeSiteCredential, TypeOperatorCaptureKey, TypeCAKey,
}

// SiteKind is the kind of a site credential.
type SiteKind string

// Site credential kinds (docs/plataforma/cofre.md#credenciais-de-site).
const (
	KindMainPassword SiteKind = "senha_principal"
	KindAppPassword  SiteKind = "senha_de_app"
	KindToken        SiteKind = "token"
	KindAPIKey       SiteKind = "chave_api"
)

// SiteMeta is the clear metadata of a site credential, read without
// decrypting.
type SiteMeta struct {
	OwnerUser string     `json:"owner_user"`
	Classes   []string   `json:"classes"`
	Domains   []string   `json:"domains"`
	Kind      SiteKind   `json:"kind"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
}

// Info is everything the vault reveals about a secret: never the value.
type Info struct {
	Name        string     `json:"name"`
	Type        Type       `json:"type"`
	Fingerprint string     `json:"fingerprint"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	LastUsedAt  *time.Time `json:"last_used_at,omitempty"`
	Site        *SiteMeta  `json:"site,omitempty"`
}

// UseEvent is reported for every read of a secret (audit: name, purpose).
type UseEvent struct {
	Name    string
	Type    Type
	Purpose string
	At      time.Time
}

// Config wires a Vault.
type Config struct {
	DB   *store.DB // Guild database (table secrets)
	Keys KeyStore
	// EnvelopePath is cofre-envelope.bin, next to mestre.db.
	EnvelopePath string
	// RecoveryPublicKey is the X25519 public key derived from the recovery
	// key; the Guild keeps it in the database.
	RecoveryPublicKey []byte
	// Masker receives every value read, before the caller sees it.
	Masker *logging.Masker
	Clock  clock.Clock // nil means clock.Real
	// OnAlert receives a grave alert when the vault cannot open.
	OnAlert func(Alert)
	// OnUse receives every read, for the audit trail.
	OnUse func(UseEvent)

	// fastEnvelope, set only by tests that are not about durability, writes
	// the envelope without flushing it to disk.
	fastEnvelope bool
}

// ErrClosed is wrapped by every error of a vault that could not open.
var ErrClosed = errors.New("vault is closed")

// ErrNotFound is wrapped when the named secret does not exist.
var ErrNotFound = errors.New("secret not found")

// ErrVerification is wrapped when a secret does not decrypt: wrong key or a
// row that was altered or copied.
var ErrVerification = errors.New("secret failed verification")

// ClosedError explains why the vault is closed.
type ClosedError struct{ Reason error }

func (e *ClosedError) Error() string { return "vault is closed: " + e.Reason.Error() }
func (e *ClosedError) Unwrap() []error {
	return []error{ErrClosed, e.Reason}
}

// Vault is the open (or closed) vault of the Citadel.
type Vault struct {
	cfg Config

	mu     sync.RWMutex
	ring   *keyring
	closed error // non-nil when the vault refuses to work

	// failAfter, set by tests, simulates a crash after a rotation step.
	failAfter func(step string) error
	// durableHook, set by tests, wraps the durable transaction of Rotate.
	durableHook func(ctx context.Context, fn func(*sql.Tx) error) error
}

// Create generates the first data key at the creation of the Guild, saves it
// in cfg.Keys and writes the recovery envelope. It refuses when a key
// already exists or when secrets are already stored (a lost key).
func Create(ctx context.Context, cfg Config) (*Vault, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if data, err := cfg.Keys.Load(ctx); err == nil {
		zero(data)
		return nil, errors.New("vault: a key already exists; refusing to replace it")
	} else if !errors.Is(err, ErrNoKey) {
		return nil, fmt.Errorf("vault: create: %w", err)
	}
	// Rows without a key mean the key was lost, not that the vault is new: a
	// new key would never open them. The way back is the recovery envelope.
	var rows int
	if err := cfg.DB.Reader().QueryRowContext(ctx, `SELECT count(*) FROM secrets`).Scan(&rows); err != nil {
		return nil, fmt.Errorf("vault: create: %w", err)
	}
	if rows > 0 {
		return nil, fmt.Errorf("vault: create: the key is missing but %d secrets are stored; restore the key with the recovery key (mestre install --restore) instead of creating a new one", rows)
	}
	k, err := newRandomKey(1)
	if err != nil {
		return nil, err
	}
	ring := &keyring{keys: []*dataKey{k}}
	if err := writeRing(ctx, cfg, ring); err != nil {
		ring.destroy()
		return nil, fmt.Errorf("vault: create: %w", err)
	}
	return &Vault{cfg: cfg, ring: ring}, nil
}

// Open loads the data key and checks it against the stored secrets. It
// fails only on a wrong configuration: when the key is missing, refused or
// wrong, it returns a closed vault (Err reports why, every secret operation
// fails with ErrClosed) and raises a grave alert, so the engine keeps
// running everything that does not need secrets. An interrupted key
// rotation is settled here.
func Open(ctx context.Context, cfg Config) (*Vault, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	v := &Vault{cfg: cfg}
	if err := v.load(ctx); err != nil {
		v.closed = err
		var check string
		if ue := (*UnsafeKeyFileError)(nil); errors.As(err, &ue) {
			check = ue.Check
		}
		if cfg.OnAlert != nil {
			cfg.OnAlert(Alert{Severity: SeverityGrave, Check: check, Err: &ClosedError{Reason: err}})
		}
	}
	return v, nil
}

func (c *Config) validate() error {
	switch {
	case c.DB == nil:
		return errors.New("vault: Config.DB is required")
	case c.DB.IsReplica():
		return ErrReplica
	case c.Keys == nil:
		return errors.New("vault: Config.Keys is required")
	case c.EnvelopePath == "":
		return errors.New("vault: Config.EnvelopePath is required")
	case len(c.RecoveryPublicKey) != 32:
		return errors.New("vault: Config.RecoveryPublicKey must be a 32-byte X25519 public key")
	case c.Masker == nil:
		return errors.New("vault: Config.Masker is required")
	}
	if c.Clock == nil {
		c.Clock = clock.Real{}
	}
	return nil
}

func (v *Vault) load(ctx context.Context) error {
	data, err := v.cfg.Keys.Load(ctx)
	if err != nil {
		if errors.Is(err, ErrNoKey) {
			return errors.New("vault: the key is missing")
		}
		return err
	}
	ring, err := parseKeyring(data)
	zero(data)
	if err != nil {
		return err
	}
	if err := v.settle(ctx, ring); err != nil {
		ring.destroy()
		return err
	}
	v.ring = ring
	return nil
}

// Err is nil for an open vault and the reason (wrapping ErrClosed) for a
// closed one.
func (v *Vault) Err() error {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.errLocked()
}

func (v *Vault) errLocked() error {
	if v.closed != nil {
		return &ClosedError{Reason: v.closed}
	}
	return nil
}

// Close wipes the keys held in memory; the vault is closed afterwards.
func (v *Vault) Close() {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.ring != nil {
		v.ring.destroy()
		v.ring = nil
	}
	if v.closed == nil {
		v.closed = errors.New("vault: closed by the engine")
	}
}

// Put stores (or replaces) a secret. The value is encrypted with the
// current key; value is not retained and the caller should wipe it. site is
// required for TypeSiteCredential and refused for every other type.
func (v *Vault) Put(ctx context.Context, name string, typ Type, value []byte, site *SiteMeta) error {
	if err := validateName(name); err != nil {
		return err
	}
	if !slices.Contains(knownTypes, typ) {
		return fmt.Errorf("vault: secret %q: unknown type %q", name, typ)
	}
	if len(value) == 0 {
		return fmt.Errorf("vault: secret %q: empty value", name)
	}
	if err := validateSite(name, typ, site); err != nil {
		return err
	}
	if err := validateValue(name, typ, value); err != nil {
		return err
	}
	cols, err := siteColumns(site)
	if err != nil {
		return fmt.Errorf("vault: secret %q: %w", name, err)
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	if err := v.errLocked(); err != nil {
		return fmt.Errorf("put secret %q: %w", name, err)
	}
	k := v.ring.current()
	ct, nonce := k.seal(name, typ, value)
	now := v.cfg.Clock.Now().UnixMilli()
	_, err = v.cfg.DB.Writer().ExecContext(ctx, `
		INSERT INTO secrets (name, type, ciphertext, nonce, key_version, fingerprint,
			owner_user, classes, domains, kind, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (name) DO UPDATE SET
			type = excluded.type, ciphertext = excluded.ciphertext, nonce = excluded.nonce,
			key_version = excluded.key_version, fingerprint = excluded.fingerprint,
			owner_user = excluded.owner_user, classes = excluded.classes, domains = excluded.domains,
			kind = excluded.kind, expires_at = excluded.expires_at, updated_at = excluded.updated_at`,
		name, string(typ), ct, nonce, k.version, k.fingerprint(value),
		cols.owner, cols.classes, cols.domains, cols.kind, cols.expires, now, now)
	if err != nil {
		return fmt.Errorf("vault: put secret %q: %w", name, err)
	}
	return nil
}

// Use decrypts the named secret, registers it in the masker (the whole value,
// each sensitive component and each line; see maskParts), records the
// read (last_used_at and OnUse with purpose) and passes it to fn. The
// buffer is wiped when fn returns: fn must not keep it.
func (v *Vault) Use(ctx context.Context, name, purpose string, fn func(value []byte) error) error {
	if strings.TrimSpace(purpose) == "" {
		return fmt.Errorf("vault: reading secret %q needs a purpose", name)
	}
	v.mu.RLock()
	plain, typ, err := v.decrypt(ctx, name)
	v.mu.RUnlock()
	if err != nil {
		return err
	}
	defer zero(plain)
	for _, part := range maskParts(typ, plain) {
		v.cfg.Masker.Register(part)
	}

	now := v.cfg.Clock.Now()
	if _, err := v.cfg.DB.Writer().ExecContext(ctx,
		`UPDATE secrets SET last_used_at = ? WHERE name = ?`, now.UnixMilli(), name); err != nil {
		return fmt.Errorf("vault: record use of %q: %w", name, err)
	}
	if v.cfg.OnUse != nil {
		v.cfg.OnUse(UseEvent{Name: name, Type: typ, Purpose: purpose, At: now})
	}
	return fn(plain)
}

// decrypt must run under v.mu (read).
func (v *Vault) decrypt(ctx context.Context, name string) ([]byte, Type, error) {
	if err := v.errLocked(); err != nil {
		return nil, "", fmt.Errorf("read secret %q: %w", name, err)
	}
	var (
		typ        string
		ct, nonce  []byte
		keyVersion uint32
	)
	err := v.cfg.DB.Reader().QueryRowContext(ctx,
		`SELECT type, ciphertext, nonce, key_version FROM secrets WHERE name = ?`, name).
		Scan(&typ, &ct, &nonce, &keyVersion)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, "", fmt.Errorf("vault: secret %q: %w", name, ErrNotFound)
	}
	if err != nil {
		return nil, "", fmt.Errorf("vault: read secret %q: %w", name, err)
	}
	k := v.ring.get(keyVersion)
	if k == nil {
		return nil, "", fmt.Errorf("vault: secret %q: key version %d is not available: %w", name, keyVersion, ErrVerification)
	}
	plain, err := k.open(name, Type(typ), ct, nonce)
	if err != nil {
		return nil, "", fmt.Errorf("vault: secret %q: %w (wrong key, or the row was altered)", name, ErrVerification)
	}
	return plain, Type(typ), nil
}

// Delete removes a secret.
func (v *Vault) Delete(ctx context.Context, name string) error {
	if err := v.Err(); err != nil {
		return fmt.Errorf("delete secret %q: %w", name, err)
	}
	res, err := v.cfg.DB.Writer().ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, name)
	if err != nil {
		return fmt.Errorf("vault: delete secret %q: %w", name, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("vault: secret %q: %w", name, ErrNotFound)
	}
	return nil
}

const infoColumns = `name, type, fingerprint, created_at, updated_at, last_used_at,
	owner_user, classes, domains, kind, expires_at`

// List returns the metadata of every secret, ordered by name. It reads only
// clear columns, so it works on a closed vault too.
func (v *Vault) List(ctx context.Context) ([]Info, error) {
	rows, err := v.cfg.DB.Reader().QueryContext(ctx, `SELECT `+infoColumns+` FROM secrets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("vault: list secrets: %w", err)
	}
	defer rows.Close()
	var out []Info
	for rows.Next() {
		i, err := scanInfo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("vault: list secrets: %w", err)
	}
	return out, nil
}

// Info returns the metadata of one secret.
func (v *Vault) Info(ctx context.Context, name string) (Info, error) {
	i, err := scanInfo(v.cfg.DB.Reader().QueryRowContext(ctx, `SELECT `+infoColumns+` FROM secrets WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Info{}, fmt.Errorf("vault: secret %q: %w", name, ErrNotFound)
	}
	return i, err
}

func scanInfo(r interface{ Scan(...any) error }) (Info, error) {
	var (
		i                         Info
		typ                       string
		created, updated          int64
		lastUsed, expires         sql.NullInt64
		owner, classes, doms, knd sql.NullString
	)
	if err := r.Scan(&i.Name, &typ, &i.Fingerprint, &created, &updated, &lastUsed,
		&owner, &classes, &doms, &knd, &expires); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Info{}, err
		}
		return Info{}, fmt.Errorf("vault: read secret metadata: %w", err)
	}
	i.Type = Type(typ)
	i.CreatedAt = time.UnixMilli(created).UTC()
	i.UpdatedAt = time.UnixMilli(updated).UTC()
	if lastUsed.Valid {
		t := time.UnixMilli(lastUsed.Int64).UTC()
		i.LastUsedAt = &t
	}
	if i.Type == TypeSiteCredential {
		m := &SiteMeta{OwnerUser: owner.String, Kind: SiteKind(knd.String)}
		if err := json.Unmarshal([]byte(classes.String), &m.Classes); err != nil {
			return Info{}, fmt.Errorf("vault: secret %q: classes: %w", i.Name, err)
		}
		if err := json.Unmarshal([]byte(doms.String), &m.Domains); err != nil {
			return Info{}, fmt.Errorf("vault: secret %q: domains: %w", i.Name, err)
		}
		if expires.Valid {
			t := time.UnixMilli(expires.Int64).UTC()
			m.ExpiresAt = &t
		}
		i.Site = m
	}
	return i, nil
}

func validateName(name string) error {
	if name == "" || len(name) > 200 {
		return fmt.Errorf("vault: secret name must have 1 to 200 bytes")
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return fmt.Errorf("vault: secret name %q has control characters", name)
		}
	}
	return nil
}

var siteKinds = []SiteKind{KindMainPassword, KindAppPassword, KindToken, KindAPIKey}

func validateSite(name string, typ Type, site *SiteMeta) error {
	if typ != TypeSiteCredential {
		if site != nil {
			return fmt.Errorf("vault: secret %q: site metadata is only for %s", name, TypeSiteCredential)
		}
		return nil
	}
	switch {
	case site == nil:
		return fmt.Errorf("vault: secret %q: a site credential needs its metadata", name)
	case site.OwnerUser == "":
		return fmt.Errorf("vault: secret %q: owner_user is required", name)
	case len(site.Classes) == 0:
		return fmt.Errorf("vault: secret %q: classes is required", name)
	case len(site.Domains) == 0:
		return fmt.Errorf("vault: secret %q: domains is required", name)
	case !slices.Contains(siteKinds, site.Kind):
		return fmt.Errorf("vault: secret %q: kind must be one of %v", name, siteKinds)
	}
	return nil
}

type siteCols struct {
	owner, classes, domains, kind sql.NullString
	expires                       sql.NullInt64
}

func siteColumns(site *SiteMeta) (siteCols, error) {
	var c siteCols
	if site == nil {
		return c, nil
	}
	cl, err := json.Marshal(site.Classes)
	if err != nil {
		return c, err
	}
	dm, err := json.Marshal(site.Domains)
	if err != nil {
		return c, err
	}
	c.owner = sql.NullString{String: site.OwnerUser, Valid: true}
	c.classes = sql.NullString{String: string(cl), Valid: true}
	c.domains = sql.NullString{String: string(dm), Valid: true}
	c.kind = sql.NullString{String: string(site.Kind), Valid: true}
	if site.ExpiresAt != nil {
		c.expires = sql.NullInt64{Int64: site.ExpiresAt.UnixMilli(), Valid: true}
	}
	return c, nil
}

// writeRing saves the keyring in the key store and the matching envelope.
// Order matters for rotation; see Rotate.
func writeRing(ctx context.Context, cfg Config, ring *keyring) error {
	if err := saveKeys(ctx, cfg, ring); err != nil {
		return err
	}
	return writeEnvelope(cfg, ring)
}

func saveKeys(ctx context.Context, cfg Config, ring *keyring) error {
	data := ring.marshal()
	defer zero(data)
	return cfg.Keys.Save(ctx, data)
}

func writeEnvelope(cfg Config, ring *keyring) error {
	env, err := sealEnvelope(cfg.RecoveryPublicKey, ring)
	if err != nil {
		return err
	}
	if err := writeFile(cfg.EnvelopePath, env, !cfg.fastEnvelope); err != nil {
		return fmt.Errorf("vault: write recovery envelope: %w", err)
	}
	return nil
}

// envelopeCurrent reports whether the envelope on disk is made for the
// configured recovery key and holds exactly versions.
func envelopeCurrent(cfg Config, versions []uint32) bool {
	b, err := os.ReadFile(cfg.EnvelopePath)
	if err != nil {
		return false
	}
	info, err := parseEnvelopeHeader(b)
	return err == nil && slices.Equal(info.recipient, cfg.RecoveryPublicKey) && slices.Equal(info.versions, versions)
}
