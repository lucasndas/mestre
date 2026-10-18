package vault

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"

	"github.com/lucasndas/mestre/internal/store"
)

// Rotation steps, in order. A crash after any of them leaves every secret
// decryptable: the key store and the envelope hold both keys from
// "keyfile-staged" until the database commit has moved every row to the new
// key, and only then drop the old one (docs/plataforma/cofre.md#chave-do-cofre).
// The commit is durable (synchronous=FULL, store.DB.WriteDurable) before the
// old key is dropped: with the default synchronous=NORMAL a power loss could
// bring back rows that need the old key after the key file lost it.
const (
	stepKeyFileStaged  = "keyfile-staged"
	stepEnvelopeStaged = "envelope-staged"
	stepDBPartial      = "db-partial" // inside the transaction, after the first row
	stepDBCommitted    = "db-committed"
	stepKeyFileFinal   = "keyfile-final"
	stepEnvelopeFinal  = "envelope-final"
)

func (v *Vault) step(name string) error {
	if v.failAfter != nil {
		return v.failAfter(name)
	}
	return nil
}

// ErrNotDurable is returned by Rotate when the database is opened without
// flushes (store.Options.Synchronous OFF): the rows would move to the new key
// in a commit that a power loss can take back after the old key is gone.
var ErrNotDurable = errors.New("vault: the database does not flush its commits; the key is not rotated")

// Rotate replaces the data key: it generates a new version, re-encrypts
// every secret with it in one transaction, envelopes it to the recovery
// public key and drops the old key. Sending the new vault to the Towers is
// the replication's job (M2). It refuses a database opened without flushes
// (ErrNotDurable), before it touches anything.
func (v *Vault) Rotate(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.errLocked(); err != nil {
		return fmt.Errorf("rotate key: %w", err)
	}
	if !v.cfg.DB.Flushes() {
		return fmt.Errorf("rotate key: %w", ErrNotDurable)
	}
	old := v.ring
	nk, err := newRandomKey(old.current().version + 1)
	if err != nil {
		return err
	}
	staged := &keyring{keys: append(slices.Clone(old.keys), nk)}

	// 1. Both keys on disk before any row changes.
	if err := saveKeys(ctx, v.cfg, staged); err != nil {
		nk.destroy()
		return fmt.Errorf("vault: rotate key: %w", err)
	}
	if err := v.step(stepKeyFileStaged); err != nil {
		return err
	}
	if err := writeEnvelope(v.cfg, staged); err != nil {
		return fmt.Errorf("vault: rotate key: %w", err)
	}
	if err := v.step(stepEnvelopeStaged); err != nil {
		return err
	}

	// 2. Every row to the new key, all or nothing, in a commit that is on
	// disk before step 3 drops the old key.
	if err := v.reencrypt(ctx, old, nk); err != nil {
		return err
	}
	v.ring = staged // rows now need nk; old stays until the disk drops it
	if err := v.step(stepDBCommitted); err != nil {
		return err
	}

	// 3. Drop the old key.
	final := &keyring{keys: []*dataKey{nk}}
	if err := saveKeys(ctx, v.cfg, final); err != nil {
		return fmt.Errorf("vault: rotate key: %w", err)
	}
	if err := v.step(stepKeyFileFinal); err != nil {
		return err
	}
	if err := writeEnvelope(v.cfg, final); err != nil {
		return fmt.Errorf("vault: rotate key: %w", err)
	}
	if err := v.step(stepEnvelopeFinal); err != nil {
		return err
	}
	old.destroy()
	v.ring = final
	return nil
}

func (v *Vault) reencrypt(ctx context.Context, old *keyring, nk *dataKey) error {
	return v.writeDurable(ctx, func(tx *sql.Tx) error {
		// The rows move to the new key only in a commit that is on disk when
		// it returns; check it rather than trust the connection settings.
		var sync int
		if err := tx.QueryRowContext(ctx, `PRAGMA synchronous`).Scan(&sync); err != nil {
			return fmt.Errorf("vault: rotate key: %w", err)
		}
		if sync < 2 {
			return fmt.Errorf("vault: rotate key: the commit would not be durable (synchronous=%d); nothing was changed", sync)
		}
		return v.reencryptRows(ctx, tx, old, nk)
	})
}

