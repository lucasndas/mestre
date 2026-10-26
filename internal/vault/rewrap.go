package vault

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// RewrapRecovery makes the recovery envelope for a new recovery key: the data
// key and every version in use are enveloped to the public key given, the
// envelope on disk is replaced and the public key the Guild keeps is changed
// with it. The backups already made keep needing the old recovery key; from now
// on the envelope of a backup opens only with the new one
// (docs/plataforma/cofre.md#backup-e-chave-de-recuperação). If the Guild does not
// take the new public key the old envelope is put back, so the envelope and the
// public key never tell different stories. Sending the envelope to the Towers is
// the replication's job.
func (v *Vault) RewrapRecovery(ctx context.Context, public []byte) error {
	if len(public) != 32 {
		return errors.New("vault: the recovery public key must be a 32-byte X25519 public key")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if err := v.errLocked(); err != nil {
		return fmt.Errorf("rewrap recovery: %w", err)
	}
	old, err := os.ReadFile(v.cfg.EnvelopePath)
	if err != nil {
		return fmt.Errorf("vault: rewrap recovery: read the envelope: %w", err)
	}
	next := v.cfg
	next.RecoveryPublicKey = append([]byte(nil), public...)
	if err := writeEnvelope(next, v.ring); err != nil {
		return err
	}
	if _, err := v.cfg.DB.Writer().ExecContext(ctx, `UPDATE guild SET recovery_public_key = ? WHERE id = 1`, public); err != nil {
		if rerr := writeFile(v.cfg.EnvelopePath, old, !v.cfg.fastEnvelope); rerr != nil {
			err = errors.Join(err, rerr)
		}
		return fmt.Errorf("vault: rewrap recovery: the Guild kept the old public key: %w", err)
	}
	v.cfg.RecoveryPublicKey = next.RecoveryPublicKey
	return nil
}
