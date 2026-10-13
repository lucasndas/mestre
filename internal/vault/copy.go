package vault

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
)

// The copy of the vault that every machine not excluded keeps
// (docs/guilda/replicacao.md#cofre-em-toda-máquina): the data key, protected
// by the key store of the machine as the Citadel protects its own, and the
// recovery envelope; the secrets themselves come with the database replica,
// already encrypted. Only the Citadel opens the vault (Open and Create refuse a
// replica database); a Tower keeps the copy closed.

// ErrReplica is returned when the vault is asked to open on a database that
// is a Tower's replica: only the Citadel opens the vault (CA-REPL-05).
var ErrReplica = errors.New("vault: the database is a replica; only the Citadel opens the vault")

// Deleter is a KeyStore that can forget the key (vault.drop).
type Deleter interface {
	Delete(ctx context.Context) error
}

// Copy is what travels in vault.replica: the key ring of the data key (every
// version alive) and the recovery envelope. The ring is a secret.
type Copy struct {
	Keyring  []byte
	Envelope []byte
}

// Copier moves the key and the envelope of the vault of this machine: out,
// from the Citadel, and in or away, on a Tower.
type Copier struct {
	Keys         KeyStore
	EnvelopePath string
}

// Export reads the key and the envelope. The caller wipes Keyring.
func (c Copier) Export(ctx context.Context) (Copy, error) {
	ring, err := c.Keys.Load(ctx)
	if err != nil {
		return Copy{}, fmt.Errorf("vault: copy: %w", err)
	}
	env, err := os.ReadFile(c.EnvelopePath)
	if err != nil {
		zero(ring)
		return Copy{}, fmt.Errorf("vault: copy: %w", err)
	}
	return Copy{Keyring: ring, Envelope: env}, nil
}

// Import checks the copy and keeps it: the ring must parse, the envelope must
// be one and hold the same versions of the key as the ring. The envelope is
// written before the key, so a machine never has a key without its envelope.
func (c Copier) Import(ctx context.Context, cp Copy) error {
	ring, err := parseKeyring(cp.Keyring)
	if err != nil {
		return fmt.Errorf("vault: copy: %w", err)
	}
	defer ring.destroy()
	info, err := parseEnvelopeHeader(cp.Envelope)
	if err != nil {
		return fmt.Errorf("vault: copy: %w", err)
	}
	if !slices.Equal(info.versions, ring.versions()) {
		return fmt.Errorf("vault: copy: the envelope holds key versions %v, the key ring %v", info.versions, ring.versions())
	}
	if err := writeFile(c.EnvelopePath, cp.Envelope, true); err != nil {
		return fmt.Errorf("vault: copy: write the envelope: %w", err)
	}
	if err := c.Keys.Save(ctx, cp.Keyring); err != nil {
		return fmt.Errorf("vault: copy: %w", err)
	}
	return nil
}

// Drop erases the key and the envelope (vault.drop); absent is not an error.
func (c Copier) Drop(ctx context.Context) error {
	d, ok := c.Keys.(Deleter)
	if !ok {
		return fmt.Errorf("vault: the key store %T cannot forget the key", c.Keys)
	}
	if err := d.Delete(ctx); err != nil {
		return fmt.Errorf("vault: drop: %w", err)
	}
	if err := os.Remove(c.EnvelopePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("vault: drop: %w", err)
	}
	return nil
}

// Holds says whether this machine has the key and the envelope.
func (c Copier) Holds(ctx context.Context) bool {
	data, err := c.Keys.Load(ctx)
	if err != nil {
		return false
	}
	zero(data)
	_, err = os.Stat(c.EnvelopePath)
	return err == nil
}

// Delete removes the key file (the key store forgets the key).
func (s *FileKeyStore) Delete(context.Context) error {
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("vault: delete key file: %w", err)
	}
	return nil
}
