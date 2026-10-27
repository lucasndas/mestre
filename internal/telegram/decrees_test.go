package telegram_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/tablerules"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/internal/users"
)

const decreePIN = "482913"

func decreeWorld(t *testing.T) *world {
	t.Helper()
	return newWorld(t, rigOpts{decrees: true, ownerAlerts: true})
}

// pinned registers a paired user with a PIN.
func (w *world) pinned(name string, level users.Level, workspaces ...string) (users.User, faketelegram.User) {
	w.t.Helper()
	u, tg := w.user(name, level, workspaces...)
	if err := w.users.SetPIN(w.ctx, u.ID, decreePIN); err != nil {
		w.t.Fatal(err)
	}
	return u, tg
}

func allowProposal(author users.User, class string, cmds ...string) decrees.Proposal {
	return decrees.Proposal{Author: author.ID, Kind: decrees.KindRule, Workspace: "loja", Target: decrees.Target{Kind: decrees.TargetClass, Name: class},
		Effect: decrees.Effect{Kind: decrees.EffectPermission, Allow: &classes.Grant{Commands: cmds}},
		Text:   "o " + class + " pode " + strings.Join(cmds, ", "), Origin: decrees.Origin{Channel: "telegram", MessageID: 5}}
}

// offer proposes, as the Grand Master does, and puts the proposal in front of
// its author.
func (w *world) offer(p decrees.Proposal) decrees.Pending {
	w.t.Helper()
	pend, err := w.ds.Propose(w.ctx, p)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.dc.Offer(w.ctx, pend); err != nil {
		w.t.Fatal(err)
	}
	w.drain()
	return pend
}

func lastText(w *world, u faketelegram.User) string {
	all := texts(w, u.ID)
	if len(all) == 0 {
		return ""
	}
	return all[len(all)-1]
}

// Part of CA-DEC-08: the creation over the bot, the effect in the rules and the
// revocation; the agent's side of the heavy criterion is the M7's.
func TestTheAuthorConfirmsWithTheButtonAndThePINAndTheDecreeIsActive(t *testing.T) {
	t.Parallel()
	w := decreeWorld(t)
	ana, anaTG := w.pinned("Ana", users.Administrator, "loja")
	_, caioTG := w.pinned("Caio", users.Administrator, "loja")
	pend := w.offer(allowProposal(ana, "dev", "npm install *"))
	msg, keys := w.buttons(anaTG)
	if keys["Confirmar"] == "" || keys["Cancelar"] == "" {
		t.Fatalf("no buttons: %v", keys)
	}
	if text := texts(w, anaTG.ID)[0]; !strings.Contains(text, "Entendi: permitir comandos npm install * para a classe dev, permanente (campanha loja). Confirma?") || !strings.Contains(text, "PIN") || !strings.Contains(text, "Sua ordem: ") {
		t.Fatalf("the confirmation says %q", text)
	}
	// the click of another user is not the author's
	w.click(caioTG, msg, keys["Confirmar"])
	if w.count("decrees") != 0 {
		t.Fatal("another user confirmed")
	}
	// the click of the author asks the PIN, and nothing is a decree before it
	w.click(anaTG, msg, keys["Confirmar"])
	if w.count("decrees") != 0 {
		t.Fatal("a decree was made before the PIN")
	}
	if !strings.Contains(lastText(w, anaTG), "PIN") {
		t.Fatalf("no prompt for the PIN: %q", lastText(w, anaTG))
	}
	w.say(anaTG, "000000")
	w.drain()
	if w.count("decrees") != 0 {
		t.Fatal("a wrong PIN made a decree")
	}
	w.say(anaTG, decreePIN)
	w.drain()
	if w.count("decrees") != 1 {
		t.Fatalf("%d decrees after the right PIN", w.count("decrees"))
	}
	if got := strings.Join(texts(w, anaTG.ID), "|"); !strings.Contains(got, "Decreto 1 ativo: permitir comandos npm install * para a classe dev.") {
		t.Fatalf("the author was told %q", got)
	}
	d, err := w.ds.Get(w.ctx, 1)
	if err != nil || d.State != decrees.Active || !d.PIN || d.Author != ana.ID || d.Origin.MessageID != 5 {
		t.Fatalf("%+v, %v", d, err)
	}
	// the PIN never went out, and never reached the Grand Master
	for _, s := range w.fake.Sent() {
		if strings.Contains(s.Text, decreePIN) || strings.Contains(s.Text, "000000") {
			t.Errorf("a PIN went out: %q", s.Text)
		}
	}
	if w.router.count() != 0 {
		t.Fatal("the PIN reached the inbox of the Grand Master")
	}
	// a proposal is confirmed once
	if _, err := w.ds.Confirm(w.ctx, pend.ID, ana.ID, ""); err == nil {
		t.Fatal("confirmed twice")
	}
	// the revocation from /decretos: the rule only freed something, no PIN
	w.say(anaTG, "/decretos")
	w.drain()
	msg, keys = w.buttons(anaTG)
	if keys["Revogar #1"] == "" {
		t.Fatalf("no Revogar: %v", keys)
	}
	w.click(anaTG, msg, keys["Revogar #1"])
	if d, _ := w.ds.Get(w.ctx, 1); d.State != decrees.Revoked {
		t.Fatalf("%s", d.State)
	}
	if decs, _ := w.ds.List(w.ctx, ana.ID); len(decs) != 0 {
		t.Fatalf("%+v", decs)
	}
}