// writeDurable runs fn in a durable write transaction of the Guild database;
// tests wrap it to observe when the commit happens.
func (v *Vault) writeDurable(ctx context.Context, fn func(*sql.Tx) error) error {
	if v.durableHook != nil {
		return v.durableHook(ctx, fn)
	}
	return v.cfg.DB.WriteDurable(ctx, fn)
}

func (v *Vault) reencryptRows(ctx context.Context, tx *sql.Tx, old *keyring, nk *dataKey) error {
	type row struct {
		name, typ string
		ct, nonce []byte
		version   uint32
	}
	rs, err := tx.QueryContext(ctx, `SELECT name, type, ciphertext, nonce, key_version FROM secrets ORDER BY name`)
	if err != nil {
		return fmt.Errorf("vault: rotate key: %w", err)
	}
	var rows []row
	for rs.Next() {
		var r row
		if err := rs.Scan(&r.name, &r.typ, &r.ct, &r.nonce, &r.version); err != nil {
			rs.Close()
			return fmt.Errorf("vault: rotate key: %w", err)
		}
		rows = append(rows, r)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return fmt.Errorf("vault: rotate key: %w", err)
	}

	for i, r := range rows {
		k := old.get(r.version)
		if k == nil {
			return fmt.Errorf("vault: rotate key: secret %q: key version %d is not available: %w", r.name, r.version, ErrVerification)
		}
		plain, err := k.open(r.name, Type(r.typ), r.ct, r.nonce)
		if err != nil {
			return fmt.Errorf("vault: rotate key: secret %q: %w; nothing was changed", r.name, ErrVerification)
		}
		ct, nonce := nk.seal(r.name, Type(r.typ), plain)
		fp := nk.fingerprint(plain)
		zero(plain)
		if _, err := tx.ExecContext(ctx,
			`UPDATE secrets SET ciphertext = ?, nonce = ?, key_version = ?, fingerprint = ? WHERE name = ?`,
			ct, nonce, nk.version, fp, r.name); err != nil {
			return fmt.Errorf("vault: rotate key: %w", err)
		}
		if i == 0 {
			if err := v.step(stepDBPartial); err != nil {
				return err
			}
		}
	}
	return nil
}

// settle checks ring against the stored secrets and finishes an interrupted
// rotation: every key version a row needs that is present must open at
// least one of its rows, and at least one must be present (else the key is
// wrong; rows claiming an unknown version fail alone); keys no row needs are
// dropped, keeping the newest when the table is empty; and the envelope is
// rewritten when it does not match the keys kept or the recovery key.
func (v *Vault) settle(ctx context.Context, ring *keyring) error {
	keep, err := keysInUse(ctx, v.cfg.DB, ring)
	if err != nil {
		return err
	}
	if !slices.Equal(keep, ring.versions()) {
		kept := ring.subset(keep)
		if err := saveKeys(ctx, v.cfg, kept); err != nil {
			return fmt.Errorf("vault: finish key rotation: %w", err)
		}
		for _, k := range ring.keys {
			if !slices.Contains(keep, k.version) {
				k.destroy()
			}
		}
		ring.keys = kept.keys
	}
	if !envelopeCurrent(v.cfg, ring.versions()) {
		if err := writeEnvelope(v.cfg, ring); err != nil {
			return err
		}
	}
	return nil
}

