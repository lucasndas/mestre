//go:build full

package users_test

import (
	"database/sql"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/users"
)

// CA-USR-08
// The transfer of the post is one transaction: while it happens, a reader that
// counts the Owners never sees zero or two, and when it ends the new Owner has
// the post and the old one is an Administrator of every workspace.
func TestTheTransferChangesTheLevelsInOneTransactionAndThereIsAlwaysOneOwner(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.pin(w.owner, "111111")
	ana := w.add("Ana", users.Administrator, "loja")
	w.pin(ana, "222222")
	if err := w.svc.StartTransfer(w.ctx, w.owner, ana.ID, w.confirm(w.owner, "111111", users.ActionStartTransfer)); err != nil {
		t.Fatal(err)
	}
	accept := w.confirm(ana, "222222", users.ActionAcceptTransfer)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	seen := map[int]bool{}
	var mu sync.Mutex
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			var n int
			if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM users WHERE level = 'owner' AND state <> 'removed'`).Scan(&n); err == nil || err == sql.ErrNoRows {
				mu.Lock()
				seen[n] = true
				mu.Unlock()
			}
		}
	})
	err := w.svc.AcceptTransfer(w.ctx, ana, accept)
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	for n := range seen {
		if n != 1 {
			t.Errorf("a reader saw %d Owners", n)
		}
	}
	oldOwner, _ := w.svc.Get(w.ctx, w.owner.ID)
	newOwner, _ := w.svc.Get(w.ctx, ana.ID)
	if newOwner.Level != users.Owner || oldOwner.Level != users.Administrator {
		t.Fatalf("levels after the transfer: new %s, old %s", newOwner.Level, oldOwner.Level)
	}
	if len(oldOwner.Workspaces) != 2 || !oldOwner.Acts("loja") || !oldOwner.Acts("blog") {
		t.Errorf("the old Owner acts in %v, want every workspace", oldOwner.Workspaces)
	}
	if !newOwner.Acts("blog") {
		t.Error("the new Owner does not act in every workspace")
	}
}
