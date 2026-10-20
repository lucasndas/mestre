//go:build full

package app

import (
	"errors"
	"os"
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/transport"
	"github.com/lucasndas/mestre/pkg/clock"
)

// Part of CA-GUILDA-12
// The Owner excludes a Tower that holds the vault: the engines do what the
// criterion says with the hook the Citadel fires wired to the replication. The
// Tower erases the key, the envelope and the vault tables of its replica (the
// Citadel audits the receipt), its certificates go on without mestre.local, a
// promotion of it is refused (by itself and by the Citadel), and the exclusion
// undone brings the vault back. What is not here is the PIN: the caller of
// SetExcluded is the panel and the /guilda of the bot, which come in milestone 6
// (CA-ADM-10 holds the PIN of the panel).
func TestExcludingATowerErasesItsVaultAndUndoingItBringsTheVaultBack(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	ctx := t.Context()

	secrets := func() int {
		n := 0
		// a replica that has no copy yet, or whose vault tables are gone, has no secrets
		_ = b.e.withGuildDB(func(db *store.DB) error {
			return db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM secrets`).Scan(&n)
		})
		return n
	}
	keyAndEnvelope := func() (key, envelope bool) {
		data, err := b.e.vaultCopy().Keys.Load(ctx)
		_, envErr := os.Stat(b.paths.envelope())
		return err == nil && len(data) > 0, envErr == nil
	}
	promotable := func() error {
		return b.e.withGuildDB(func(db *store.DB) error { return b.e.promotable(ctx, db) })
	}

	// the Tower has the vault: key, envelope and the tables (the CA of the Guild is in them)
	r.waitFor("b to hold the vault", func() bool {
		k, e := keyAndEnvelope()
		return b.connected(1) && k && e && secrets() > 0 && b.e.Status().LSN >= a.e.Status().LSN
	})
	want := secrets()
	if hasName(b.e.cert.Leaf(), "mestre.local") {
		t.Fatal("the certificate of a Tower carries mestre.local")
	}
	if err := a.e.citadelNow().Promotable(ctx, b.id); err != nil {
		t.Fatalf("a Tower with the vault is not promotable: %v", err)
	}
	r.waitFor("b to be promotable", func() bool { return promotable() == nil })

	// the Owner excludes it
	if err := a.e.citadelNow().SetExcluded(ctx, b.id, true); err != nil {
		t.Fatal(err)
	}
	r.waitFor("b to erase the key, the envelope and the vault tables", func() bool {
		k, e := keyAndEnvelope()
		return !k && !e && secrets() == 0
	})
	const droppedRows = `SELECT id FROM audit_log WHERE action = 'vault.dropped' AND machine_id = ?`
	r.waitFor("the Citadel to audit the receipt of b", func() bool { return len(a.query(droppedRows, b.id)) == 1 })
	if sr := a.query(`SELECT count(*) FROM secrets`); len(sr) != 1 || sr[0] == "0" {
		t.Errorf("the Citadel lost its own vault: %v", sr)
	}

	// the certificates of the excluded machine follow without mestre.local, renewed or not
	oldSerial := transport.Serial(b.e.cert.Leaf())
	if err := b.e.towerNow().renewer.Renew(ctx); err != nil {
		t.Fatalf("the excluded Tower renewing its certificate: %v", err)
	}
	r.waitFor("b to present a renewed certificate", func() bool { return transport.Serial(b.e.cert.Leaf()) != oldSerial })
	for _, n := range []*inproc{b} {
		if hasName(n.e.cert.Leaf(), "mestre.local") || hasName(n.e.machine.Leaf(), "mestre.local") {
			t.Errorf("a certificate of the excluded machine %s carries mestre.local", n.name)
		}
	}
	if got := a.query(`SELECT serial FROM machine_credentials WHERE machine_id = ? AND role = 'cidadela'`, b.id); len(got) != 0 {
		t.Errorf("the registry issued a certificate of the Citadel to an excluded machine: %v", got)
	}

	// a promotion of it is refused, by the Citadel and by the machine
	if err := a.e.citadelNow().Promotable(ctx, b.id); !errors.Is(err, guild.ErrExcludedMachine) {
		t.Errorf("the Citadel on the promotion of an excluded machine: %v", err)
	}
	r.waitFor("b to know it is excluded", func() bool { return errors.Is(promotable(), guild.ErrExcludedMachine) })
	if err := b.e.Promote(ctx, "test"); !errors.Is(err, guild.ErrNotPromotable) || !errors.Is(err, guild.ErrExcludedMachine) {
		t.Errorf("the promotion of the excluded machine: %v", err)
	}
	if st := b.e.Status(); st.Role != RoleTower || st.Failover {
		t.Errorf("the refused promotion left b as %+v", st)
	}

	// the exclusion undone: the vault comes back, with every table, and the promotion is open again
	if err := a.e.citadelNow().SetExcluded(ctx, b.id, false); err != nil {
		t.Fatal(err)
	}
	r.waitFor("b to receive the vault again", func() bool {
		k, e := keyAndEnvelope()
		return k && e && secrets() == want
	})
	r.waitFor("b to be promotable again", func() bool { return promotable() == nil })
	if err := a.e.citadelNow().Promotable(ctx, b.id); err != nil {
		t.Errorf("the Citadel on the promotion after the exclusion was undone: %v", err)
	}
}
