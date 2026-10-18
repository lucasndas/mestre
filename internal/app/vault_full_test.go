//go:build full

package app

import (
	"bytes"
	"testing"

	"github.com/lucasndas/mestre/internal/vault"
	"github.com/lucasndas/mestre/pkg/clock"
)

// CA-COFRE-22
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

	wrongKey(t, a.paths, a.cfg)
	r.start(a, clock.Real{})
	r.waitFor("a to be the Citadel with its vault closed", func() bool {
		st := a.e.Status()
		return st.Role == RoleCitadel && st.Acting
	})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })
	// the key travels when the Tower connects, before the records: once b applied
	// one that came after, it has had every chance to take the key
	lsn := a.mark("after-the-wrong-key")
	r.waitFor("b to apply a record of the Citadel", func() bool { return b.e.Status().LSN >= lsn })
	if !bytes.Equal(keyOf(b), want) {
		t.Error("the Citadel with the wrong key replaced the key of its Tower")
	}
}
