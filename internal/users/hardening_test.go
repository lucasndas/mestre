package users_test

import (
	"errors"
	"testing"

	"github.com/lucasndas/mestre/internal/users"
)

// The level that counts is the one stored now, not the one of the struct the
// caller holds: the Owner that passed the post has no more power with the old
// struct, and a user suspended meanwhile has none either.
func TestTheLevelThatCountsIsTheStoredOneNotTheOneOfTheStructTheCallerHolds(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.pin(w.owner, "111111")
	ana := w.add("Ana", users.Administrator, "loja")
	w.pin(ana, "222222")
	if err := w.svc.StartTransfer(w.ctx, w.owner, ana.ID, w.confirm(w.owner, "111111", users.ActionStartTransfer)); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.AcceptTransfer(w.ctx, ana, w.confirm(ana, "222222", users.ActionAcceptTransfer)); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Create(w.ctx, w.owner, users.NewUser{Name: "X", Level: users.Observer}); !errors.Is(err, users.ErrLevel) {
		t.Errorf("the old Owner registers a user with his old struct = %v", err)
	}
	if err := w.svc.Suspend(w.ctx, w.owner, ana.ID); !errors.Is(err, users.ErrLevel) {
		t.Errorf("the old Owner suspends the new one = %v", err)
	}
	// a user suspended meanwhile
	newOwner, _ := w.svc.Get(w.ctx, ana.ID)
	bia, err := w.svc.Create(w.ctx, newOwner, users.NewUser{Name: "Bia", Level: users.Observer})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.svc.Suspend(w.ctx, newOwner, bia.ID); err != nil {
		t.Fatal(err)
	}
	old, _ := w.svc.Get(w.ctx, w.owner.ID) // now an Administrator
	if err := w.svc.Suspend(w.ctx, newOwner, old.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.NewCode(w.ctx, old, bia.ID); !errors.Is(err, users.ErrLevel) {
		t.Errorf("a suspended user makes a code = %v", err)
	}
}

// A transfer does not survive the suspension or the removal of who it names, so
// the Guild is never left without an Owner.
func TestSuspendingTheChosenUserCancelsTheTransferAndTheOwnerStays(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.pin(w.owner, "111111")
	ana := w.add("Ana", users.Administrator)
	w.pin(ana, "222222")
	if err := w.svc.StartTransfer(w.ctx, w.owner, ana.ID, w.confirm(w.owner, "111111", users.ActionStartTransfer)); err != nil {
		t.Fatal(err)
	}
	accept := w.confirm(ana, "222222", users.ActionAcceptTransfer)
	if err := w.svc.Suspend(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.AcceptTransfer(w.ctx, ana, accept); err == nil {
		t.Fatal("a suspended user took the post")
	}
	if got, _ := w.svc.Get(w.ctx, w.owner.ID); got.Level != users.Owner {
		t.Errorf("the Owner is %s", got.Level)
	}
}

// A request answered right is spent once, whatever comes after: a wrong PIN
// does not open it again, and a suspension, a reset or a new PIN cancels it.
func TestAnAnsweredRequestIsSpentOnceAndWhatChangesTheUserCancelsIt(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.pin(w.owner, "111111")
	ana := w.add("Ana", users.Administrator)
	w.pin(ana, "222222")

	done := w.confirm(ana, "222222", "cofre")
	if err := w.svc.CheckPIN(w.ctx, done, "000000"); !errors.Is(err, users.ErrPINRequest) {
		t.Errorf("a wrong PIN in a request already answered = %v", err)
	}
	if err := w.svc.Consume(w.ctx, done, ana.ID, "cofre"); err != nil {
		t.Errorf("the request was reopened or lost: %v", err)
	}

	for _, name := range []string{"suspension", "reset", "new PIN"} {
		w2 := newWorld(t)
		a := w2.add("Ana", users.Administrator)
		w2.pin(a, "222222")
		req := w2.confirm(a, "222222", "cofre")
		switch name {
		case "suspension":
			_ = w2.svc.Suspend(w2.ctx, w2.owner, a.ID)
		case "reset":
			_ = w2.svc.ResetPIN(w2.ctx, w2.owner, a.ID)
		case "new PIN":
			w2.pin(a, "333333")
		}
		if err := w2.svc.Consume(w2.ctx, req, a.ID, "cofre"); !errors.Is(err, users.ErrPINRequest) {
			t.Errorf("after the %s the request is still good: %v", name, err)
		}
	}
}

// A hash with costs out of the limits (a damaged database) is not verified: it
// fails closed, without a panic or a huge allocation.
func TestAHashWithCostsOutOfTheLimitsIsRefusedWithoutVerifying(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	for _, h := range []string{
		"$argon2id$v=19$m=8,t=0,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=4000000,t=1,p=1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"$argon2id$v=19$m=8,t=1,p=0$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
	} {
		if _, err := w.db.Writer().ExecContext(w.ctx, `UPDATE users SET pin_hash = ? WHERE id = ?`, h, w.owner.ID); err != nil {
			t.Fatal(err)
		}
		r, err := w.svc.StartPIN(w.ctx, w.owner.ID, "cofre")
		if err != nil {
			t.Fatal(err)
		}
		if err := w.svc.CheckPIN(w.ctx, r.ID, "123456"); !errors.Is(err, users.ErrBadPIN) {
			t.Errorf("hash %q: %v, want a wrong PIN", h, err)
		}
	}
}

func TestEditingARemovedUserOrReactivatingOneAreRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ana := w.add("Ana", users.Observer)
	if err := w.svc.Remove(w.ctx, w.owner, ana.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.svc.Edit(w.ctx, w.owner, ana.ID, users.Edit{Name: ptr("Outra")}); !errors.Is(err, users.ErrNoUser) {
		t.Errorf("editing a removed user = %v", err)
	}
	if err := w.svc.Reactivate(w.ctx, w.owner, ana.ID); !errors.Is(err, users.ErrNoUser) {
		t.Errorf("reactivating a removed user = %v", err)
	}
	if err := w.svc.ResetPIN(w.ctx, w.owner, "nobody"); !errors.Is(err, users.ErrNoUser) {
		t.Errorf("resetting the PIN of nobody = %v", err)
	}
	if err := w.svc.ClearTelegram(w.ctx, w.owner, "nobody"); !errors.Is(err, users.ErrNoUser) {
		t.Errorf("clearing the Telegram of nobody = %v", err)
	}
}
