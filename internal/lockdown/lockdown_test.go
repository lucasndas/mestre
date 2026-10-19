package lockdown_test

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/lockdown"
	"github.com/lucasndas/mestre/internal/store"
	"github.com/lucasndas/mestre/internal/testkit/guildtpl"
	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/pkg/clock"
)

type world struct {
	t     *testing.T
	ctx   context.Context
	clk   *clock.Fake
	db    *store.DB
	users *users.Service
	owner users.User
	svc   *lockdown.Service
	mu    sync.Mutex
	told  map[string][]string
	ended int
	flips []bool
}

type operator struct{ w *world }

func (o operator) EndNow(context.Context, users.User) error {
	o.w.mu.Lock()
	o.w.ended++
	o.w.mu.Unlock()
	return nil
}

func newWorld(t *testing.T) *world {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 14, 30, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	w := &world{t: t, ctx: ctx, clk: clk, db: db, told: map[string][]string{}}
	if w.users, err = users.New(users.Options{DB: db, Clock: clk, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}}); err != nil {
		t.Fatal(err)
	}
	if w.owner, err = w.users.CreateOwner(ctx, "Lucas"); err != nil {
		t.Fatal(err)
	}
	w.svc = w.newService()
	return w
}

func (w *world) newService() *lockdown.Service {
	svc, err := lockdown.New(lockdown.Options{DB: w.db, Clock: w.clk, Users: w.users, Operator: operator{w},
		Tell: func(_ context.Context, id, text string) {
			w.mu.Lock()
			w.told[id] = append(w.told[id], text)
			w.mu.Unlock()
		}})
	if err != nil {
		w.t.Fatal(err)
	}
	svc.OnChange(func(locked bool) { w.mu.Lock(); w.flips = append(w.flips, locked); w.mu.Unlock() })
	return svc
}

func (w *world) add(name string, level users.Level) users.User {
	w.t.Helper()
	u, err := w.users.Create(w.ctx, w.owner, users.NewUser{Name: name, Level: level})
	if err != nil {
		w.t.Fatal(err)
	}
	return u
}

// pin makes a request of PIN answered right, for the unlock.
func (w *world) pin(u users.User) string {
	w.t.Helper()
	if err := w.users.SetPIN(w.ctx, u.ID, "123456"); err != nil {
		w.t.Fatal(err)
	}
	req, err := w.users.StartPIN(w.ctx, u.ID, lockdown.UnlockAction)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.users.CheckPIN(w.ctx, req.ID, "123456"); err != nil {
		w.t.Fatal(err)
	}
	return req.ID
}

// Part of CA-CANAL-04: the lock holds, every user is told who locked it and when,
// and the session of the Operator ends. The hook, the decrees, the inbox and the
// GitHub are the pieces that read it.
func TestAnAdministratorLocksAndEveryoneIsTold(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	adm := w.add("Ana", users.Administrator)
	obs := w.add("Otto", users.Observer)
	if w.svc.Locked(w.ctx) {
		t.Fatal("locked from the start")
	}
	if err := w.svc.Lock(w.ctx, adm); err != nil {
		t.Fatal(err)
	}
	if !w.svc.Locked(w.ctx) {
		t.Fatal("not locked")
	}
	for _, u := range []users.User{w.owner, adm, obs} {
		if got := w.told[u.ID]; len(got) != 1 || !strings.Contains(got[0], "Ana travou o Mestre às 14:30") {
			t.Errorf("%s was told %q", u.Name, got)
		}
	}
	if w.ended != 1 {
		t.Errorf("the session of the Operator was ended %d times", w.ended)
	}
	// locking again says nothing again
	if err := w.svc.Lock(w.ctx, adm); err != nil {
		t.Fatal(err)
	}
	if len(w.told[obs.ID]) != 1 || w.ended != 1 || !slices.Equal(w.flips, []bool{true}) {
		t.Errorf("a second lock changed things: told %v, ended %d, flips %v", w.told[obs.ID], w.ended, w.flips)
	}
}

