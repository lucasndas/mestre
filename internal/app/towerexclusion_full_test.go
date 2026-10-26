//go:build full

package app

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/testkit/fakenet"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/transport"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// guildBot is the Owner at the fake Telegram with /guilda over the engine of the
// Citadel: the bot, its users and its clock are the test's own, and the two
// functions of the channel are the engine's (GuildMachines and SetExcluded).
type guildBot struct {
	t     *testing.T
	clk   *clock.Fake
	fake  *faketelegram.Server
	bot   *telegram.Bot
	owner faketelegram.User
}

type noAlerts struct{}

func (noAlerts) Unauthorized(context.Context, int64, string) {}
func (noAlerts) OwnerAlert(context.Context, string)          {}
func (noAlerts) Tell(context.Context, string, string)        {}

type noRouter struct{}

func (noRouter) Deliver(context.Context, telegram.Inbound) error { return nil }

func newGuildBot(t *testing.T, citadel *Engine, pin string) *guildBot {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc, err := users.New(users.Options{DB: db, Clock: clk, Alerts: noAlerts{}, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := svc.CreateOwner(ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.SetPIN(ctx, owner.ID, pin); err != nil {
		t.Fatal(err)
	}
	code, err := svc.NewCode(ctx, owner, owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	tg := faketelegram.User{ID: 77, FirstName: "Lucas", Username: "lucas"}
	if _, err := svc.Pair(ctx, users.PairRequest{Text: code.Code, TelegramID: tg.ID, Private: true}); err != nil {
		t.Fatal(err)
	}
	fake, err := faketelegram.New(clk, t, "123456:fake-bot-token")
	if err != nil {
		t.Fatal(err)
	}
	pins := &telegram.PINRouter{Users: svc}
	gc := &telegram.GuildChannel{Users: svc, PINs: pins, Machines: citadel.GuildMachines, SetExcluded: citadel.SetExcluded}
	bot, err := telegram.New(telegram.Options{DB: db, Clock: clk, Users: svc, Alerts: noAlerts{}, Router: noRouter{},
		Client:   &telegram.Client{Token: "123456:fake-bot-token", HTTP: &http.Client{Transport: fakenet.HandlerTransport(fake)}},
		Commands: gc.Commands(), Actions: gc.Actions(), Captures: map[string]telegram.CaptureFunc{telegram.PINKind: pins.Capture()}, PollTimeout: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	gc.Bind(bot)
	return &guildBot{t: t, clk: clk, fake: fake, bot: bot, owner: tg}
}

func (g *guildBot) poll() {
	g.t.Helper()
	if err := g.bot.PollOnce(g.t.Context()); err != nil {
		g.t.Fatal(err)
	}
	for range 3 {
		g.clk.Advance(1100 * time.Millisecond)
		if _, err := g.bot.Flush(g.t.Context()); err != nil {
			g.t.Fatal(err)
		}
	}
}

func (g *guildBot) say(text string) {
	g.t.Helper()
	if _, err := g.fake.UserSendsText(g.owner, faketelegram.Private(g.owner), text); err != nil {
		g.t.Fatal(err)
	}
	g.poll()
}

// tap presses the button of the last message with buttons, by its text.
func (g *guildBot) tap(label string) {
	g.t.Helper()
	var msg int64
	var key string
	for _, s := range g.fake.Sent() {
		kb, ok := s.Params["reply_markup"].(map[string]any)
		if s.Method != "sendMessage" || s.ChatID != g.owner.ID || !ok || kb["inline_keyboard"] == nil {
			continue
		}
		msg, key = s.MessageID, ""
		for _, row := range kb["inline_keyboard"].([]any) {
			for _, b := range row.([]any) {
				if bt := b.(map[string]any); strings.HasPrefix(bt["text"].(string), label) {
					key = bt["callback_data"].(string)
				}
			}
		}
	}
	if key == "" {
		g.t.Fatalf("no button %q in the last message", label)
	}
	if _, err := g.fake.UserClicks(g.owner, msg, key); err != nil {
		g.t.Fatal(err)
	}
	g.poll()
}

// CA-GUILDA-12 (the criterion is complete: this is the composition with the engines; the Owner's
// button and PIN alone are in internal/telegram/guildchan_test.go, in the fast suite).
// The Owner excludes a Tower that holds the vault from the fake Telegram: /guilda,
// the button Excluir and the PIN (a wrong one does nothing). The engines do what
// the criterion says with the hook the Citadel fires wired to the replication:
// the Tower erases the key, the envelope and the vault tables of its replica (the
// Citadel audits the receipt), its certificates go on without mestre.local, a
// promotion of it is refused (by itself and by the Citadel), and Desfazer
// exclusão, with the PIN, brings the vault back. The part of the PIN and of the
// buttons alone, with a fake Guild, is in internal/telegram/guildchan_test.go
// (same id, fast suite).
func TestExcludingATowerErasesItsVaultAndUndoingItBringsTheVaultBack(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	a := r.node("a")
	b := r.node("b", a)
	r.found(a, clock.Real{})
	r.start(a, clock.Real{})
	r.waitFor("a to act", a.acting)
	r.join(b, a, clock.Real{})
	r.start(b, clock.Real{})
	ctx := t.Context()

	secrets := func() int {
		n := 0
		// a replica that has no copy yet, or whose vault tables are gone, has no secrets
		_ = b.e.withGuildDB(func(db *store.DB) error {
			return db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM secrets`).Scan(&n)
		})
		return n
	}
	keyAndEnvelope := func() (key, envelope bool) {
		data, err := b.e.vaultCopy().Keys.Load(ctx)
		_, envErr := os.Stat(b.paths.envelope())
		return err == nil && len(data) > 0, envErr == nil
	}
	promotable := func() error {
		return b.e.withGuildDB(func(db *store.DB) error { return b.e.promotable(ctx, db) })
	}

	// the Tower has the vault: key, envelope and the tables (the CA of the Guild is in them)
	r.waitFor("b to hold the vault", func() bool {
		k, e := keyAndEnvelope()
		return b.connected(1) && k && e && secrets() > 0 && b.e.Status().LSN >= a.e.Status().LSN
	})
	want := secrets()
	if hasName(b.e.cert.Leaf(), "mestre.local") {
		t.Fatal("the certificate of a Tower carries mestre.local")
	}
	if err := a.e.citadelNow().Promotable(ctx, b.id); err != nil {
		t.Fatalf("a Tower with the vault is not promotable: %v", err)
	}
	r.waitFor("b to be promotable", func() bool { return promotable() == nil })

	// the Owner excludes it: /guilda, Excluir, a wrong PIN changes nothing, the right one excludes
	owner := newGuildBot(t, a.e, "246810")
	owner.say("/guilda")
	owner.tap("Excluir")
	for range 3 { // three wrong ones end the request
		owner.say("000000")
	}
	if m, err := a.e.citadelNow().Machine(ctx, b.id); err != nil || m.Excluded {
		t.Fatalf("a wrong PIN excluded the machine (%v, %v)", m.Excluded, err)
	}
	owner.say("/guilda")
	owner.tap("Excluir")
	owner.say("246810")
	r.waitFor("b to erase the key, the envelope and the vault tables", func() bool {
		k, e := keyAndEnvelope()
		return !k && !e && secrets() == 0
	})
	const droppedRows = `SELECT id FROM audit_log WHERE action = 'vault.dropped' AND machine_id = ?`
	r.waitFor("the Citadel to audit the receipt of b", func() bool { return len(a.query(droppedRows, b.id)) == 1 })
	if sr := a.query(`SELECT count(*) FROM secrets`); len(sr) != 1 || sr[0] == "0" {
		t.Errorf("the Citadel lost its own vault: %v", sr)
	}

	// the certificates of the excluded machine follow without mestre.local, renewed or not
	oldSerial := transport.Serial(b.e.cert.Leaf())
	if err := b.e.towerNow().renewer.Renew(ctx); err != nil {
		t.Fatalf("the excluded Tower renewing its certificate: %v", err)
	}
	r.waitFor("b to present a renewed certificate", func() bool { return transport.Serial(b.e.cert.Leaf()) != oldSerial })
	for _, n := range []*inproc{b} {
		if hasName(n.e.cert.Leaf(), "mestre.local") || hasName(n.e.machine.Leaf(), "mestre.local") {
			t.Errorf("a certificate of the excluded machine %s carries mestre.local", n.name)
		}
	}
	if got := a.query(`SELECT serial FROM machine_credentials WHERE machine_id = ? AND role = 'cidadela'`, b.id); len(got) != 0 {
		t.Errorf("the registry issued a certificate of the Citadel to an excluded machine: %v", got)
	}

	// a promotion of it is refused, by the Citadel and by the machine
	if err := a.e.citadelNow().Promotable(ctx, b.id); !errors.Is(err, guild.ErrExcludedMachine) {
		t.Errorf("the Citadel on the promotion of an excluded machine: %v", err)
	}
	r.waitFor("b to know it is excluded", func() bool { return errors.Is(promotable(), guild.ErrExcludedMachine) })
	if err := b.e.Promote(ctx, "test"); !errors.Is(err, guild.ErrNotPromotable) || !errors.Is(err, guild.ErrExcludedMachine) {
		t.Errorf("the promotion of the excluded machine: %v", err)
	}
	if st := b.e.Status(); st.Role != RoleTower || st.Failover {
		t.Errorf("the refused promotion left b as %+v", st)
	}

	// the exclusion undone: the vault comes back, with every table, and the promotion is open again
	owner.say("/guilda")
	owner.tap("Desfazer exclusão")
	owner.say("246810")
	r.waitFor("b to receive the vault again", func() bool {
		k, e := keyAndEnvelope()
		return k && e && secrets() == want
	})
	r.waitFor("b to be promotable again", func() bool { return promotable() == nil })
	if err := a.e.citadelNow().Promotable(ctx, b.id); err != nil {
		t.Errorf("the Citadel on the promotion after the exclusion was undone: %v", err)
	}
}
