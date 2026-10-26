package telegram_test

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/users"
)

// A workspace still being prepared has no Grand Master: it is not offered nor routed to.
func TestAWorkspaceBeingPreparedIsNotOffered(t *testing.T) {
	t.Parallel()
	w := routingWorld(t)
	if _, err := w.db.Writer().ExecContext(w.ctx, `UPDATE workspaces SET state = 'preparing' WHERE slug = 'shop'`); err != nil {
		t.Fatal(err)
	}
	_, tg := w.user("Ana", users.Administrator, "loja", "shop")
	w.say(tg, "#shop oi")
	w.drain()
	if !strings.Contains(lastText(w, tg), "Você não atua em #shop") || len(w.delivered.all()) != 0 {
		t.Fatalf("%q", lastText(w, tg))
	}
}

// A parked message keeps what it replied to, and the item of the inbox has a key
// so a second delivery of the same message makes no second item.
func TestAParkedMessageKeepsItsReplyAndTheItemIsIdempotent(t *testing.T) {
	t.Parallel()
	w := routingWorld(t)
	_, tg := w.user("Ana", users.Administrator, "loja", "blog")
	w.reply(tg, "sobre aquilo", 4242) // a message the bot does not remember
	msg, keys := w.buttons(tg)
	w.click(tg, msg, keys["blog"])
	got := w.delivered.all()
	if len(got) != 1 || got[0].ReplyToMessage != 4242 {
		t.Fatalf("%+v", got)
	}
	a, b := telegram.NewItem(got[0], "gm"), telegram.NewItem(got[0], "gm")
	if a.Refs.Key == "" || a.Refs.Key != b.Refs.Key {
		t.Fatalf("keys %q %q", a.Refs.Key, b.Refs.Key)
	}
}
