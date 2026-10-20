//go:build full

package app_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/connections"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
)

// CA-MESAS-01: the Grand Master creates the epic of the user with `gh epic
// create` and opens the table with `table open --epic`, over the real CLI and the
// double of GitHub, with the message of the user in its inbox. The branch comes
// from the main one, the worktree is on the Citadel, the table master is
// connected to the Grand Master, and its first message has the text of the user
// byte for byte.
func TestTheGrandMasterCreatesTheEpicWithGhEpicCreateAndOpensTheTableOfIt(t *testing.T) {
	t.Parallel()
	m := &mm{realEpics: true}
	w := newCommWorld(t, m.setup)
	original := "Quero um login com e-mail e senha.\n\n  Sem OAuth por enquanto — só e-mail.\r\nObrigado!\n"
	it, err := w.orc.Inbox.Enqueue(w.ctx, inbox.NewItem{Workspace: w.slug, Target: w.gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: original,
		Origin: &inbox.Origin{Channel: inbox.ChannelTelegram, User: "u1", UserName: "Lucas", Level: characters.LevelOwner}})
	if err != nil {
		t.Fatal(err)
	}

	created := decodeJSON(t, w.run(w.gm, "gh", "epic", "create", "--title", "Login de usuários", "--body", "Login com e-mail e senha.", "--json"))
	n, _ := created.Data["number"].(float64)
	if !created.OK || n == 0 {
		t.Fatalf("gh epic create = %+v", created)
	}
	epic := int(n)
	if _, _, _, labels, ok := epicOf(m, epic); !ok || !slices.Contains(labels, "epico") {
		t.Fatalf("the epic #%d on GitHub has the labels %v; want the epic one", epic, labels)
	}

	r := w.run(w.gm, "table", "open", "--epic", fmt.Sprint(epic), "--from-message", it.ID, "--json")
	opened := decodeJSON(t, r)
	if r.code != 0 || !opened.OK {
		t.Fatalf("table open = %+v", r)
	}
	branch := fmt.Sprintf("epico/%d-login-de-usuarios", epic)
	if opened.Data["branch"] != branch {
		t.Fatalf("the branch of the table is %v; want %s", opened.Data["branch"], branch)
	}
	tableID := opened.Data["name"].(string)

	// the branch comes from the main one
	mirror, err := w.orc.Workspaces.OpenMirror(w.ctx, w.slug)
	if err != nil {
		t.Fatal(err)
	}
	tip, err := mirror.ResolveRef(w.ctx, "refs/heads/"+branch)
	if err != nil {
		t.Fatalf("the branch %s is not in the mirror: %v", branch, err)
	}
	if main, err := mirror.ResolveRef(w.ctx, "refs/remotes/origin/main"); err != nil || tip != main {
		t.Errorf("the branch is at %s; want the main one, %s (%v)", tip, main, err)
	}
	// the worktree on the Citadel
	rec, err := w.orc.Tables.Store().Worktree(w.ctx, w.slug, tableID, "cidadela")
	if err != nil || rec.Dir == "" {
		t.Fatalf("the worktree record = %+v, %v", rec, err)
	}
	if wts, _ := mirror.ListWorktrees(w.ctx); !slices.ContainsFunc(wts, func(x gitx.Worktree) bool { return x.Path == rec.Dir && x.Branch == branch }) {
		t.Errorf("the mirror has no worktree of %s at %s: %+v", branch, rec.Dir, wts)
	}
	// the table master, connected to the Grand Master
	master, ok, err := w.orc.Registry.TableMaster(w.ctx, w.slug, tableID)
	if err != nil || !ok {
		t.Fatalf("the table master: %v, %v", ok, err)
	}
	if c, ok, err := w.orc.Connections.Store().Between(w.ctx, connections.Char(master.ID), connections.Char(w.gm.ID)); err != nil || !ok || !c.Has(connections.ReasonMaster) {
		t.Errorf("connection of the table master to the Grand Master = %+v, %v, %v", c, ok, err)
	}
	// its first message has the text of the user, byte for byte
	w.mach.Report(w.ctx, master.ID, characters.Idle)
	got := w.mach.Delivered(master)
	if len(got) == 0 || !strings.Contains(got[0], original) {
		t.Fatalf("the first message of the table master = %q; want the text of the user, byte for byte: %q", got, original)
	}
}

// epicOf reads an issue of the double of GitHub.
func epicOf(m *mm, n int) (title, body, state string, labels []string, ok bool) {
	d, err := (mmEpics{m}).api().IssueData(m.w.ctx, mmRepo, n)
	if err != nil {
		return "", "", "", nil, false
	}
	return d.Title, d.Body, d.State, d.Labels, true
}
