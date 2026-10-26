package vault

import (
	"bytes"
	"errors"
	"os"
	"testing"
)

// CA-COFRE-20 (the vault half): after a new recovery key the envelope on disk opens
// with the new key and no longer with the old one, the secrets still open, and the
// Guild keeps the new public key.
func TestRewrapRecoveryEnvelopesTheKeyToTheNewRecoveryKey(t *testing.T) {
	t.Parallel()
	f := newFixture(t, nil)
	v := f.create(t)
	defer v.Close()
	if err := v.Put(f.ctx, "provider", TypeProviderAPIKey, []byte("sk-segredo-longo-1234"), nil); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Writer().ExecContext(f.ctx, `INSERT INTO guild (id, guild_id, ca_cert, epoch, epoch_certificate, timezone, recovery_public_key, created_at) VALUES (1, 'g', x'00', 1, '', 'UTC', ?, 0)`, f.rk.PublicKey()); err != nil {
		t.Fatal(err)
	}
	fresh, err := NewRecoveryKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := v.RewrapRecovery(f.ctx, fresh.PublicKey()); err != nil {
		t.Fatal(err)
	}
	env, err := os.ReadFile(f.cfg.EnvelopePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := RestoreKey(f.ctx, env, f.rk, &memKeys{}); !errors.Is(err, ErrRecoveryKeyMismatch) {
		t.Fatalf("the old recovery key opens the new envelope: %v", err)
	}
	restored := &memKeys{}
	if err := RestoreKey(f.ctx, env, fresh, restored); err != nil {
		t.Fatalf("the new recovery key does not open it: %v", err)
	}
	var kept []byte
	if err := f.db.Reader().QueryRowContext(f.ctx, `SELECT recovery_public_key FROM guild WHERE id = 1`).Scan(&kept); err != nil || !bytes.Equal(kept, fresh.PublicKey()) {
		t.Fatalf("the Guild keeps %x (%v), want the new public key", kept, err)
	}
	cfg := f.cfg
	cfg.Keys, cfg.RecoveryPublicKey = restored, fresh.PublicKey()
	rv, err := Open(f.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer rv.Close()
	if err := rv.Use(f.ctx, "provider", "test", func(b []byte) error {
		if string(b) != "sk-segredo-longo-1234" {
			t.Errorf("secret = %q", b)
		}
		return nil
	}); err != nil {
		t.Fatalf("the secrets do not open after the new recovery key: %v", err)
	}
	if err := v.RewrapRecovery(f.ctx, []byte("short")); err == nil {
		t.Fatal("a public key that is not 32 bytes was accepted")
	}
}
