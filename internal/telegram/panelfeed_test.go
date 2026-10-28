package telegram_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/blocking"
	"github.com/mestre-ia/mestre/internal/i18n"
	"github.com/mestre-ia/mestre/internal/notices"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/internal/users"
)

func (w *world) feed() *notices.Feed {
	return &notices.Feed{DB: w.db, Clock: w.clk, Users: w.users, Mask: func(s string) string { return ghToken.ReplaceAllString(s, "***") }}
}

func (w *world) noticesOf(u users.User) []notices.Notice {
	w.t.Helper()
	got, err := w.feed().For(w.ctx, u, 50)
	if err != nil {
		w.t.Fatal(err)
	}
	return got
}

// Part of CA-CANAL-07 (the part of the panel): the unknown sender is ignored, the Owner gets
// the alert on the Telegram and the panel keeps it for the Owner and the
// Administrators, one notice that counts the tries, never for an Approver.
func TestTheAlertOfAnUnknownSenderIsAlsoInThePanelForTheOwnerAndTheAdministrators(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{ownerAlerts: true})
	w.pairOwnerTelegram()
	w.oa.Panel = w.feed()
	_, _ = w.user("Ana", users.Administrator, "loja")
	apr, _ := w.user("Caio", users.Approver, "loja")
	rando := faketelegram.User{ID: 555, FirstName: "Intruso", Username: "intruso"}
	for range 3 {
		if _, err := w.fake.UserSendsText(rando, faketelegram.Private(rando), "oi"); err != nil {
			t.Fatal(err)
		}
		w.poll()
		w.clk.Advance(20 * time.Second)
		w.drain()
	}
	got := w.noticesOf(w.owner)
	if len(got) != 1 || got[0].Kind != notices.Stranger || got[0].Audience != notices.Admins || !strings.Contains(got[0].In(i18n.PtBR), "ID: 555") {
		t.Fatalf("the Owner's bell: %+v", got)
	}
	if !strings.Contains(got[0].In(i18n.PtBR), "tentativas") {
		t.Errorf("the notice does not count the tries: %q", got[0].In(i18n.PtBR))
	}
	if got := w.noticesOf(apr); len(got) != 0 {
		t.Fatalf("an Approver sees the alert of a stranger: %+v", got)
	}
}

// Part of CA-TRAVA-05 (the notice "GitHub fora" and its return also in the panel).
// The alerts of the engine to the Owner and to the Administrators are kept in the panel
// once, also the one that waited for the bot; the GitHub that is down has its own kind.
func TestTheAlertsOfTheEngineAreKeptOnceForThePanel(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{ownerAlerts: true})
	w.oa.Panel = w.feed()
	w.oa.OwnerNotice(w.ctx, notices.GitHubDown, i18n.Text("GitHub fora desde 09:00:00. Último erro: 503."))
	w.oa.OwnerWorkspaceAlert(w.ctx, "loja", i18n.Text("O limite de uso da Loja chegou a 90%."))
	_, _ = w.oa.AdminAlert(w.ctx, "loja", i18n.Text("A mesa 42 parou."))
	w.oa.Tell(w.ctx, w.owner.ID, i18n.Text("Você foi pareado."))
	kinds := map[notices.Kind]int{}
	for _, n := range w.noticesOf(w.owner) {
		kinds[n.Kind]++
		if n.Kind == notices.Alert && n.Workspace != "loja" {
			t.Errorf("an alert of a workspace has none: %+v", n)
		}
	}
	if kinds[notices.GitHubDown] != 1 || kinds[notices.Alert] != 2 || kinds[notices.System] != 1 {
		t.Fatalf("the bell has %v", kinds)
	}
	// the bot is bound again: what waited goes out on the Telegram, and the bell has no second copy
	w.oa.Bind(w.bot)
	w.drain()
	if n := len(w.noticesOf(w.owner)); n != 4 {
		t.Fatalf("the bell has %d notices after the bot was bound, want 4", n)
	}
}

// Part of CA-BLOQ-04 (what the panel gets): `Notify` keeps the whole evidence, masked,
// the steps and the owner, and the notice reaches the bell of the Owner and the
// Administrators with a link to it; the plain message of `notify` reaches the users of the
// workspace who may be told, with the link to the table.
func TestTheBlockingNoticeAndTheMessageOfTheGrandMasterAlsoGoToThePanel(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true, mask: func(s string) string { return ghToken.ReplaceAllString(s, "***") }})
	w.pairOwnerTelegram()
	w.bc.Panel = w.feed()
	_, _ = w.user("Ana", users.Administrator, "loja")
	apr, _ := w.user("Caio", users.Approver, "loja")
	notice := stripeNotice("loja")
	notice.Evidence = strings.Repeat("linha de evidência\n", 9) + "ghp_abcdefgh12345678"
	id, _, err := w.bc.Notify(w.ctx, notice)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := (&blocking.Store{DB: w.db, Clock: w.clk}).Get(w.ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(saved.Evidence, "\n") != 9 || strings.Contains(saved.Evidence, "ghp_") || len(saved.Steps) != 1 || saved.Steps[0].Kind != blocking.StepVault || saved.Owner != "Lucas" {
		t.Fatalf("what was kept: %+v", saved)
	}
	if !strings.Contains(saved.Agent, "Por que não decido: envolve cobrança") || saved.Stopped != "falta a chave STRIPE_TEST_KEY" {
		t.Fatalf("what was kept of the agent: %+v", saved)
	}
	got := w.noticesOf(w.owner)
	if len(got) != 1 || got[0].Kind != notices.Blocked || got[0].LinkKind != notices.LinkBlocking || got[0].LinkRef != strconv.FormatInt(id, 10) || got[0].Mesa != "42-checkout" {
		t.Fatalf("the bell of the Owner: %+v", got)
	}
	if len(w.noticesOf(apr)) != 0 {
		t.Fatal("an Approver is told of a notice that blocks")
	}
	if _, err := w.bc.Say(w.ctx, "loja", "42-checkout", "A mesa terminou."); err != nil {
		t.Fatal(err)
	}
	if got := w.noticesOf(apr); len(got) != 1 || got[0].Kind != notices.Notify || got[0].LinkKind != notices.LinkTable || !strings.Contains(got[0].In(i18n.PtBR), "A mesa terminou.") {
		t.Fatalf("the bell of the Approver: %+v", got)
	}
}
