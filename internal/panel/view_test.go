package panel_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// fakeView is the engine behind the workspace view.
type fakeView struct {
	mu      sync.Mutex
	reads   []string
	layouts [][]panel.CanvasItem
	paused  []string
}

func (f *fakeView) View(_ context.Context, ws string) (panel.ViewData, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reads = append(f.reads, ws)
	return panel.ViewData{
		GrandMaster: &panel.ViewGrandMaster{ID: "gm", Name: "Grão", State: "idle"},
		Tables:      []panel.TableBlock{{ID: "42-login", Epic: 42, State: "working", Branch: "epico/42-login"}},
	}, nil
}

func (f *fakeView) SaveLayout(_ context.Context, _ string, items []panel.CanvasItem) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.layouts = append(f.layouts, items)
	return nil
}

func (f *fakeView) PauseTable(_ context.Context, by users.User, ws, table, reason string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paused = append(f.paused, by.ID+" "+ws+"/"+table+" "+reason)
	return true, nil
}

// CA-VWS-02 (the part of the server): a workspace the user does not see is not found,
// and a preparing one has the session-zero steps and no table, whatever the engine holds.
func TestTheViewIsOfWorkspacesTheUserSeesAndAPreparingOneHasNoTable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	fv := &fakeView{}
	r.panel.SetWorkspaces(&fakeWorkspaces{})
	r.panel.SetWorkspaceView(fv)
	_, obs := r.member("Beto", "beto", users.Observer)

	r.wantErr(r.call("GET", "/api/workspaces/blog/view", nil, opts{cookie: obs}), 404, panel.CodeNotFound)
	v := decodeInto[panel.WorkspaceView](t, r.call("GET", "/api/workspaces/loja/view", nil, opts{cookie: obs}))
	if v.Workspace.Slug != "loja" || len(v.Workspace.Stages) != 1 || len(v.Tables) != 0 || v.GrandMaster != nil {
		t.Errorf("a preparing workspace shows %+v", v)
	}
	if v.CanPause || v.CanType {
		t.Errorf("an observer can pause=%v type=%v", v.CanPause, v.CanType)
	}
	if len(fv.reads) != 0 {
		t.Errorf("the engine was read for a preparing workspace: %v", fv.reads)
	}

	v = decodeInto[panel.WorkspaceView](t, r.call("GET", "/api/workspaces/blog/view", nil, opts{cookie: r.ownerSession()}))
	if len(v.Tables) != 1 || v.Tables[0].ID != "42-login" || v.GrandMaster == nil || !v.CanPause || !v.CanType {
		t.Errorf("the Owner sees %+v", v)
	}
}

// CA-VWS-08 (the part of the server): Pausar mesa is an Approver's, in the workspaces the
// Approver acts in, without PIN, and it is audited.
func TestPausingATableNeedsAnApproverWhoActsInTheWorkspace(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	fv := &fakeView{}
	r.panel.SetWorkspaces(&fakeWorkspaces{})
	r.wantErr(r.call("POST", "/api/workspaces/loja/tables/42-login/pause", nil, opts{cookie: r.ownerSession()}), 503, panel.CodeUnavailable)
	r.panel.SetWorkspaceView(fv)
	obsUser, obs := r.member("Beto", "beto", users.Observer)
	appUser, apr := r.member("Ana", "ana", users.Approver)

	r.wantErr(r.call("POST", "/api/workspaces/loja/tables/42-login/pause", nil, opts{cookie: obs}), 403, panel.CodeForbiddenLevel)
	r.wantErr(r.call("POST", "/api/workspaces/blog/tables/42-login/pause", nil, opts{cookie: apr}), 404, panel.CodeNotFound)
	if len(fv.paused) != 0 {
		t.Fatalf("paused by who must not: %v (%s)", fv.paused, obsUser.ID)
	}
	// the reason is required: none, blank or unknown fields are a bad request and nothing is paused
	for _, body := range []any{nil, panel.PauseTableRequest{}, panel.PauseTableRequest{Reason: " \n\t "}, map[string]string{"why": "x"}} {
		r.wantErr(r.call("POST", "/api/workspaces/loja/tables/42-login/pause", body, opts{cookie: apr}), 400, panel.CodeBadRequest)
	}
	if len(fv.paused) != 0 {
		t.Fatalf("paused without a reason: %v", fv.paused)
	}
	w := r.call("POST", "/api/workspaces/loja/tables/42-login/pause", panel.PauseTableRequest{Reason: "  refazendo\na mesma   tela  "}, opts{cookie: apr})
	if w.Code != 200 || !decodeInto[panel.PauseTableResponse](t, w).Changed {
		t.Fatalf("pause = %d %s", w.Code, w.Body)
	}
	if len(fv.paused) != 1 || fv.paused[0] != appUser.ID+" loja/42-login refazendo a mesma tela" {
		t.Errorf("paused = %v", fv.paused)
	}
	if r.audited("panel.workspace.table_paused") != 1 {
		t.Error("the pause is not in the audit")
	}
}

func TestTheLayoutOfTheViewKeepsOnlyTheGrandMasterAndTheTables(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	fv := &fakeView{}
	r.panel.SetWorkspaces(&fakeWorkspaces{})
	r.panel.SetWorkspaceView(fv)
	_, obs := r.member("Beto", "beto", users.Observer)
	put := func(ws string, items ...panel.CanvasItem) int {
		return r.call("PUT", "/api/workspaces/"+ws+"/view/layout", panel.ViewLayoutRequest{Items: items}, opts{cookie: obs}).Code
	}
	if c := put("loja", panel.CanvasItem{Key: "gm", X: 1, Y: 2}, panel.CanvasItem{Key: "table:42-login", X: 3, Y: 4}); c != 204 {
		t.Fatalf("a valid layout = %d", c)
	}
	for _, key := range []string{"character:x", "table:", "", strings.Repeat("a", 3)} {
		if c := put("loja", panel.CanvasItem{Key: key}); c != 400 {
			t.Errorf("key %q = %d, want 400", key, c)
		}
	}
	if c := put("blog", panel.CanvasItem{Key: "gm"}); c != 404 {
		t.Errorf("a workspace the user does not see = %d", c)
	}
	if len(fv.layouts) != 1 || len(fv.layouts[0]) != 2 {
		t.Errorf("saved %v", fv.layouts)
	}
}

// The last workspace opened is kept on the Citadel, and only one the user sees.
func TestTheLastWorkspaceIsKeptOnTheCitadelForWhoSeesIt(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, obs := r.member("Beto", "beto", users.Observer)
	r.wantErr(r.call("PUT", "/api/me/workspace", panel.LastWorkspaceRequest{Workspace: "blog"}, opts{cookie: obs}), 404, panel.CodeNotFound)
	r.wantErr(r.call("PUT", "/api/me/workspace", panel.LastWorkspaceRequest{}, opts{cookie: obs}), 404, panel.CodeNotFound)
	me := decodeInto[panel.Me](t, r.call("PUT", "/api/me/workspace", panel.LastWorkspaceRequest{Workspace: "loja"}, opts{cookie: obs}))
	if me.User.LastWorkspace != "loja" {
		t.Errorf("answer = %+v", me.User)
	}
	// a new session of the same user (another device) gets it back
	got := decodeInto[panel.Me](t, r.call("GET", "/api/me", nil, opts{cookie: obs}))
	if got.User.LastWorkspace != "loja" {
		t.Errorf("me = %q", got.User.LastWorkspace)
	}
}
