//go:build full

package app

import (
	"testing"

	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/pkg/clock"
)

// A Citadel that starts suspended (the lock has not answered) writes nothing
// to the Guild database: no use of the key of the CA, no sheet of its own, no
// pruning. Stopped and started again still suspended, its last lsn is the
// same (CA-CIDADELA-02).
func TestASuspendedCitadelWritesNothingAcrossARestart(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	r.found(a, clock.Real{})
	last := func() uint64 {
		var lsn uint64
		if err := a.e.withGuildDB(func(db *store.DB) (err error) {
			lsn, err = db.LastLSN(t.Context())
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return lsn
	}

	r.lock.Hold()
	defer r.lock.Release()
	r.start(a, clock.Real{})
	r.waitFor("a to start suspended", func() bool {
		st := a.e.Status()
		return st.Role == RoleCitadel && st.Fence == guild.FenceSuspended
	})
	before := last()
	a.stop()

	r.start(a, clock.Real{})
	r.waitFor("a to start suspended again", func() bool {
		st := a.e.Status()
		return st.Role == RoleCitadel && st.Fence == guild.FenceSuspended
	})
	if after := last(); after != before {
		t.Errorf("the last lsn went from %d to %d across a suspended restart", before, after)
	}
}
