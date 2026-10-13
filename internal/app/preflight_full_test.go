//go:build full

package app

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/pkg/clock"
)

// A stop between the promotion and the record of the identity leaves the
// machine with the old certificate of epoch while the Guild database holds the
// higher one it signed itself: the engine follows it (verified) before it
// serves anything, and starts as the Citadel of the epoch 2.
func TestPreflightFollowsAHigherCertificateOfEpochOnlyTheGuildDatabaseHolds(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	lsn := a.mark("t1")
	r.waitFor("b to apply t1", func() bool { return b.connected(1) && b.e.Status().LSN >= lsn })

	raw, err := sql.Open("sqlite", b.paths.machineDB())
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var epoch1 []byte
	if err := raw.QueryRowContext(t.Context(), `SELECT epoch_cert FROM machine_identity WHERE id = 1`).Scan(&epoch1); err != nil {
		t.Fatal(err)
	}

	if err := b.e.Promote(t.Context(), "test"); err != nil {
		t.Fatal(err)
	}
	r.waitFor("b to take the command", func() bool {
		st := b.e.Status()
		return st.Role == RoleCitadel && st.Epoch == 2
	})
	b.stop()

	// the record of the identity is back at the epoch 1, as if the stop came before it
	if _, err := raw.ExecContext(t.Context(), `UPDATE machine_identity SET epoch_cert = ? WHERE id = 1`, epoch1); err != nil {
		t.Fatal(err)
	}
	r.start(b, clock.Real{})
	r.waitFor("b to follow the epoch 2 the Guild database holds", func() bool {
		st := b.e.Status()
		return st.Role == RoleCitadel && st.Epoch == 2
	})
	if log := b.log.String(); !strings.Contains(log, "following the higher certificate of epoch the Guild database holds") {
		t.Errorf("the engine did not say it followed the certificate of the database: %s", log)
	}
}
