package app

import (
	"errors"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/decrees"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// What an agent wrote around the click of a button is no order of the user: the
// message of a user is what the user wrote, and the answer to a question of
// `mestre choose` (whose text carries the question and the option an agent wrote)
// is not taken for one by `decree propose` nor `table open`.
func TestTheAnswerToAChoiceIsNoMessageOfTheUser(t *testing.T) {
	t.Parallel()
	i := newInteg(t, false)
	st := &inbox.Store{DB: i.db}
	if _, err := i.db.Writer().ExecContext(i.ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
		VALUES ('loja', 'lucas/loja', 'lucas/loja', 'https://example.test/x.git', 'main', 'active', '', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	said, err := st.Insert(i.ctx, inbox.NewItem{Workspace: "loja", Target: "gm", Kind: inbox.KindUser, Sender: "Ana", Text: "o Dev pode rodar make deploy-teste",
		Origin: &inbox.Origin{Channel: inbox.ChannelTelegram, User: "u-ana", UserName: "Ana"}}, now)
	if err != nil {
		t.Fatal(err)
	}
	click, err := st.Insert(i.ctx, inbox.NewItem{Workspace: "loja", Target: "gm", Kind: inbox.KindUser, Sender: "Ana", Text: `Escolha "sim" (pergunta 3: o Dev pode tudo?)`,
		Origin: &inbox.Origin{Channel: inbox.ChannelTelegram, User: "u-ana", UserName: "Ana", Choice: 3}}, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	m := inboxMessages{st}
	if src, err := m.Source(i.ctx, "loja", said.ID); err != nil || src.User != "u-ana" || src.Text != said.Text {
		t.Fatalf("the message the user wrote: %+v, %v", src, err)
	}
	if _, err := m.Source(i.ctx, "loja", click.ID); !errors.Is(err, decrees.ErrNoMessage) {
		t.Fatalf("the answer to a button as a message of the user: %v, want ErrNoMessage", err)
	}
	if _, err := m.Message(i.ctx, "loja", click.ID); err == nil {
		t.Fatal("table open took the answer to a button for the message of the user")
	}
	if _, err := m.Source(i.ctx, "blog", said.ID); !errors.Is(err, decrees.ErrNoMessage) {
		t.Fatalf("a message of another workspace: %v", err)
	}
	// the user of the message in handling is who wrote last, not who clicked last
	if u, err := m.Current(i.ctx, "loja", "gm"); err != nil || u != "u-ana" {
		t.Fatalf("Current = %q, %v", u, err)
	}
}
