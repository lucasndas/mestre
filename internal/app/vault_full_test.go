//go:build full

package app

import (
	"bytes"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CA-COFRE-23
// A Citadel whose key is the wrong one, a key of the same version as the
// envelope that opens nothing, goes on and does not send it to its Towers: a
// Tower takes any ring of the versions of its envelope and puts it in place of its
// own, and the Guild would lose the good copies it has. The Tower keeps the key
// it had.
func TestACitadelWhoseKeyIsWrongLeavesTheKeyOfItsTowersAlone(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	keyOf := func(n *inproc) []byte {
		data, err := vault.NewFileKeyStore(n.paths.vaultKey()).Load(t.Context())
		if err != nil {
			return nil
		}
		return data
	}
	// the Citadel with the right key does send it: this is what the test guards
	r.waitFor("b to hold the key of the vault", func() bool { return b.connected(1) && len(keyOf(b)) > 0 && bytes.Equal(keyOf(b), keyOf(a)) })
	want := keyOf(b)
	a.stop()
	b.stop()

	wrongKey(t, a.paths)
	r.start(a, clock.Real{})
	r.waitFor("a to be the Citadel with its vault closed", func() bool {
		st := a.e.Status()
		return st.Role == RoleCitadel && st.Acting
	})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })
	// The key travels when the Tower connects, before the records. A message that
	// wants an ack goes after it on the same queue of the link, and the Tower
	// handles one message at a time: once it is acked, the key had every chance to
	// be taken, and a key that was not sent is not coming.
	lsn := a.mark("after-the-wrong-key")
	r.waitFor("b to apply a record of the Citadel", func() bool { return b.e.Status().LSN >= lsn })
	a.e.mu.Lock()
	run := a.e.cit
	a.e.mu.Unlock()
	cfg, err := guild.ConfigOf(t.Context(), run.db, a.e.creds)
	if err != nil {
		t.Fatal(err)
	}
	if res, err := run.srv.Link(b.id).Call(t.Context(), protocol.Outgoing{Type: protocol.TypeGuildConfig, Payload: cfg}); err != nil || res.Status != protocol.AckOK {
		t.Fatalf("guild.config to b = %+v, %v", res, err)
	}
	if !bytes.Equal(keyOf(b), want) {
		t.Error("the Citadel with the wrong key replaced the key of its Tower")
	}
}
