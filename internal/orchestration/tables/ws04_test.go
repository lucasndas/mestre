package tables_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// CA-WS-04: with the real workspace paused, a message of a user and a message
// of an agent arrive for the Grand Master: only the one of the user is
// delivered; the other waits in the queue until the workspace is resumed.
func TestAPausedWorkspaceDeliversOnlyTheMessageOfAUserToTheGrandMaster(t *testing.T) {
	t.Parallel()
	w := newWorld(t, worldOpts{})
	ws := w.activate()
	if err := w.svc.PauseWorkspace(w.ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	if got, err := w.wss.Get(w.ctx, ws.Slug); err != nil || got.State != workspaces.StatePaused {
		t.Fatalf("workspace = %+v, %v; want paused", got.State, err)
	}
	before := len(w.deliv.typed())
	agent, err := w.inb.Enqueue(w.ctx, inbox.NewItem{Workspace: ws.Slug, Target: w.gm.ID, Kind: inbox.KindAgent, Sender: "Mestre da mesa", Text: "relato da mesa"})
	if err != nil {
		t.Fatal(err)
	}
	user, err := w.inb.Enqueue(w.ctx, inbox.NewItem{Workspace: ws.Slug, Target: w.gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: "como está?",
		Origin: &inbox.Origin{Channel: inbox.ChannelTelegram, User: "lucas", UserName: "Lucas", Level: characters.LevelOwner, Conversation: "chat", Message: "tg-1"}})
	if err != nil {
		t.Fatal(err)
	}
	typed := w.deliv.typed()[before:]
	if len(typed) != 1 || typed[0].ItemID != user.ID {
		t.Fatalf("typed while the workspace is paused = %+v; want only the message of the user", typed)
	}
	pending, err := w.inb.List(w.ctx, w.gm.ID)
	if err != nil || len(pending) != 1 || pending[0].ID != agent.ID || pending[0].State != inbox.Queued {
		t.Fatalf("queue = %+v, %v; want the agent's message waiting", pending, err)
	}
	// The Grand Master finished the turn; the workspace is resumed.
	if _, _, err := w.reg.Transition(w.ctx, w.gm.ID, characters.Idle, characters.Change{At: w.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := w.svc.ResumeWorkspace(w.ctx, ws.Slug); err != nil {
		t.Fatal(err)
	}
	typed = w.deliv.typed()[before:]
	if len(typed) != 2 || typed[1].ItemID != agent.ID {
		t.Fatalf("typed after the resume = %+v; want the agent's message second", typed)
	}
}