func TestCancelAndAWrongPINLeaveNothing(t *testing.T) {
	t.Parallel()
	w := decreeWorld(t)
	ana, anaTG := w.pinned("Ana", users.Administrator, "loja")
	w.offer(allowProposal(ana, "dev", "npm install *"))
	msg, keys := w.buttons(anaTG)
	w.click(anaTG, msg, keys["Cancelar"])
	if w.count("decrees") != 0 {
		t.Fatal("a decree was made")
	}
	// a PIN wrong three times cancels the proposal
	w.offer(allowProposal(ana, "dev", "make test"))
	msg, keys = w.buttons(anaTG)
	w.click(anaTG, msg, keys["Confirmar"])
	for range users.PINRequestErrors {
		w.say(anaTG, "111111")
		w.drain()
	}
	if got := lastText(w, anaTG); !strings.Contains(got, "cancelada") && !strings.Contains(got, "incorreto") {
		t.Fatalf("%q", got)
	}
	if w.count("decrees") != 0 {
		t.Fatal("a decree was made")
	}
	var open int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM decree_proposals WHERE state = 'open'`).Scan(&open); err != nil || open != 0 {
		t.Fatalf("%d proposals still open (%v)", open, err)
	}
}

func (w *world) count(table string) int {
	w.t.Helper()
	var n int
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM `+table).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// Part of CA-DEC-11 (the decree of "Negar sempre" is made here by the service; the
// answer through the flow of approval is in
// internal/orchestration/chain/delegation_audit_test.go)
func TestDenyAlwaysNeedsNoPINAndAllowsAskItAndTheTableRulesAreListedWithRevogar(t *testing.T) {
	t.Parallel()
	w := decreeWorld(t)
	ana, anaTG := w.pinned("Ana", users.Administrator, "loja")
	caio, caioTG := w.pinned("Caio", users.Approver, "loja")
	// "Negar sempre": the permanent decree that denies, with no PIN
	act := classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "curl https://exemplo.io/x"}
	w.requests[7] = chain.Request{ID: 7, Workspace: "loja", Table: "42-login", Type: classes.RequestClassPermission, Requester: "d1", Class: "dev", Action: &act, About: "curl"}
	if err := w.ds.Decree(w.ctx, chain.DecreeGrant{Request: 7, Answer: chain.AnswerDenyAlways, Character: "d1", Class: "dev", By: ana.ID, Level: chain.Level{Kind: chain.LevelPeople}, Pattern: "curl *"}); err != nil {
		t.Fatal(err)
	}
	denied, err := w.ds.Get(w.ctx, 1)
	if err != nil || denied.PIN || denied.Effect.Deny == nil || denied.State != decrees.Active {
		t.Fatalf("%+v, %v", denied, err)
	}
	// a permanent decree that frees asks the PIN
	pend, err := w.ds.Propose(w.ctx, allowProposal(ana, "dev", "npm install *"))
	if err != nil || !pend.PIN {
		t.Fatalf("%+v, %v", pend, err)
	}
	// a temporary table rule, as an agent makes it
	if _, err := (&tablerules.Store{DB: w.db}).Insert(w.ctx, tablerules.Rule{Workspace: "loja", Table: "42-login", Class: "dev", Request: 9, State: tablerules.Active, Level: chain.LevelMesa,
		Author: "mestre-42", Risk: classes.RiskLow, Pattern: "make *", ToolKind: providers.KindShell, Until: w.clk.Now().Add(3 * time.Hour), CreatedAt: w.clk.Now()}, "teste"); err != nil {
		t.Fatal(err)
	}
	// the Approver sees the table rule, but no Revogar of it nor of the rule
	w.say(caioTG, "/decretos")
	w.drain()
	list := lastText(w, caioTG)
	if !strings.Contains(list, "regra temporária de mesa 1") || !strings.Contains(list, "make *") || !strings.Contains(list, "#1 · Ana (Administrador)") {
		t.Fatalf("/decretos says %q", list)
	}
	if _, keys := w.buttons(caioTG); len(keys) != 0 {
		t.Fatalf("an Approver was given %v", keys)
	}
	// the Administrator sees both, with Revogar
	w.say(anaTG, "/decretos")
	w.drain()
	msg, keys := w.buttons(anaTG)
	if keys["Revogar #1"] == "" || keys["Revogar regra de mesa 1"] == "" {
		t.Fatalf("buttons %v", keys)
	}
	// demoted before the tap: the button of a table rule is refused by the level at the click
	if _, err := w.users.Edit(w.ctx, w.owner, ana.ID, users.Edit{Level: ptr(users.Approver)}); err != nil {
		t.Fatal(err)
	}
	w.click(anaTG, msg, keys["Revogar regra de mesa 1"])
	live, _ := (&tablerules.Store{DB: w.db}).Live(w.ctx, "")
	if len(live) != 1 {
		t.Fatalf("an Approver revoked a table rule: %d live", len(live))
	}
	if _, err := w.users.Edit(w.ctx, w.owner, ana.ID, users.Edit{Level: ptr(users.Administrator)}); err != nil {
		t.Fatal(err)
	}
	w.click(anaTG, msg, keys["Revogar regra de mesa 1"])
	if live, _ = (&tablerules.Store{DB: w.db}).Live(w.ctx, ""); len(live) != 0 {
		t.Fatalf("the Administrator did not revoke the table rule: %d live", len(live))
	}
	// revoking the decree that restricted asks the PIN
	w.click(anaTG, msg, keys["Revogar #1"])
	if d, _ := w.ds.Get(w.ctx, 1); d.State != decrees.Active {
		t.Fatalf("revoked without the PIN: %s", d.State)
	}
	w.say(anaTG, decreePIN)
	w.drain()
	if d, _ := w.ds.Get(w.ctx, 1); d.State != decrees.Revoked {
		t.Fatalf("not revoked with the PIN: %s", d.State)
	}
	_ = caio
}

