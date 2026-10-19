package telegram_test

import (
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/users"
)

// delegationOf makes the Owner's delegation of the workflows for a table, with
// the PIN, as the engine stores it.
func (w *world) delegationOf(owner users.User, mesa string) decrees.Decree {
	w.t.Helper()
	pend, err := w.ds.Propose(w.ctx, decrees.Proposal{Author: owner.ID, Kind: decrees.KindRule, Workspace: "loja", Mesa: mesa,
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "loja"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "envio_workflows"}, Text: "delegue os workflows"})
	if err != nil {
		w.t.Fatal(err)
	}
	req, err := w.ds.BeginPIN(w.ctx, pend.ID, owner.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.users.CheckPIN(w.ctx, req.ID, decreePIN); err != nil {
		w.t.Fatal(err)
	}
	d, err := w.ds.Confirm(w.ctx, pend.ID, owner.ID, req.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	return d
}

// pairedOwner pairs the Owner of the rig with a Telegram account and a PIN.
func (w *world) pairedOwner() (users.User, faketelegram.User) {
	w.t.Helper()
	c, err := w.users.NewCode(w.ctx, w.owner, w.owner.ID)
	if err != nil {
		w.t.Fatal(err)
	}
	w.next++
	u, err := w.users.Pair(w.ctx, users.PairRequest{Text: c.Code, TelegramID: w.next, Private: true})
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.users.SetPIN(w.ctx, u.ID, decreePIN); err != nil {
		w.t.Fatal(err)
	}
	return u, faketelegram.User{ID: w.next, FirstName: "Lucas", Username: "lucas"}
}

func (w *world) bindDelegation() {
	w.ds.SetReminder(w.dc)
	w.ds.SetRevokeHooks(telegram.RevokeAllHooks{Rules: w.rules, Users: w.users, Channel: w.dc})
}

// Part of CA-DELEG-03 over the bot: 3 days before the end the Owner gets the reminder
// with Renovar and Deixar vencer; Renovar proposes the renewal for the PIN.
func TestTheReminderOfADelegationHasRenovarAndDeixarVencer(t *testing.T) {
	t.Parallel()
	w := decreeWorld(t)
	w.bindDelegation()
	owner, ownerTG := w.pairedOwner()
	d := w.delegationOf(owner, "42-login")
	w.clk.Advance(27 * 24 * time.Hour)
	if err := w.ds.Sweep(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.drain()
	msg, keys := w.buttons(ownerTG)
	if keys["Renovar"] == "" || keys["Deixar vencer"] == "" {
		t.Fatalf("no buttons in the reminder: %v", keys)
	}
	if !strings.Contains(lastText(w, ownerTG), "acaba em") {
		t.Fatalf("the reminder says %q", lastText(w, ownerTG))
	}
	w.click(ownerTG, msg, keys["Renovar"])
	_, keys = w.buttons(ownerTG)
	if keys["Confirmar"] == "" {
		t.Fatalf("Renovar proposed nothing to confirm: %v (%q)", keys, lastText(w, ownerTG))
	}
	var open int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM decree_proposals WHERE state = 'open'`).Scan(&open); err != nil || open != 1 {
		t.Fatalf("open proposals = %d, %v", open, err)
	}
	if got, _ := w.ds.Get(w.ctx, d.Number); got.State != decrees.Active {
		t.Fatalf("Renovar changed the old decree before the confirmation: %+v", got)
	}
}

// Part of CA-DELEG-05 over the bot: /revogar_tudo is the Owner's, asks one button and no
// PIN, and the Administrators of the workspace are told; an Administrator is
// refused.
func TestRevogarTudoIsTheOwnersAndTellsTheAdministrators(t *testing.T) {
	t.Parallel()
	w := decreeWorld(t)
	w.bindDelegation()
	owner, ownerTG := w.pairedOwner()
	_, adminTG := w.pinned("Ana", users.Administrator, "loja")
	d := w.delegationOf(owner, "42-login")

	w.say(adminTG, "/revogar_tudo")
	w.drain()
	if _, keys := w.buttons(adminTG); keys["Revogar todas as delegações"] != "" {
		t.Fatal("an Administrator was offered the emergency button")
	}
	if got, _ := w.ds.Get(w.ctx, d.Number); got.State != decrees.Active {
		t.Fatal("an Administrator revoked")
	}

	w.say(ownerTG, "/revogar_tudo")
	w.drain()
	msg, keys := w.buttons(ownerTG)
	if keys["Revogar todas as delegações"] == "" {
		t.Fatalf("no confirmation button: %v", keys)
	}
	w.click(ownerTG, msg, keys["Revogar todas as delegações"])
	if got, _ := w.ds.Get(w.ctx, d.Number); got.State != decrees.Revoked {
		t.Fatalf("the delegation is %s", got.State)
	}
	found := false
	for _, text := range texts(w, adminTG.ID) {
		found = found || strings.Contains(text, "revogou todas as delegações")
	}
	if !found {
		t.Fatalf("the Administrator was not told: %v", texts(w, adminTG.ID))
	}
}
