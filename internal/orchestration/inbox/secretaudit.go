package inbox

import (
	"context"
	"encoding/json"
	"time"

	"github.com/lucasndas/mestre/internal/store"
)

// AuditSecretBlocked is the action of the audit trail of the attempt of a user
// to send a secret through a channel without end-to-end encryption
// (docs/plataforma/cofre.md#credenciais-de-site). The audit holds who, where and
// when, never the text.
const AuditSecretBlocked = "secret.attempt_blocked"

// SecretBlockedEvent is the readable name of the event, the one the document
// names: "tentativa de envio de segredo bloqueada".
const SecretBlockedEvent = "tentativa de envio de segredo bloqueada"

// AuditSecretAttempt writes the attempt in audit_log: the user, the channel and
// the time, and how it was caught (the way the engine saw it); nothing of the
// message.
func AuditSecretAttempt(ctx context.Context, db *store.DB, id string, at time.Time, machine, user, channel, how string) error {
	raw, err := json.Marshal(map[string]string{"event": SecretBlockedEvent, "user": user, "channel": channel, "how": how})
	if err != nil {
		return err
	}
	_, err = db.Writer().ExecContext(ctx, `INSERT INTO audit_log (id, at, machine_id, action, detail) VALUES (?, ?, ?, ?, ?) ON CONFLICT(id) DO NOTHING`,
		id, at.UnixMilli(), machine, AuditSecretBlocked, string(raw))
	return err
}