func ptr[T any](v T) *T { return &v }

func TestTwoOpposedDecreesTellTheAuthorsAndTheOwnerOverTheBot(t *testing.T) {
	t.Parallel()
	w := decreeWorld(t)
	ana, anaTG := w.pinned("Ana", users.Administrator, "loja")
	bia, biaTG := w.pinned("Bia", users.Administrator, "loja")
	owner := w.pairOwnerTelegram()
	w.confirmNow(ana, allowProposal(ana, "dev", "make x"))
	deny := allowProposal(bia, "dev", "make x")
	deny.Effect = decrees.Effect{Kind: decrees.EffectPermission, Deny: &classes.Grant{Commands: []string{"make x"}}}
	w.confirmNow(bia, deny)
	w.drain()
	for name, tg := range map[string]faketelegram.User{"Ana": anaTG, "Bia": biaTG, "o Senhor da Guilda": owner} {
		var told bool
		for _, s := range texts(w, tg.ID) {
			told = told || (strings.Contains(s, "se opõem") && strings.Contains(s, "suspensos"))
		}
		if !told {
			t.Errorf("%s was not told of the conflict: %q", name, texts(w, tg.ID))
		}
	}
	for _, n := range []int64{1, 2} {
		if d, _ := w.ds.Get(w.ctx, n); d.State != decrees.Suspended {
			t.Fatalf("decree %d is %s", n, d.State)
		}
	}
}

// confirmNow proposes and confirms with the PIN, without the buttons.
func (w *world) confirmNow(u users.User, p decrees.Proposal) {
	w.t.Helper()
	pend, err := w.ds.Propose(w.ctx, p)
	if err != nil {
		w.t.Fatal(err)
	}
	pin := ""
	if pend.PIN {
		pin = w.pinFor(u, "decree:"+pend.ID)
	}
	if _, err := w.ds.Confirm(w.ctx, pend.ID, u.ID, pin); err != nil {
		w.t.Fatal(err)
	}
}

func (w *world) pinFor(u users.User, action string) string {
	w.t.Helper()
	req, err := w.users.StartPIN(w.ctx, u.ID, action)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := w.users.CheckPIN(w.ctx, req.ID, decreePIN); err != nil {
		w.t.Fatal(err)
	}
	return req.ID
}
