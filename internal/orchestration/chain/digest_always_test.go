package chain_test

import (
	"context"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/decrees"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/digest"
	"github.com/mestre-ia/mestre/internal/users"
)

type digestSent struct {
	mu   sync.Mutex
	sent []digest.Message
}

func (s *digestSent) Send(_ context.Context, m digest.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, m)
	return nil
}

func (s *digestSent) texts(user string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, m := range s.sent {
		if m.UserID == user {
			out = append(out, m.Text)
		}
	}
	return out
}

type digestNoInbox struct{}

func (digestNoInbox) ToGrandMaster(context.Context, string, string) error { return nil }

type digestAlerts struct{}

func (digestAlerts) Unauthorized(context.Context, int64, string) {}
func (digestAlerts) OwnerAlert(context.Context, string)          {}
func (digestAlerts) Tell(context.Context, string, string)        {}

// CA-APROV-RES-05: the Mestre da mesa decides "aprovar sempre" with the
// command, on a real request of the chain; when the summary goes out the Owner
// gets the suggestion with the reason the agent gave, and no decree exists
// before the Aceitar with the PIN.
func TestAnAlwaysDecidedByTheTableMasterReachesTheOwnerAsASuggestionWithItsReason(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	db := w.orc.Registry.DB
	usvc, err := users.New(users.Options{DB: db, Clock: w.clk, Alerts: digestAlerts{}, PIN: users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}})
	if err != nil {
		t.Fatal(err)
	}
	owner, err := usvc.CreateOwner(w.ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := decrees.New(decrees.Options{DB: db, Clock: w.clk, PINs: usvc})
	if err != nil {
		t.Fatal(err)
	}
	sent := &digestSent{}
	svc, err := digest.New(digest.Options{DB: db, Clock: w.clk, Source: &chain.Store{DB: db}, Policy: chain.StaticPolicy{}, Inbox: digestNoInbox{}, Directory: digest.UsersDirectory{Users: usvc},
		Sender: sent, Rand: rand.New(rand.NewPCG(1, 1)), Decrees: ds})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(w.ctx); err != nil { // the first sight of the workspace only notes the slot
		t.Fatal(err)
	}
	dev := w.recruit("Dev")
	id := w.hookReq(t, dev, "npm install lodash", classes.RiskLow)
	const reason = "são só dependências de dev"
	got := w.cli(w.master, "approval decide", map[string]any{"reason": reason}, itoa(id), "always")
	if !got.OK || got.Data["suggested"] != true {
		t.Fatalf("always = %+v, want approved with a suggestion", got)
	}
	if r := w.request(id); r.State != chain.Approved || r.Level.Kind != chain.LevelMesa {
		t.Fatalf("the request is %s at %+v, want approved by the Mestre da mesa", r.State, r.Level)
	}
	count := func() int {
		var n int
		if err := db.Reader().QueryRowContext(w.ctx, `SELECT COUNT(*) FROM decrees`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	now := w.clk.Now()
	w.clk.Advance(time.Date(now.Year(), now.Month(), now.Day(), 18, 0, 30, 0, time.UTC).Sub(now))
	if err := svc.Tick(w.ctx); err != nil {
		t.Fatal(err)
	}
	var text string
	for _, x := range sent.texts(owner.ID) {
		if strings.Contains(x, "Sugestão de regra") {
			text = x
		}
	}
	if text == "" {
		t.Fatalf("the Owner got no suggestion: %q", sent.texts(owner.ID))
	}
	for _, want := range []string{"`npm install *`", "classe Dev", reason, "texto do agente, não verificado"} {
		if !strings.Contains(text, want) {
			t.Errorf("the suggestion lacks %q: %q", want, text)
		}
	}
	if n := count(); n != 0 {
		t.Fatalf("%d decrees exist before the Aceitar", n)
	}
	open, err := svc.Suggestions(w.ctx, w.slug, "open")
	if err != nil || len(open) != 1 {
		t.Fatalf("suggestions = %+v, %v", open, err)
	}
	pend, err := svc.Accept(w.ctx, open[0].ID, owner.ID)
	if err != nil || !pend.PIN {
		t.Fatalf("accept = %+v, %v, want a proposal that waits for the PIN", pend, err)
	}
	if n := count(); n != 0 {
		t.Fatalf("%d decrees exist before the PIN is given", n)
	}
}
