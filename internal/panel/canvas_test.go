package panel_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

// fakeCanvas is the engine behind the canvas: it records what the panel asks and
// refuses what the test says.
type fakeCanvas struct {
	mu         sync.Mutex
	connects   [][2]string
	disconnect error
	connect    error
	layouts    []panel.LayoutRequest
}

func (f *fakeCanvas) State(_ context.Context, ws, table string) (panel.CanvasState, error) {
	if table != "m1" {
		return panel.CanvasState{}, panel.ErrCanvasNotFound
	}
	return panel.CanvasState{Workspace: ws, Table: table, TableState: "working", Characters: []panel.CanvasCharacter{{ID: "c1", Name: "Dev-1"}}}, nil
}

func (f *fakeCanvas) SaveLayout(_ context.Context, _, _ string, req panel.LayoutRequest) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.layouts = append(f.layouts, req)
	return nil
}

func (f *fakeCanvas) Connect(_ context.Context, _ users.User, _, _ string, a, b panel.CanvasRef) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connects = append(f.connects, [2]string{a.ID, b.ID})
	return f.connect
}

func (f *fakeCanvas) Disconnect(context.Context, users.User, string, string, panel.CanvasRef, panel.CanvasRef) error {
	return f.disconnect
}

func (*fakeCanvas) Pin(context.Context, users.User, string, string, string, bool) error { return nil }
func (*fakeCanvas) WriteScroll(context.Context, users.User, string, string, string, panel.ScrollWriteRequest) (panel.CanvasScroll, error) {
	return panel.CanvasScroll{}, panel.ErrCanvasConflict
}
func (*fakeCanvas) CreateQuest(context.Context, users.User, string, string, panel.QuestRequest) (panel.CanvasQuest, error) {
	return panel.CanvasQuest{ID: "q1"}, nil
}
func (*fakeCanvas) EditQuest(context.Context, users.User, string, string, string, panel.QuestRequest) (panel.CanvasQuest, error) {
	return panel.CanvasQuest{ID: "q1"}, nil
}
func (*fakeCanvas) Grimoires(context.Context, users.User) ([]panel.GrimoireInfo, error) {
	return nil, nil
}
func (*fakeCanvas) SaveGrimoire(context.Context, users.User, string, string, panel.GrimoireSaveRequest) (panel.GrimoireInfo, error) {
	return panel.GrimoireInfo{}, nil
}
func (*fakeCanvas) RestoreGrimoire(context.Context, users.User, string, string, panel.GrimoireRestoreRequest) (panel.GrimoirePlan, error) {
	return panel.GrimoirePlan{}, nil
}
func (*fakeCanvas) Rituals(context.Context, string, string) ([]panel.RitualInfo, error) {
	return nil, nil
}
func (*fakeCanvas) EditRitual(context.Context, users.User, string, string, string, panel.RitualEditRequest) (panel.RitualInfo, error) {
	return panel.RitualInfo{}, nil
}

const canvasPath = "/api/workspaces/loja/tables/m1/canvas"

var link = panel.ConnectRequest{A: panel.CanvasRef{Kind: "character", ID: "c1"}, B: panel.CanvasRef{Kind: "character", ID: "c2"}}

func TestTheCanvasReadsForAnObserverAndActsOnlyForAnAdministrator(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &fakeCanvas{}
	r.panel.SetCanvas(f)
	_, obs := r.member("Beto", "beto", users.Observer)
	_, adm := r.member("Ana", "ana", users.Administrator)

	w := r.call("GET", canvasPath, nil, opts{cookie: obs})
	if w.Code != 200 {
		t.Fatalf("read: %d %s", w.Code, w.Body)
	}
	if st := decodeInto[panel.CanvasState](t, w); st.CanEdit || len(st.Characters) != 1 {
		t.Errorf("an observer's state = %+v", st)
	}
	if st := decodeInto[panel.CanvasState](t, r.call("GET", canvasPath, nil, opts{cookie: adm})); !st.CanEdit {
		t.Error("an administrator cannot edit")
	}
	// the server decides: the observer is refused whatever the buttons said
	r.wantErr(r.call("POST", canvasPath+"/connections", link, opts{cookie: obs}), 403, panel.CodeForbiddenLevel)
	r.wantErr(r.call("PUT", canvasPath+"/characters/c1/pin", panel.PinRequest{Pinned: true}, opts{cookie: obs}), 403, panel.CodeForbiddenLevel)
	if len(f.connects) != 0 {
		t.Errorf("the engine was asked %v by an observer", f.connects)
	}
	if w := r.call("POST", canvasPath+"/connections", link, opts{cookie: adm}); w.Code != 204 {
		t.Fatalf("connect: %d %s", w.Code, w.Body)
	}
	if r.audited("panel.canvas_connect") != 1 {
		t.Error("the connection was not audited")
	}
	// another workspace is not seen; an unknown table is not found
	r.wantErr(r.call("GET", "/api/workspaces/blog/tables/m1/canvas", nil, opts{cookie: adm}), 404, panel.CodeNotFound)
	r.wantErr(r.call("GET", "/api/workspaces/loja/tables/zzz/canvas", nil, opts{cookie: adm}), 404, panel.CodeNotFound)
}

