package telegram_test

import (
	"context"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/orchestration/chain"
	"github.com/mestre-ia/mestre/internal/orchestration/digest"
	"github.com/mestre-ia/mestre/internal/providers"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/internal/users"
)

// digestOwner pairs the Owner of the rig with a Telegram account.
func (w *world) digestOwner() (users.User, faketelegram.User) {
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

type noInbox struct{}

func (noInbox) ToGrandMaster(context.Context, string, string) error { return nil }

// Part of CA-APROV-RES-02: the suggestion reaches the Owner over the bot with
// Aceitar, Recusar and Ver exemplos; Aceitar proposes the decree, which waits
// for the PIN.
func TestTheSuggestionOfTheSummaryIsAnsweredWithTheButtons(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{decrees: true, digest: true})
	owner, ownerTG := w.digestOwner()
	st := &chain.Store{DB: w.db}
	svc, err := digest.New(digest.Options{DB: w.db, Clock: w.clk, Source: st, Policy: chain.StaticPolicy{}, Inbox: noInbox{}, Directory: digest.UsersDirectory{Users: w.users},
		Sender: w.dg, Rand: rand.New(rand.NewPCG(1, 1)), Decrees: w.ds})
	if err != nil {
		t.Fatal(err)
	}
	w.dg.Svc = svc
	if err := svc.Tick(w.ctx); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"a", "b", "c"} {
		r, err := st.Insert(w.ctx, chain.Request{Workspace: "loja", Table: "42-login", Type: classes.RequestClassPermission, State: chain.Approved, Class: "dev", About: "npm install " + pkg,
			Action: &classes.Action{Kind: providers.KindShell, Tool: "Bash", Command: "npm install " + pkg}, CreatedAt: w.clk.Now()})
		if err != nil {
			t.Fatal(err)
		}
		r.ClosedAt = w.clk.Now()
		if err := st.Save(w.ctx, r); err != nil {
			t.Fatal(err)
		}
		if err := st.Audit(w.ctx, chain.AuditEntry{Request: r.ID, At: w.clk.Now(), Approver: "mm", Level: chain.LevelMesa, Answer: chain.AnswerApprove, Outcome: "accepted", Reason: "ok"}); err != nil {
			t.Fatal(err)
		}
		w.clk.Advance(time.Minute)
	}
	w.clk.Advance(7 * time.Hour) // past the next 18:00
	if err := svc.Tick(w.ctx); err != nil {
		t.Fatal(err)
	}
	w.drain()
	msg, keys := w.buttons(ownerTG)
	if keys["Aceitar"] == "" || keys["Recusar"] == "" || keys["Ver exemplos"] == "" {
		t.Fatalf("buttons = %v (%q)", keys, lastText(w, ownerTG))
	}
	if !strings.Contains(lastText(w, ownerTG), "npm install *") {
		t.Fatalf("the suggestion says %q", lastText(w, ownerTG))
	}
	w.click(ownerTG, msg, keys["Ver exemplos"])
	if !strings.Contains(lastText(w, ownerTG), "Libera a mais") {
		t.Fatalf("Ver exemplos says %q", lastText(w, ownerTG))
	}
	w.click(ownerTG, msg, keys["Aceitar"])
	_, keys2 := w.buttons(ownerTG)
	if keys2["Confirmar"] == "" {
		t.Fatalf("Aceitar offered nothing to confirm: %v (%q)", keys2, lastText(w, ownerTG))
	}
	if w.count("decrees") != 0 {
		t.Fatal("a decree exists before the PIN")
	}
	_ = owner
}
