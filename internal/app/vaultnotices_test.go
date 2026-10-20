package app

import (
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/users"
	"github.com/lucasndas/mestre/internal/vault"
)

// chatsTold are the chats that got a message about a credential, with how many.
func chatsTold(t *testing.T, i *integ, name string) map[int64]int {
	t.Helper()
	rows, err := i.db.Reader().QueryContext(i.ctx, `SELECT chat_id, params FROM telegram_outbox ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[int64]int{}
	for rows.Next() {
		var chat int64
		var p string
		if err := rows.Scan(&chat, &p); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(p, name) {
			got[chat]++
		}
	}
	return got
}

// CA-COFRE-17
func TestTheOwnerOfACredentialAndTheOwnerAreToldOnceOfItsValidityByTheBot(t *testing.T) {
	t.Parallel()
	i := newInteg(t, true)
	run := i.open()
	defer run.Close()
	owner, err := run.users.CreateOwner(i.ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	pair := func(u users.User, id int64) {
		code, err := run.users.NewCode(i.ctx, owner, u.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := run.users.Pair(i.ctx, users.PairRequest{Text: code.Code, TelegramID: id, Private: true}); err != nil {
			t.Fatal(err)
		}
	}
	pair(owner, 77)
	ana, err := run.users.Create(i.ctx, owner, users.NewUser{Name: "Ana", Level: users.Administrator, Workspaces: []string{"loja"}})
	if err != nil {
		t.Fatal(err)
	}
	pair(ana, 88)
	bia, err := run.users.Create(i.ctx, owner, users.NewUser{Name: "Bia", Level: users.Administrator, Workspaces: []string{"loja"}})
	if err != nil {
		t.Fatal(err)
	}
	pair(bia, 99)
	if _, err := run.startBot(i.ctx); err != nil {
		t.Fatal(err)
	}

	// a credential that expires in 30 days (Ana's) and one with no validity (Bia's)
	const value = `{"url":"https://loja.example/login","user":"qa@x","password":"Abc123!senha-longa"}`
	t0 := i.clk.Now()
	expires := t0.Add(30 * 24 * time.Hour)
	put := func(name, who string, exp *time.Time) {
		t.Helper()
		if err := i.vault.Put(i.ctx, name, vault.TypeSiteCredential, []byte(value),
			&vault.SiteMeta{OwnerUser: who, Classes: []string{"qa"}, Domains: []string{name + ".example"}, Kind: vault.KindAppPassword, ExpiresAt: exp}); err != nil {
			t.Fatal(err)
		}
	}
	put("site_com_validade", ana.ID, &expires)
	put("site_sem_validade", bia.ID, nil)
	w := run.expiryWatcher()
	scan := func(want int) {
		t.Helper()
		if got, err := w.Scan(i.ctx); err != nil || got != want {
			t.Fatalf("Scan sent %d notices, %v; want %d", got, err, want)
		}
	}

	scan(0) // nothing is near
	i.clk.Advance(22*24*time.Hour + 23*time.Hour)
	scan(0) // 7 days and an hour left
	i.clk.Advance(time.Hour)
	scan(1) // 7 days left
	if got := chatsTold(t, i, "site_com_validade"); len(got) != 2 || got[88] != 1 || got[77] != 1 {
		t.Fatalf("told about the credential that expires: %v, want its owner (Ana) and the Owner, once each", got)
	}
	scan(0)
	i.clk.Advance(24 * time.Hour)
	scan(0)
	// a restart does not repeat it: the memory is in the database
	w = run.expiryWatcher()
	scan(0)

	// 90 days since the last replacement of the one with no validity
	i.clk.Advance(t0.Add(90 * 24 * time.Hour).Sub(i.clk.Now()))
	scan(1)
	if got := chatsTold(t, i, "site_sem_validade"); len(got) != 2 || got[99] != 1 || got[77] != 1 {
		t.Fatalf("told about the credential with no validity: %v, want its owner (Bia) and the Owner, once each", got)
	}
	scan(0)
	if got := chatsTold(t, i, "site_com_validade"); got[88] != 1 || got[77] != 1 || got[99] != 0 {
		t.Fatalf("the first notice was repeated or reached a bystander: %v", got)
	}
	for _, text := range []string{"Abc123", "senha-longa"} {
		if got := chatsTold(t, i, text); len(got) != 0 {
			t.Errorf("the value of the credential reached the chats: %v", got)
		}
	}

	// replaced, it starts over: a new expiry is told again when it comes near
	later := i.clk.Now().Add(10 * 24 * time.Hour)
	put("site_com_validade", ana.ID, &later)
	scan(0)
	i.clk.Advance(3 * 24 * time.Hour)
	scan(1)
	if got := chatsTold(t, i, "site_com_validade"); got[88] != 2 || got[77] != 2 {
		t.Fatalf("after the replacement: %v, want a second notice for each", got)
	}
}