// keysInUse checks ring against the secrets stored in db and returns the key
// versions to keep: of the versions the rows use, those the ring has are kept,
// at least one of them must open a row of its own, or the key is wrong
// (errWrongKey); rows claiming an unknown version fail alone. A table with no
// rows opens with any key. It only reads, so db may be a replica.
func keysInUse(ctx context.Context, db *store.DB, ring *keyring) ([]uint32, error) {
	inUse, err := versionsInUse(ctx, db)
	if err != nil {
		return nil, err
	}
	// keep never drops a key some row claims, even one that failed the probe.
	var keep []uint32
	opened := false
	for _, ver := range inUse {
		k := ring.get(ver)
		if k == nil {
			continue // rows that claim an unknown version: they fail alone
		}
		keep = append(keep, ver)
		ok, err := probe(ctx, db, k)
		if err != nil {
			return nil, err
		}
		opened = opened || ok
	}
	if len(inUse) > 0 && !opened {
		return nil, fmt.Errorf("%w (versions in use: %v)", errWrongKey, inUse)
	}
	if len(keep) == 0 {
		keep = []uint32{ring.current().version}
	}
	return keep, nil
}

// Check says whether the key that keys holds would open the vault of the
// secrets in db, without opening it: the key loads, is a key ring, and opens a
// row of some version the rows use (Open's own test of the key), and, of the
// secrets in names, each one exists and opens, which a vault with no rows, or
// with that row altered, would not (the key of the CA, for a Tower that is going
// to sign with it). It writes nothing, rotates nothing, records no use and
// raises no alert, and db may be a replica: it is how a Tower, before it asks
// to take the command, learns that its copy of the vault would not serve,
// instead of learning it after it won the epoch. A key that does not open is an
// error that wraps ErrClosed, like the one of a vault that does not open; a
// secret that does not is ErrNotFound or ErrVerification.
func Check(ctx context.Context, keys KeyStore, db *store.DB, names ...string) error {
	data, err := keys.Load(ctx)
	if err != nil {
		if errors.Is(err, ErrNoKey) {
			err = errors.New("vault: the key is missing")
		}
		return &ClosedError{Reason: err}
	}
	ring, err := parseKeyring(data)
	zero(data)
	if err != nil {
		return &ClosedError{Reason: err}
	}
	defer ring.destroy()
	if _, err := keysInUse(ctx, db, ring); err != nil {
		return &ClosedError{Reason: err}
	}
	for _, name := range names {
		plain, _, err := decryptRow(ctx, db, ring, name)
		if err != nil {
			return err
		}
		zero(plain)
	}
	return nil
}

func versionsInUse(ctx context.Context, db *store.DB) ([]uint32, error) {
	rows, err := db.Reader().QueryContext(ctx, `SELECT DISTINCT key_version FROM secrets ORDER BY key_version`)
	if err != nil {
		return nil, fmt.Errorf("vault: read key versions: %w", err)
	}
	defer rows.Close()
	var out []uint32
	for rows.Next() {
		var ver uint32
		if err := rows.Scan(&ver); err != nil {
			return nil, fmt.Errorf("vault: read key versions: %w", err)
		}
		out = append(out, ver)
	}
	return out, rows.Err()
}

// errWrongKey is the reason of a vault whose key opens none of its secrets.
var errWrongKey = errors.New("vault: the key does not open the stored secrets (wrong key)")

// probe reports whether k opens at least one row of its version: a single
// altered row fails only its own reads, a wrong key fails them all.
func probe(ctx context.Context, db *store.DB, k *dataKey) (bool, error) {
	rows, err := db.Reader().QueryContext(ctx,
		`SELECT name, type, ciphertext, nonce FROM secrets WHERE key_version = ?`, k.version)
	if err != nil {
		return false, fmt.Errorf("vault: check key: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			name, typ string
			ct, nonce []byte
		)
		if err := rows.Scan(&name, &typ, &ct, &nonce); err != nil {
			return false, fmt.Errorf("vault: check key: %w", err)
		}
		if plain, err := k.open(name, Type(typ), ct, nonce); err == nil {
			zero(plain)
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("vault: check key: %w", err)
	}
	return false, nil
}
