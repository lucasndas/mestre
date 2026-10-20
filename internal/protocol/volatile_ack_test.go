package protocol_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"testing/synctest"

	"github.com/lucasndas/mestre/internal/protocol"
)

// Part of CA-COFRE-21: the credential travels only in the ack of the request, and
// the idempotency store of the Citadel (the database, replicated) keeps that the id
// was answered, never the answer: a volatile type is not written anywhere.
func TestTheAnswerOfAVolatileTypeIsNotKeptByTheIdempotencyStore(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := newWorld(t, opts{})
		const value = "s3nha-que-nao-pode-ficar"
		w.cfgC.Router.HandleFunc(protocol.TypeVaultRequest, func(context.Context, *protocol.Message) (*protocol.Reply, error) {
			return &protocol.Reply{Type: protocol.TypeVaultCredential, Payload: protocol.VaultCredential{Value: value}, InAck: true}, nil
		})
		w.start()
		defer w.stop()
		settle()
		const id = "01J9Z0000000000000000000B1"
		res, err := w.client.Link().Call(t.Context(), protocol.Outgoing{ID: id, Type: protocol.TypeVaultRequest, Transient: true,
			Payload: protocol.VaultRequest{AgentID: "qa1", Secret: "site_loja", Purpose: "login"}})
		if err != nil {
			t.Fatal(err)
		}
		var got protocol.VaultCredential
		if err := json.Unmarshal(res.Result, &got); err != nil || got.Value.Reveal() != value {
			t.Fatalf("the Tower got %s, %v; want the value in the ack", res.Result, err)
		}
		fresh, kept, err := w.partsC.Store.Claim(t.Context(), towerID, id, protocol.TypeVaultRequest, w.clk.Now())
		if err != nil || fresh {
			t.Fatalf("the id was not recorded as answered: fresh %v, %v", fresh, err)
		}
		if bytes.Contains(kept, []byte(value)) {
			t.Fatalf("the idempotency store keeps the value: %s", kept)
		}
	})
}
