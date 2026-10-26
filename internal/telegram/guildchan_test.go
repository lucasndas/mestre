package telegram_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/guild"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/users"
)

// guildFake is the Guild behind /guilda: it records the calls of SetExcluded and
// refuses the Citadel as the real one does.
type guildFake struct {
	mu       sync.Mutex
	machines []telegram.GuildMachine
	pending  []telegram.GuildPending
	calls    []string
}

func (g *guildFake) waiting(context.Context) ([]telegram.GuildPending, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]telegram.GuildPending(nil), g.pending...), nil
}

func (g *guildFake) list(context.Context) ([]telegram.GuildMachine, uint64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]telegram.GuildMachine(nil), g.machines...), 3, nil
}

func (g *guildFake) set(_ context.Context, id string, excluded bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i, m := range g.machines {
		if m.ID != id {
			continue
		}
		if m.Citadel {
			return guild.ErrCitadelProtected
		}
		g.machines[i].Excluded = excluded
		g.calls = append(g.calls, map[bool]string{true: "exclude ", false: "undo "}[excluded]+id)
		return nil
	}
	return errors.New("no machine")
}

func (g *guildFake) recorded() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.calls...)
}

func (g *guildFake) swapCitadel() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range g.machines {
		g.machines[i].Citadel = !g.machines[i].Citadel
	}
}

// Part of CA-GUILDA-12, the Owner's side: /guilda lists the machines, the Owner taps
// Excluir and types the PIN, and only then SetExcluded is called; the Citadel has
// no button; a wrong PIN calls nothing; Desfazer exclusão calls it again. What
// the Tower does with SetExcluded (erases the vault, keeps certificates without
// mestre.local, refuses the promotion, gets the vault back) is in
// internal/guild/registry_test.go and, composed with this button and PIN over
// the real engines, in internal/app/towerexclusion_full_test.go (full suite).
func TestGuildExclusionNeedsTheOwnersPINAndTheUndoCallsAgain(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{guild: true})
	if err := w.users.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	owner := w.pairOwnerTelegram()
	w.say(owner, "/guilda")
	w.drain()
	text := strings.Join(texts(w, owner.ID), "\n")
	if !strings.Contains(text, "época 3") || !strings.Contains(text, "casa · Cidadela") || !strings.Contains(text, "notebook · Torre") {
		t.Fatalf("the listing = %q", text)
	}
	msg, keys := w.buttons(owner)
	if len(keys) != 1 || keys["Excluir notebook"] == "" {
		t.Fatalf("buttons = %v, want only Excluir of the Tower (never of the Citadel)", keys)
	}

	// a wrong PIN does nothing; three of them end the request
	w.click(owner, msg, keys["Excluir notebook"])
	for range 3 {
		w.say(owner, "000000")
		w.drain()
	}
	if got := w.gf.recorded(); len(got) != 0 {
		t.Fatalf("a wrong PIN called SetExcluded: %v", got)
	}

	// the tap alone does nothing either; the right PIN excludes (a button is
	// used once: the listing is asked again)
	w.say(owner, "/guilda")
	w.drain()
	msg, keys = w.buttons(owner)
	w.click(owner, msg, keys["Excluir notebook"])
	if got := w.gf.recorded(); len(got) != 0 {
		t.Fatalf("the tap called SetExcluded before the PIN: %v", got)
	}
	w.say(owner, "123456")
	w.drain()
	if got := w.gf.recorded(); len(got) != 1 || got[0] != "exclude m-b" {
		t.Fatalf("calls = %v", got)
	}

	// the listing now offers Desfazer, and its PIN undoes
	w.say(owner, "/guilda")
	w.drain()
	text = strings.Join(texts(w, owner.ID), "\n")
	if !strings.Contains(text, "notebook · Torre · excluída") {
		t.Errorf("the listing does not show the exclusion: %q", text)
	}
	msg, keys = w.buttons(owner)
	if len(keys) != 1 || keys["Desfazer exclusão de notebook"] == "" {
		t.Fatalf("buttons = %v", keys)
	}
	w.click(owner, msg, keys["Desfazer exclusão de notebook"])
	w.say(owner, "123456")
	w.drain()
	if got := w.gf.recorded(); len(got) != 2 || got[1] != "undo m-b" {
		t.Fatalf("calls = %v", got)
	}

	// the PIN never stayed in the chat
	for _, s := range w.fake.Sent() {
		if strings.Contains(s.Text, "123456") || strings.Contains(s.Text, "000000") {
			t.Errorf("a PIN went out: %q", s.Text)
		}
	}
}

