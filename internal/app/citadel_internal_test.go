package app

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/vault"
)

// CA-COFRE-22
// What needs the CA, which the Citadel opens when it first acts, is answered
// to a Tower with a retryable error: that the Citadel does not act yet, and,
// once it tried and its vault is closed, that it cannot act, which no wait
// will change until the Owner fixes the key.
func TestAHandlerThatNeedsTheCASaysWhyTheCitadelCannotAct(t *testing.T) {
	t.Parallel()
	e := &Engine{joins: &joinMux{h: map[string]http.Handler{}}}
	run := &citadelRun{}
	handle := run.needsCitadel(func(*guild.Citadel) protocol.HandlerFunc {
		t.Error("the handler ran with no Citadel")
		return nil
	})
	ask := func() *protocol.Error {
		t.Helper()
		_, err := handle(t.Context(), &protocol.Message{Peer: protocol.Peer{ID: "tower"}})
		var pe *protocol.Error
		if !errors.As(err, &pe) || pe.Code != protocol.CodeUnavailable || !pe.Retryable {
			t.Fatalf("answer = %v; want a retryable unavailable", err)
		}
		return pe
	}

	if pe := ask(); !strings.Contains(pe.Message, "does not act yet") || !run.refused["tower"] {
		t.Errorf("before it tries: %q, and the Tower was remembered: %v", pe.Message, run.refused["tower"])
	}

	e.cannotAct(run, fmt.Errorf("guild: open the CA of the Guild: %w", vault.ErrClosed))
	run.refused = nil
	if pe := ask(); !strings.Contains(pe.Message, "cannot act") || !strings.Contains(pe.Message, "vault is closed") || len(run.refused) != 0 {
		t.Errorf("with the vault closed: %q, and the Tower was remembered for a resend that never comes: %v", pe.Message, run.refused)
	}
}