// CA-CANVAS-05 and CA-CANVAS-10, the part of the server: what the engine refuses
// is answered with the reason it gave, for the canvas to undo the gesture and show it.
func TestTheCanvasAnswersARefusalWithItsReason(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &fakeCanvas{
		connect:    &panel.Refusal{Reason: "different_tables", Message: "os itens estão em mesas diferentes"},
		disconnect: &panel.Refusal{Reason: "automatic_connection", Message: "a conexão é automática", Params: map[string]any{"reasons": []string{"mestre"}}},
	}
	r.panel.SetCanvas(f)
	_, adm := r.member("Ana", "ana", users.Administrator)

	w := r.call("POST", canvasPath+"/connections", link, opts{cookie: adm})
	r.wantErr(w, 409, panel.CodeRefused)
	var b panel.ErrorBody
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if b.Error.Params["reason"] != "different_tables" || b.Error.Message == "" {
		t.Errorf("the refusal = %+v", b.Error)
	}
	w = r.call("DELETE", canvasPath+"/connections", link, opts{cookie: adm})
	r.wantErr(w, 409, panel.CodeRefused)
	_ = json.Unmarshal(w.Body.Bytes(), &b)
	if b.Error.Params["reason"] != "automatic_connection" || b.Error.Params["reasons"] == nil {
		t.Errorf("the refusal = %+v", b.Error)
	}
	if r.audited("panel.canvas_disconnect") != 0 {
		t.Error("a refused removal was audited as done")
	}
	r.wantErr(r.call("PUT", canvasPath+"/scrolls/p1", panel.ScrollWriteRequest{Content: "x", Base: 1}, opts{cookie: adm}), 409, panel.CodeConflict)
}

func TestTheCanvasKeepsTheLayoutForAnyoneWhoSeesTheTable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	f := &fakeCanvas{}
	r.panel.SetCanvas(f)
	_, obs := r.member("Beto", "beto", users.Observer)
	req := panel.LayoutRequest{Items: []panel.CanvasItem{{Key: "character:c1", X: 10, Y: 20}}}
	if w := r.call("PUT", canvasPath+"/layout", req, opts{cookie: obs}); w.Code != 204 {
		t.Fatalf("layout: %d %s", w.Code, w.Body)
	}
	if len(f.layouts) != 1 || f.layouts[0].Items[0].X != 10 {
		t.Errorf("saved %+v", f.layouts)
	}
	r.wantErr(r.call("PUT", canvasPath+"/layout", panel.LayoutRequest{Items: []panel.CanvasItem{{Key: "semprefixo"}}}, opts{cookie: obs}), 400, panel.CodeBadRequest)
}

func TestTheCanvasIsUnavailableWithoutTheEngine(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.wantErr(r.call("GET", canvasPath, nil, opts{cookie: r.ownerSession()}), 503, panel.CodeUnavailable)
}

// The followers of a canvas are told when the database changes, so an agent that
// writes a scroll shows on the screen without a reload.
func TestTheFollowersOfACanvasAreToldWhenTheDatabaseChanges(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.panel.SetCanvas(&fakeCanvas{})
	srv := r.wsServer()
	_, tok := r.member("Beto", "beto", users.Observer)
	c, _, err := r.dial(srv, tok, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, panel.TypeUISubscribe, "01A", panel.UISubscribe{Topics: []string{"canvas:loja/m1", "canvas:blog/m1"}})
	snap := read(t, c)
	var ev panel.UIEvent
	_ = json.Unmarshal(snap.Payload, &ev)
	if ev.Topic != "canvas:loja/m1" || ev.Event != panel.EventSnapshot {
		t.Fatalf("the first event is %+v", ev)
	}
	var st panel.CanvasState
	_ = json.Unmarshal(ev.Data, &st)
	if st.Table != "m1" || st.CanEdit {
		t.Errorf("the snapshot is %+v", st)
	}
	var sub panel.UISubscribed
	_ = json.Unmarshal(read(t, c).Payload, &sub)
	if len(sub.Topics) != 1 || len(sub.Rejected) != 1 {
		t.Fatalf("subscribed %+v", sub)
	}

	// a commit, then the quiet the panel waits for
	if err := r.db.WriteDurable(r.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.ctx, `INSERT INTO audit_log (id, at, machine_id, action, detail) VALUES ('x1', 1, 'm1', 'agent.wrote_a_scroll', '{}')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	r.clk.BlockUntil(1)
	r.clk.Advance(150 * time.Millisecond)
	got := read(t, c)
	_ = json.Unmarshal(got.Payload, &ev)
	if ev.Topic != "canvas:loja/m1" || ev.Event != panel.EventChanged {
		t.Fatalf("the event is %+v", ev)
	}
}
