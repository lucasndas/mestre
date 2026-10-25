//go:build full

package app

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/gitx"
	"github.com/lucasndas/mestre/internal/guild"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/workspaces"
)

// CA-CLI-GH-05: a GitHub that refuses the sending of the branch for the lack of
// the permission "Workflows": `mestre pr update`, through the handlers the
// standard integration builds, is exit 3 workflow_permission, the sending is
// tried once, and the event reaches the inbox of the Grand Master (and of nobody
// else).
func TestPRUpdateRefusedForTheWorkflowsPermissionReachesTheInboxOfTheGrandMaster(t *testing.T) {
	t.Parallel()
	f := newFencedGitHub(t, false)
	c := f.ghCompose
	f.state(guild.FenceConfirmed)
	mirrorDir := workspaces.Layout{Root: filepath.Join(c.root, "workspaces")}.Mirror(c.ws.Slug)
	if _, err := c.remote.Init(c.ctx, mirrorDir, gitx.InitOptions{}); err != nil {
		t.Fatal(err)
	}
	mirror := c.remote.Handle(mirrorDir).Data()
	mirror.Commit("main", "chore: initial commit", map[string]string{"README.md": "# Meu App\n"})
	if err := mirror.Branch(c.table.Branch, "main"); err != nil {
		t.Fatal(err)
	}
	mirror.Commit(c.table.Branch, "feat: login", map[string]string{"src/login.ts": "export const timeout = 30\n"})

	c.remote.FailNextPush(gitx.ErrPushWorkflow)
	resp := f.fwd.Forward(c.ctx, &localchan.Call{
		Request: localchan.Request{V: 1, ID: "req-pr-update", AgentID: c.master.ID, Cmd: "pr update", Flags: map[string]any{}},
		Caller:  localchan.Caller{Identified: true, AgentID: c.master.ID, TerminalID: "t-master"},
	})
	if resp.OK || resp.ExitCode() != 3 || resp.Error == nil || resp.Error.Code != "workflow_permission" {
		t.Fatalf("pr update: %+v, want exit 3 workflow_permission", resp)
	}
	if pushes := c.remote.Pushes(); len(pushes) != 1 {
		t.Errorf("the sending was tried %d times, want once: %q", len(pushes), pushes)
	}
	gm, _, err := c.orc.Registry.GrandMaster(c.ctx, c.ws.Slug)
	if err != nil {
		t.Fatal(err)
	}
	told := func(id string) []string {
		items, err := c.orc.Inbox.Store().All(c.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range items {
			if it.Kind == inbox.KindSystem && strings.Contains(it.Text, "workflow_permission") {
				out = append(out, it.Text)
			}
		}
		return out
	}
	if got := told(gm.ID); len(got) != 1 || !strings.Contains(got[0], c.master.Table) {
		t.Errorf("the Grand Master got %q, want one system item with the code and the table", got)
	}
	if other := told(c.master.ID); len(other) != 0 {
		t.Errorf("the table master got the word meant for the Grand Master: %q", other)
	}
}
