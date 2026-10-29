//go:build full

package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
)

// CA-CONV-01: an Administrator in the panel sends a question, the Grão-Mestre answers with
// `mestre reply`, and the answer is in his conversation (with the unread counter) and nothing is
// sent to the Telegram, though the user has one.
func TestTheAnswerOfReplyToAMessageOfThePanelComesBackToThePanelAndNotToTheTelegram(t *testing.T) {
	t.Parallel()
	w := newChatWorld(t)
	var mu sync.Mutex
	var heard []panel.ChatEvent
	stop := w.run.chat.Listen(func(e panel.ChatEvent) {
		mu.Lock()
		defer mu.Unlock()
		heard = append(heard, e)
	})
	defer stop()

	sent := w.say(w.bia, "msg-conv-0001", "como está a mesa do login?")
	// the message was typed in the terminal of the Grão-Mestre, as a message of the panel
	typed := w.machines.Delivered(w.gm)
	if len(typed) != 1 || typed[0] != "como está a mesa do login?" {
		t.Fatalf("what the Grand Master got: %q", typed)
	}
	if it, err := (&inbox.Store{DB: w.db}).UserItem(w.ctx, w.ws.Slug, w.bia.ID, "m"+strings.TrimPrefix(sent.ID, "in-")); err != nil ||
		!strings.HasPrefix(it.Header(), "[Mestre · user · de Bia (Administrador) · via painel · idioma pt-BR · m") {
		t.Fatalf("the header of the message: %v %v", it.Header(), err)
	}
	toTelegram := len(w.chatTexts(biaChat))

	r := w.forward("reply", nil, "a mesa 1 está em revisão")
	if !r.OK {
		t.Fatalf("mestre reply: %+v", r.Error)
	}
	var data map[string]any
	if err := json.Unmarshal(r.Data, &data); err != nil {
		t.Fatal(err)
	}
	if data["channel"] != "panel" || data["delivery"] != "sent" {
		t.Fatalf("the reply went %v", data)
	}
	// nothing goes to the Telegram for a message of the panel
	if got := w.chatTexts(biaChat); len(got) != toTelegram {
		t.Fatalf("the Telegram got %q", got[toTelegram:])
	}
	c := w.conversation(w.bia)
	if len(c.Entries) != 2 || c.Entries[0].ID != sent.ID || c.Entries[1].From != "grand_master" || c.Entries[1].Channel != "panel" ||
		c.Entries[1].Text != "a mesa 1 está em revisão" || !c.Entries[1].Unread || c.Unread != 1 || c.Entries[1].Quote == nil || c.Entries[1].Quote.ID != sent.ID {
		t.Fatalf("the conversation: %+v", c)
	}
	mu.Lock()
	defer mu.Unlock()
	var changed int
	for _, e := range heard {
		if e.Kind == panel.EventChatChanged && e.User == w.bia.ID && e.Workspace == w.ws.Slug {
			changed++
		}
	}
	if changed < 2 { // the message, and then the answer
		t.Fatalf("the screens of the user heard %+v", heard)
	}
	// the Owner, who also acts in the workspace, sees none of it
	if o := w.conversation(w.owner); len(o.Entries) != 0 || o.Unread != 0 {
		t.Fatalf("another user sees %+v", o)
	}
}

// CA-CONV-05: a PDF sent through the conversation is saved as an attachment of the workspace with
// the origin "painel" and delivered to the Grão-Mestre with its path.
func TestAPdfSentThroughThePanelIsSavedAndDeliveredToTheGrandMasterWithItsPath(t *testing.T) {
	t.Parallel()
	w := newChatWorld(t)
	w.say(w.bia, "msg-conv-0002", "segue o contrato", [2]string{"contrato.pdf", "%PDF-1.4\n1 0 obj\n<< >>\nendobj\n"})
	var path, channel, mime string
	if err := w.db.Reader().QueryRowContext(w.ctx, `SELECT path, channel, mime_detected FROM attachments WHERE workspace = ?`, w.ws.Slug).Scan(&path, &channel, &mime); err != nil {
		t.Fatal(err)
	}
	if channel != "panel" || mime != "application/pdf" || !strings.HasPrefix(path, ".mestre/anexos/") || !strings.HasSuffix(path, "-contrato.pdf") {
		t.Fatalf("attachment %q %q %q", path, channel, mime)
	}
	if _, err := os.Stat(filepath.Join(w.orc.Workspaces.Layout().Principal(w.ws.Slug), filepath.FromSlash(path))); err != nil {
		t.Fatalf("the file is not in the main clone of the workspace: %v", err)
	}
	typed := w.machines.Delivered(w.gm)
	if len(typed) != 1 || !strings.Contains(typed[0], "segue o contrato") || !strings.Contains(typed[0], "[anexo] "+path+" · application/pdf · ") {
		t.Fatalf("what the Grand Master got: %q", typed)
	}
	items, err := (&inbox.Store{DB: w.db}).All(w.ctx, w.gm.ID)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(items[len(items)-1].Attachments); n != 1 {
		t.Fatalf("the item carries %d attachments", n)
	}
}
