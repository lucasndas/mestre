package telegram_test

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/orchestration/chain"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/tablerules"
	"github.com/lucasndas/mestre/internal/providers"
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

// gmNotices records what the hook of the Grão-Mestre of each workspace is told.
type gmNotices struct {
	mu  sync.Mutex
	got map[string][]string
}

func (g *gmNotices) tell(_ context.Context, ws, text string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.got == nil {
		g.got = map[string][]string{}
	}
	g.got[ws] = append(g.got[ws], text)
}

func (g *gmNotices) of(ws string) []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.got[ws]...)
}

// tempRule is a live temporary table rule of a workspace, as an agent makes it.
func (w *world) tempRule(ws, table string, request int64) int64 {
	w.t.Helper()
	r, err := (&tablerules.Store{DB: w.db}).Insert(w.ctx, tablerules.Rule{Workspace: ws, Table: table, Class: "dev", Request: request, State: tablerules.Active, Level: chain.LevelMesa,
		Author: "mestre-" + table, Risk: classes.RiskLow, Pattern: "make *", ToolKind: providers.KindShell, Until: w.clk.Now().Add(3 * time.Hour), CreatedAt: w.clk.Now()}, "teste")
	if err != nil {
		w.t.Fatal(err)
	}
	return r.ID
}

// rawExec runs a statement on a connection of its own to the file of the Guild,
// outside the guard of the store (a trigger that makes a write fail).
func (w *world) rawExec(stmt string) {
	w.t.Helper()
	raw, err := sql.Open("sqlite", w.db.Path())
	if err != nil {
		w.t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(w.ctx, stmt); err != nil {
		w.t.Fatal(err)
	}
}

// CA-DELEG-05: over the bot and the real rules (the temporary table rules are revoked
// after the transaction of the delegations, not in it): /revogar_tudo is the
// Owner's, asks one button and no PIN; the delegations, the subdelegations and
// the real temporary table rules of two workspaces end, the delegations in one
// transaction (a failure in the middle changes nothing), the audit has the Owner
// and the time, and the Administrators and the Grão-Mestres are told; an
// Administrator is refused with the level it needs.
func TestRevogarTudoIsTheOwnersAndTellsTheAdministrators(t *testing.T) {
	t.Parallel()
	w := decreeWorld(t)
	gms := &gmNotices{}
	w.ds.SetReminder(w.dc)
	w.ds.SetRevokeHooks(telegram.RevokeAllHooks{Rules: w.rules, Users: w.users, Channel: w.dc, GrandMaster: gms.tell})
	owner, ownerTG := w.pairedOwner()
	_, adminTG := w.pinned("Ana", users.Administrator, "loja", "blog")
	loja := w.delegationOf(owner, "42-login")
	pend, err := w.ds.Propose(w.ctx, decrees.Proposal{Author: owner.ID, Kind: decrees.KindRule, Workspace: "blog", Mesa: "7-post",
		Target: decrees.Target{Kind: decrees.TargetWorkspace, Name: "blog"}, Effect: decrees.Effect{Kind: decrees.EffectDelegation, Setting: "configuracao_repositorio"}, Text: "delegue"})
	if err != nil {
		t.Fatal(err)
	}
	blog, err := w.ds.Confirm(w.ctx, pend.ID, owner.ID, w.pinFor(owner, "decree:"+pend.ID))
	if err != nil {
		t.Fatal(err)
	}
	gm := characters.Character{ID: "gm-loja", Role: characters.RoleGrandMaster, Workspace: "loja"}
	tm := characters.Character{ID: "tm-42", Role: characters.RoleTableMaster, Workspace: "loja", Table: "42-login"}
	sub, err := w.ds.Subdelegate(w.ctx, decrees.SubRequest{By: gm, Grantee: tm, Mesa: "42-login", Type: "envio_workflows", Until: w.clk.Now().Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	rules := []int64{w.tempRule("loja", "42-login", 9), w.tempRule("blog", "7-post", 10)}
	live := func() int {
		t.Helper()
		got, err := (&tablerules.Store{DB: w.db}).Live(w.ctx, "")
		if err != nil {
			t.Fatal(err)
		}
		return len(got)
	}
	untouched := func(when string) {
		t.Helper()
		for _, n := range []int64{loja.Number, blog.Number} {
			if got, _ := w.ds.Get(w.ctx, n); got.State != decrees.Active {
				t.Fatalf("%s: the delegation %d is %s", when, n, got.State)
			}
		}
		if got, _ := w.ds.Sub(w.ctx, sub.ID); !got.Active {
			t.Fatalf("%s: the subdelegation ended", when)
		}
		if n := live(); n != 2 {
			t.Fatalf("%s: %d temporary rules live, want 2", when, n)
		}
	}

	// an Administrator is refused, with the level it needs
	w.say(adminTG, "/revogar_tudo")
	w.drain()
	if _, keys := w.buttons(adminTG); keys["Revogar todas as delegações"] != "" {
		t.Fatal("an Administrator was offered the emergency button")
	}
	if got := lastText(w, adminTG); !strings.Contains(got, "exige o nível Dono") {
		t.Fatalf("the Administrator was told %q, want the level it needs", got)
	}
	untouched("after the Administrator")

	// a failure in the middle of the transaction changes nothing
	w.rawExec(`CREATE TRIGGER fail_revocation BEFORE INSERT ON delegation_revocations BEGIN SELECT RAISE(ABORT, 'disco cheio'); END`)
	w.say(ownerTG, "/revogar_tudo")
	w.drain()
	msg, keys := w.buttons(ownerTG)
	if keys["Revogar todas as delegações"] == "" {
		t.Fatalf("no confirmation button: %v", keys)
	}
	w.click(ownerTG, msg, keys["Revogar todas as delegações"])
	untouched("after a failed transaction")
	w.rawExec(`DROP TRIGGER fail_revocation`)

	w.say(ownerTG, "/revogar_tudo")
	w.drain()
	msg, keys = w.buttons(ownerTG)
	w.clk.Advance(time.Minute)
	at := w.clk.Now()
	w.click(ownerTG, msg, keys["Revogar todas as delegações"])
	for _, n := range []int64{loja.Number, blog.Number} {
		got, _ := w.ds.Get(w.ctx, n)
		if got.State != decrees.Revoked {
			t.Fatalf("the delegation %d is %s", n, got.State)
		}
		hist, err := w.ds.History(w.ctx, n)
		if err != nil || len(hist) == 0 {
			t.Fatalf("history of %d = %+v, %v", n, hist, err)
		}
		if last := hist[len(hist)-1]; last.By != owner.ID || !last.At.Equal(at) {
			t.Fatalf("the audit of %d = %+v, want the Owner at %v", n, last, at)
		}
	}
	if got, _ := w.ds.Sub(w.ctx, sub.ID); got.Active || got.EndedBy != owner.ID {
		t.Fatalf("the subdelegation = %+v", got)
	}
	if n := live(); n != 0 {
		t.Fatalf("%d temporary rules still live", n)
	}
	for _, id := range rules {
		r, err := (&tablerules.Store{DB: w.db}).Get(w.ctx, id)
		if err != nil || r.State != tablerules.Revoked {
			t.Fatalf("the temporary rule %d = %+v, %v", id, r, err)
		}
	}
	var by string
	var when int64
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT by_user, at FROM delegation_revocations`).Scan(&by, &when); err != nil || by != owner.ID || when != at.UnixMilli() {
		t.Fatalf("the audit row = %q at %d, %v; want the Owner at %d", by, when, err, at.UnixMilli())
	}
	told := 0
	for _, text := range texts(w, adminTG.ID) {
		if strings.Contains(text, "revogou todas as delegações") {
			told++
		}
	}
	if told != 2 {
		t.Fatalf("the Administrator of both workspaces was told %d times: %v", told, texts(w, adminTG.ID))
	}
	for _, ws := range []string{"loja", "blog"} {
		if got := gms.of(ws); len(got) != 1 || !strings.Contains(got[0], "revogou todas as delegações") {
			t.Fatalf("the Grão-Mestre of %s was told %v", ws, got)
		}
	}
}
