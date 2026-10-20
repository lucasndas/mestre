package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// keepAttachment puts an attachment in the main clone of the workspace, where
// the channel keeps it.
func (w *commWorld) keepAttachment(rel, content string) {
	w.t.Helper()
	p := filepath.Join(w.dir, w.slug, "principal", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		w.t.Fatal(err)
	}
}

// tableDir is the worktree of the table of the world on the Citadel, made as a
// folder (the repository of the world is a double).
func (w *commWorld) tableDir() string {
	w.t.Helper()
	wts, err := w.orc.Tables.Store().Worktrees(w.ctx, w.slug, w.table.ID)
	if err != nil || len(wts) == 0 {
		w.t.Fatalf("the worktrees of the table: %v, %v", wts, err)
	}
	if err := os.MkdirAll(wts[0].Dir, 0o755); err != nil {
		w.t.Fatal(err)
	}
	return wts[0].Dir
}

// userMessage puts a message of the Owner with attachments in the inbox of the
// Grand Master.
func (w *commWorld) userMessage(text string, attachments ...string) inbox.Item {
	w.t.Helper()
	it, err := w.orc.Inbox.Enqueue(w.ctx, inbox.NewItem{Workspace: w.slug, Target: w.gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: text,
		Attachments: attachments, Origin: &inbox.Origin{Channel: "telegram", User: "u1", UserName: "Lucas", Level: characters.LevelOwner}})
	if err != nil {
		w.t.Fatal(err)
	}
	return it
}

// CA-CLI-COM-09: a message of the inbox is passed on whole, and its attachment
// is copied into the worktree of the table of the target: the target gets the
// original text and the path of the copy there.
func TestAskFromMessagePassesTheMessageOnWithItsAttachments(t *testing.T) {
	t.Parallel()
	w := newCommWorld(t)
	text := "Corrija o login, por favor!\n\nO print do erro está anexo."
	const rel = ".mestre/anexos/2026/10/a1b2-print.png"
	w.keepAttachment(rel, "PNG")
	it := w.userMessage(text, rel)
	dir := w.tableDir()
	r := w.run(w.gm, "ask", "Mestre", "--table", w.table.ID, "--from-message", it.ID, "--no-wait", "--json")
	if v := decodeJSON(t, r); r.code != 0 || v.Data["status"] != "sent" {
		t.Fatalf("ask --from-message = %+v", r)
	}
	got := w.mach.Delivered(w.get(w.master))
	if len(got) == 0 {
		t.Fatal("the table master got nothing")
	}
	last := got[len(got)-1]
	if !strings.Contains(last, text) || !strings.Contains(last, "[anexo] "+rel) {
		t.Errorf("the table master got:\n%s", last)
	}
	if b, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rel))); err != nil || string(b) != "PNG" {
		t.Errorf("the copy in the worktree of the table: %q, %v", b, err)
	}
	// With a prompt, it goes before the message, as a note.
	r = w.run(w.gm, "ask", "Mestre", "veja isto antes", "--table", w.table.ID, "--from-message", it.ID, "--no-wait")
	if r.code != 0 {
		t.Fatal(r)
	}
	w.turnEnds(w.master, "")
	got = w.mach.Delivered(w.get(w.master))
	if last := got[len(got)-1]; !strings.HasPrefix(last, "veja isto antes") || !strings.Contains(last, text) {
		t.Errorf("with a note:\n%s", last)
	}
	// A path that is not an attachment of the main clone is refused and nothing is copied.
	for _, bad := range []string{".mestre/anexos/../../../etc/passwd", "README.md", ".mestre/anexos/", ".mestre/anexos/a\\b"} {
		it := w.userMessage("x", bad)
		if r := w.run(w.gm, "ask", "Mestre", "--table", w.table.ID, "--from-message", it.ID, "--no-wait", "--json"); r.code == 0 {
			t.Errorf("the attachment %q = %+v", bad, r)
		}
	}
	// A message that is not in the inbox of the caller.
	if r := w.run(w.gm, "ask", "Mestre", "--table", w.table.ID, "--from-message", "m999", "--json"); r.code != 4 {
		t.Errorf("a message that is not there = %+v", r)
	}
}
