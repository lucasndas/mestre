package telegram_test

import (
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/blocking"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/users"
)

// CA-CLI-GM-15: the question goes to the user as buttons, the recommended option
// first; only the click of the user asked counts, once; the answer comes as a
// message of the user who clicked with the option and the number of the question.
func TestChooseAsksWithTheRecommendedOptionFirstAndTheAnswerComesOnceAsAMessageOfTheUser(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true})
	ana, anaTG := w.user("Ana", users.Administrator, "loja")
	options := []string{"Mesa #12 Login", "Nova mesa", "Mesa #15 Relatórios"}
	id, err := w.cc.Ask(w.ctx, telegram.ChoiceQuestion{Workspace: "loja", Asker: "gm", UserID: ana.ID, Question: "Esse ajuste é do Login?", Options: options}, 3)
	if err != nil {
		t.Fatal(err)
	}
	w.drain()
	msg, keys := w.buttons(anaTG)
	if len(keys) != 3 || keys["Mesa #15 Relatórios"] == "" {
		t.Fatalf("buttons = %v, want the three options", keys)
	}
	// the recommended one comes first
	var first string
	for _, s := range w.sent("sendMessage") {
		if s.ChatID == anaTG.ID {
			kb := s.Params["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
			first = kb[0].([]any)[0].(map[string]any)["text"].(string)
		}
	}
	if first != "Mesa #15 Relatórios" {
		t.Fatalf("the first button is %q, want the recommended one", first)
	}
	if len(w.answers) != 0 {
		t.Fatal("something was passed on before the answer")
	}
	// the click of another user, even of the same workspace, counts for nothing
	_, biaTG := w.user("Bia", users.Administrator, "loja")
	w.click(biaTG, msg, keys["Nova mesa"])
	if len(w.answers) != 0 {
		t.Fatalf("the click of another user was taken as the answer: %d answers", len(w.answers))
	}
	w.click(anaTG, msg, keys["Nova mesa"])
	if len(w.answers) != 1 {
		t.Fatalf("%d answers, want one", len(w.answers))
	}
	a := w.answers[0]
	if a.Kind != inbox.KindUser || a.Origin == nil || a.Origin.User != ana.ID || !strings.Contains(a.Text, "Nova mesa") || !strings.Contains(a.Text, "pergunta "+itoa64(id)) {
		t.Fatalf("answer = %+v", a)
	}
	// a second click does nothing
	w.click(anaTG, msg, keys["Mesa #12 Login"])
	if len(w.answers) != 1 {
		t.Fatalf("a question answered twice: %d answers", len(w.answers))
	}
}

// CA-CLI-GM-15, the order of the buttons with --recommend 1 (the handler gives
// the payload "|1|", and the recommended option is the first one of the list):
// it comes first and the others keep their order.
func TestChooseWithRecommendationOneKeepsTheListOrder(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true})
	ana, anaTG := w.user("Ana", users.Administrator, "loja")
	options := []string{"Mesa #12 Login", "Nova mesa", "Mesa #15 Relatórios"}
	if _, err := w.cc.Ask(w.ctx, telegram.ChoiceQuestion{Workspace: "loja", Asker: "gm", UserID: ana.ID, Question: "Esse ajuste é do Login?", Options: options}, 1); err != nil {
		t.Fatal(err)
	}
	w.drain()
	var texts []string
	for _, s := range w.sent("sendMessage") {
		if s.ChatID != anaTG.ID {
			continue
		}
		for _, row := range s.Params["reply_markup"].(map[string]any)["inline_keyboard"].([]any) {
			for _, b := range row.([]any) {
				texts = append(texts, b.(map[string]any)["text"].(string))
			}
		}
	}
	if !slices.Equal(texts, options) {
		t.Fatalf("buttons = %v; want %v (#12 first, the recommended one, then the others in order)", texts, options)
	}
}

func TestChooseRefusesAUserWithoutLevelOrWithoutTelegramAndOptionsOutOfRange(t *testing.T) {
	t.Parallel()
	w := newWorld(t, rigOpts{block: true})
	obs, _ := w.user("Olga", users.Observer, "loja")
	if _, err := w.cc.Ask(w.ctx, telegram.ChoiceQuestion{Workspace: "loja", UserID: obs.ID, Question: "x?", Options: []string{"a", "b"}}, 1); !errors.Is(err, blocking.ErrNoLevel) {
		t.Fatalf("an observer: %v, want ErrNoLevel", err)
	}
	unpaired, err := w.users.Create(w.ctx, w.owner, users.NewUser{Name: "Caio", Level: users.Administrator, Workspaces: []string{"loja"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.cc.Ask(w.ctx, telegram.ChoiceQuestion{Workspace: "loja", UserID: unpaired.ID, Question: "x?", Options: []string{"a", "b"}}, 1); !errors.Is(err, blocking.ErrNotReachable) {
		t.Fatalf("a user without Telegram: %v, want ErrNotReachable", err)
	}
	ana, _ := w.user("Ana", users.Administrator, "loja")
	for _, n := range []int{1, 7} {
		opts := make([]string, n)
		for i := range opts {
			opts[i] = string(rune('a' + i))
		}
		if _, err := w.cc.Ask(w.ctx, telegram.ChoiceQuestion{Workspace: "loja", UserID: ana.ID, Question: "x?", Options: opts}, 1); err == nil {
			t.Fatalf("%d options accepted", n)
		}
	}
	other, _ := w.user("Bia", users.Administrator, "blog")
	if _, err := w.cc.Ask(w.ctx, telegram.ChoiceQuestion{Workspace: "loja", UserID: other.ID, Question: "x?", Options: []string{"a", "b"}}, 1); !errors.Is(err, blocking.ErrNotReachable) {
		t.Fatalf("a user of another workspace: %v, want ErrNotReachable", err)
	}
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }
