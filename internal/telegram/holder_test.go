package telegram_test

import (
	"context"
	"testing"

	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/internal/users"
)

type holdGate struct {
	open     bool
	held     int
	released int
}

func (g *holdGate) Allowed(context.Context) bool { return true }
func (g *holdGate) Hold(context.Context) (func(), bool) {
	if !g.open {
		return nil, false
	}
	g.held++
	return func() { g.released++ }, true
}

var _ telegram.Holder = (*holdGate)(nil)

// Part of CA-CIDADELA-19: the bot handles an update holding the gate of the handover. While the
// Citadel drains for a planned switch the update is recorded and waits (nothing it would
// change lands after the last lsn); once the gate admits, it is handled and the gate is freed.
func TestTheBotHandlesAnUpdateOnlyHoldingTheGateOfTheHandover(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	_, ta := w.user("Ana", users.Approver)
	g := &holdGate{}
	w.opts.Gate = g
	var err error
	if w.bot, err = telegram.New(w.opts); err != nil {
		t.Fatal(err)
	}
	if _, err := w.fake.UserSendsText(ta, faketelegram.Private(ta), "ola"); err != nil {
		t.Fatal(err)
	}
	w.poll()
	if w.router.count() != 0 || g.held != 0 {
		t.Fatalf("handled %d while draining (held %d)", w.router.count(), g.held)
	}
	g.open = true
	w.poll()
	if w.router.count() != 1 || g.held != 1 || g.released != 1 {
		t.Fatalf("after the gate opened: handled %d, held %d, released %d; want 1, 1, 1", w.router.count(), g.held, g.released)
	}
}
