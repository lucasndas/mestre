package app

import (
	"context"
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// The reason of a pause is text a user wrote: a passphrase is refused before anything is kept,
// and a secret of the vault never reaches the pause (the host here has no orchestration, so a
// text that passes the filter ends in "not found", which proves it went on).
func TestTheReasonOfAPauseGoesThroughTheFilterOfSecrets(t *testing.T) {
	t.Parallel()
	m := logging.NewMasker()
	m.Register("tok-segredo-123456")
	h := screenedViewHost{viewHost: viewHost{get: func() *Orchestration { return nil }}, mask: m.Mask}
	by := users.User{ID: "u1", Name: "Ana"}

	for _, held := range []string{"a senha é abc123", "minha senha do banco", "   \U0000202e  "} {
		_, err := h.PauseTable(context.Background(), by, "loja", "42-login", held)
		var ref *panel.Refusal
		if !errors.As(err, &ref) || ref.Msg.Code != "table.secret" {
			t.Errorf("%q: err = %v, want a refusal for a secret", held, err)
		}
	}
	_, err := h.PauseTable(context.Background(), by, "loja", "42-login", "vazou tok-segredo-123456 no log")
	if !errors.Is(err, panel.ErrWorkspaceNotFound) {
		t.Errorf("a known secret is replaced and the text goes on, err = %v", err)
	}
}
