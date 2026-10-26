//go:build full

package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// Part of CA-REPL-12
// A Tower the Owner removes while it runs erases what it kept from the time it
// was the Citadel, with its replica and the key of the vault: the engine puts
// the replication together with the folders of its machine, and the Wipe
// covers all of them (docs/guilda/replicacao.md#cofre-em-toda-máquina).
func TestATowerTheOwnerRemovesErasesWhatItKeptFromTheTimeItWasTheCitadel(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	r.waitFor("b to connect", func() bool { return b.connected(1) })
	r.waitFor("b to hold the key of the vault", func() bool {
		_, err := os.Stat(b.paths.vaultKey())
		return err == nil
	})

	kept := []string{
		filepath.Join(b.paths.Banco, "descartado-3.db"), filepath.Join(b.paths.Banco, "descartado-3.db-wal"), filepath.Join(b.paths.Banco, "descartado-3.db-shm"),
		filepath.Join(b.paths.Banco, "descartado-3.json"), filepath.Join(b.paths.Banco, "descartado-3-acoes.json"),
		filepath.Join(b.paths.Banco, "descartado-3.db.1760000000000"),
		filepath.Join(b.paths.snapshots(), "snapshot-01J9Z0.db"), filepath.Join(b.paths.transfers(), "01J9Z1.part"),
	}
	for _, p := range kept {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("a copy of the main database"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	res, err := a.e.citadelNow().Remove(t.Context(), b.id, "dono")
	if err != nil {
		t.Fatal(err)
	}
	if !res.Notified {
		t.Fatal("the Tower did not take guild.leave")
	}
	for _, p := range append(kept, b.paths.guildDB(), b.paths.vaultKey(), b.paths.envelope()) {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s is still there after the machine left", p)
		}
	}
	if _, err := b.e.member.Identity(t.Context()); err == nil {
		t.Error("the machine that left still has its identity")
	} else if err != guild.ErrNotMember {
		t.Errorf("identity after leaving: %v", err)
	}
	r.waitFor("b to stop its engine", func() bool {
		select {
		case <-b.ended:
			return true
		default:
			return false
		}
	})
}
