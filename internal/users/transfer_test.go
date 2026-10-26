package users_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/users"
)

// confirm answers a request of PIN for the action, right.
func (w *world) confirm(u users.User, pin, action string) string {
	w.t.Helper()
	r, err := w.svc.StartPIN(w.ctx, u.ID, action)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.svc.CheckPIN(w.ctx, r.ID, pin); err != nil {
		w.t.Fatal(err)
	}
	return r.ID
}

func TestTheTransferNeedsAUserWithAPinAndBothPins(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.pin(w.owner, "111111")
	ana := w.add("Ana", users.Administrator, "loja")
	if err := w.svc.StartTransfer(w.ctx, w.owner, ana.ID, w.confirm(w.owner, "111111", users.ActionStartTransfer)); !errors.Is(err, users.ErrTransferBad) {
		t.Fatalf("to a user without a PIN = %v", err)
	}
	w.pin(ana, "222222")
	// a request of PIN for another action does not start a transfer
	if err := w.svc.StartTransfer(w.ctx, w.owner, ana.ID, w.confirm(w.owner, "111111", "cofre")); !errors.Is(err, users.ErrPINRequest) {
		t.Fatalf("with a PIN for another action = %v", err)
	}
	if err := w.svc.StartTransfer(w.ctx, ana, w.owner.ID, w.confirm(ana, "222222", users.ActionStartTransfer)); !errors.Is(err, users.ErrLevel) {
		t.Fatalf("an Administrator starts = %v", err)
	}
	if err := w.svc.StartTransfer(w.ctx, w.owner, ana.ID, w.confirm(w.owner, "111111", users.ActionStartTransfer)); err != nil {
		t.Fatal(err)
	}
	if len(w.al.told[ana.ID]) != 1 {
		t.Errorf("Ana was told %v", w.al.told[ana.ID])
	}
	// accepting without the right request, or after the 24 hours, does nothing
	if err := w.svc.AcceptTransfer(w.ctx, ana, "none"); !errors.Is(err, users.ErrPINRequest) {
		t.Errorf("accepting with no request = %v", err)
	}
	w.clk.Advance(users.TransferTTL + time.Second)
	if err := w.svc.AcceptTransfer(w.ctx, ana, w.confirm(ana, "222222", users.ActionAcceptTransfer)); !errors.Is(err, users.ErrNoTransfer) {
		t.Errorf("accepting after 24 hours = %v", err)
	}
	if got, _ := w.svc.Get(w.ctx, w.owner.ID); got.Level != users.Owner {
		t.Errorf("the Owner is %s", got.Level)
	}
}

func TestTheOwnerCancelsATransferBeforeTheAcceptance(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	w.pin(w.owner, "111111")
	ana := w.add("Ana", users.Administrator)
	w.pin(ana, "222222")
	if err := w.svc.StartTransfer(w.ctx, w.owner, ana.ID, w.confirm(w.owner, "111111", users.ActionStartTransfer)); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.CancelTransfer(w.ctx, w.owner); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.AcceptTransfer(w.ctx, ana, w.confirm(ana, "222222", users.ActionAcceptTransfer)); !errors.Is(err, users.ErrNoTransfer) {
		t.Errorf("accepting a cancelled transfer = %v", err)
	}
	if err := w.svc.CancelTransfer(w.ctx, w.owner); !errors.Is(err, users.ErrNoTransfer) {
		t.Errorf("cancelling with nothing waiting = %v", err)
	}
}
