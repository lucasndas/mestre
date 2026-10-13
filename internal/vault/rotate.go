package vault

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

// Rotation steps, in order. A crash after any of them leaves every secret
// decryptable: the key store and the envelope hold both keys from
// "keyfile-staged" until the database commit has moved every row to the new
// key, and only then drop the old one (docs/plataforma/cofre.md#chave-do-cofre).
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

// Rotate replaces the data key: it generates a new version, re-encrypts
// every secret with it in one transaction, envelopes it to the recovery
// public key and drops the old key. Sending the new vault to the Towers is
// the replication's job (M2).
func (v *Vault) Rotate(ctx context.Context) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.errLocked(); err != nil {
		return fmt.Errorf("rotate key: %w", err)
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

	// 2. Every row to the new key, all or nothing.
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
	type row struct {
		name, typ string
		ct, nonce []byte
		version   uint32
	}
	tx, err := v.cfg.DB.Writer().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("vault: rotate key: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op after Commit

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
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("vault: rotate key: %w", err)
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
	inUse, err := v.versionsInUse(ctx)
	if err != nil {
		return err
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
		ok, err := v.probe(ctx, k)
		if err != nil {
			return err
		}
		opened = opened || ok
	}
	if len(inUse) > 0 && !opened {
		return fmt.Errorf("%w (versions in use: %v)", errWrongKey, inUse)
	}
	if len(keep) == 0 {
		keep = []uint32{ring.current().version}
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

func (v *Vault) versionsInUse(ctx context.Context) ([]uint32, error) {
	rows, err := v.cfg.DB.Reader().QueryContext(ctx, `SELECT DISTINCT key_version FROM secrets ORDER BY key_version`)
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
func (v *Vault) probe(ctx context.Context, k *dataKey) (bool, error) {
	rows, err := v.cfg.DB.Reader().QueryContext(ctx,
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