func TestALevelBelowDoesNotLockAndTheAttemptIsRecorded(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ap := w.add("Bia", users.Approver)
	if err := w.svc.Lock(w.ctx, ap); !errors.Is(err, lockdown.ErrLevel) {
		t.Fatalf("lock by an Approver: %v", err)
	}
	if w.svc.Locked(w.ctx) || w.ended != 0 || len(w.told) != 0 {
		t.Error("a refused lock changed something")
	}
	var n int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM channel_lock_audit WHERE user_id = ? AND action = 'lock' AND outcome = 'refused'`, ap.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit rows %d, %v", n, err)
	}
	// a suspended Administrator
	adm := w.add("Ana", users.Administrator)
	if err := w.users.Suspend(w.ctx, w.owner, adm.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Lock(w.ctx, adm); !errors.Is(err, lockdown.ErrLevel) {
		t.Errorf("lock by a suspended Administrator: %v", err)
	}
	// the level is the one the registry has, not the one the caller claims
	forged := ap
	forged.Level = users.Owner
	if err := w.svc.Lock(w.ctx, forged); !errors.Is(err, lockdown.ErrLevel) {
		t.Errorf("lock with a forged level: %v", err)
	}
}

// Part of CA-CANAL-05
func TestOnlyTheOwnerUnlocksAndWithThePIN(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	adm := w.add("Ana", users.Administrator)
	if err := w.svc.Lock(w.ctx, adm); err != nil {
		t.Fatal(err)
	}
	// an Administrator, even with a PIN, does not unlock
	if err := w.users.SetPIN(w.ctx, adm.ID, "654321"); err != nil {
		t.Fatal(err)
	}
	req, _ := w.users.StartPIN(w.ctx, adm.ID, lockdown.UnlockAction)
	_ = w.users.CheckPIN(w.ctx, req.ID, "654321")
	if err := w.svc.Unlock(w.ctx, adm, req.ID); !errors.Is(err, lockdown.ErrLevel) || !w.svc.Locked(w.ctx) {
		t.Fatalf("unlock by an Administrator: %v", err)
	}
	// the Owner with no answer to the PIN
	if err := w.users.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	pending, _ := w.users.StartPIN(w.ctx, w.owner.ID, lockdown.UnlockAction)
	if err := w.svc.Unlock(w.ctx, w.owner, pending.ID); !errors.Is(err, lockdown.ErrPIN) || !w.svc.Locked(w.ctx) {
		t.Fatalf("unlock with a PIN not answered: %v", err)
	}
	// a PIN request for another action does not unlock
	other, _ := w.users.StartPIN(w.ctx, w.owner.ID, "approval:7")
	_ = w.users.CheckPIN(w.ctx, other.ID, "123456")
	if err := w.svc.Unlock(w.ctx, w.owner, other.ID); !errors.Is(err, lockdown.ErrPIN) {
		t.Fatalf("unlock with the PIN of another action: %v", err)
	}
	ok := w.pin(w.owner)
	if err := w.svc.Unlock(w.ctx, w.owner, ok); err != nil {
		t.Fatal(err)
	}
	if w.svc.Locked(w.ctx) || !slices.Equal(w.flips, []bool{true, false}) {
		t.Errorf("locked %v, flips %v", w.svc.Locked(w.ctx), w.flips)
	}
	if got := w.told[adm.ID]; len(got) != 2 || !strings.Contains(got[1], "Lucas destravou") {
		t.Errorf("told %q", got)
	}
	// unlocked already: the PIN of the Owner is not spent for nothing
	spare := w.pin(w.owner)
	if err := w.svc.Unlock(w.ctx, w.owner, spare); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Lock(w.ctx, adm); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Unlock(w.ctx, w.owner, spare); err != nil || w.svc.Locked(w.ctx) {
		t.Fatalf("the PIN left from the unlock that had nothing to do: %v", err)
	}
	// the PIN is spent once
	if err := w.svc.Lock(w.ctx, adm); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Unlock(w.ctx, w.owner, ok); !errors.Is(err, lockdown.ErrPIN) || !w.svc.Locked(w.ctx) {
		t.Errorf("unlock with a spent PIN: %v", err)
	}
}

func TestTheLockSurvivesARestartAndAnErrorCountsAsLocked(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	adm := w.add("Ana", users.Administrator)
	if err := w.svc.Lock(w.ctx, adm); err != nil {
		t.Fatal(err)
	}
	again := w.newService() // the Citadel restarts
	st, err := again.State(w.ctx)
	if err != nil || !st.Locked || st.By != adm.ID {
		t.Fatalf("state after the restart = %+v, %v", st, err)
	}
	// a database that can not be read is locked: the safe side
	other := newWorld(t)
	_ = other.db.Close()
	if !other.svc.Locked(other.ctx) {
		t.Error("an error reading the lock counted as unlocked")
	}
}