// Also of CA-GUILDA-12 (no marker, one is enough): who is not the Owner sees the listing without buttons,
// and the Citadel is never excluded, even when it moved between the listing and
// the PIN.
func TestGuildListingHasNoButtonsForANonOwnerAndTheCitadelIsRefused(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{guild: true})
	_, adm := w.pinned("Ana", users.Administrator)
	w.say(adm, "/guilda")
	w.drain()
	if got := strings.Join(texts(w, adm.ID), "\n"); !strings.Contains(got, "notebook · Torre") {
		t.Fatalf("the Administrator got %q", got)
	}
	if _, keys := w.buttons(adm); len(keys) != 0 {
		t.Errorf("a non-Owner got buttons: %v", keys)
	}

	if err := w.users.SetPIN(w.ctx, w.owner.ID, "123456"); err != nil {
		t.Fatal(err)
	}
	owner := w.pairOwnerTelegram()
	w.say(owner, "/guilda")
	w.drain()
	msg, keys := w.buttons(owner)
	if keys["Excluir notebook"] == "" {
		t.Fatalf("buttons = %v", keys)
	}
	w.gf.swapCitadel() // the Citadel moved to the notebook after the listing
	w.click(owner, msg, keys["Excluir notebook"])
	w.say(owner, "123456")
	w.drain()
	if got := w.gf.recorded(); len(got) != 0 {
		t.Fatalf("calls = %v", got)
	}
	if got := strings.Join(texts(w, owner.ID), "\n"); !strings.Contains(got, "Não mudei a exclusão") {
		t.Errorf("the Owner was not told: %q", got)
	}
}

// /guilda shows online or offline of each machine; the pending entry and
// promotion requests only to the Owner; and a hostile machine name comes out
// without the marks the Telegram turns into links or bold.
func TestGuildListingShowsOnlineAndThePendingRequestsOnlyToTheOwner(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{guild: true})
	w.gf.machines = []telegram.GuildMachine{
		{ID: "m-a", Name: "casa", Citadel: true, Online: true},
		{ID: "m-b", Name: "notebook"},
		{ID: "m-c", Name: "[clique](http://x.io)*", Online: true},
	}
	w.gf.pending = []telegram.GuildPending{
		{Kind: telegram.PendingEntry, Name: "[x](http://evil.io) _novo_", Code: "482 913", At: w.clk.Now()},
		{Kind: telegram.PendingPromotion, Name: "notebook", At: w.clk.Now()},
	}
	owner := w.pairOwnerTelegram()
	w.say(owner, "/guilda")
	w.drain()
	text := strings.Join(texts(w, owner.ID), "\n")
	for _, want := range []string{"casa · Cidadela · online", "notebook · Torre · offline", "cliquehttp://x.io · Torre · online", "Pedido de entrada: xhttp://evil.io novo, desde 19/10 12:00, código 482 913", "Pedido de promoção a Cidadela: notebook, desde 19/10 12:00"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the Owner's listing lacks %q:\n%s", want, text)
		}
	}
	if strings.ContainsAny(text, "[]*_`") || strings.Contains(text, "](") {
		t.Fatalf("the listing keeps marks of the Telegram:\n%s", text)
	}

	_, adm := w.user("Ana", users.Administrator)
	w.say(adm, "/guilda")
	w.drain()
	got := strings.Join(texts(w, adm.ID), "\n")
	if !strings.Contains(got, "notebook · Torre · offline") || strings.Contains(got, "Pedido") || strings.Contains(got, "482") {
		t.Fatalf("the Administrator's listing = %q", got)
	}
}
