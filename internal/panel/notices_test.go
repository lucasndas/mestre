package panel_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/notices"
	"github.com/mestre-ia/mestre/internal/panel"
)

func mustJSON(t *testing.T, b []byte, v any) {
	t.Helper()
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
}

func (a *apprRig) notices(tok string) panel.NoticeList {
	a.t.Helper()
	w := a.call("GET", "/api/notices", nil, opts{cookie: tok})
	if w.Code != 200 {
		a.t.Fatalf("notices: %d %s", w.Code, w.Body)
	}
	return decodeInto[panel.NoticeList](a.t, w)
}

func (a *apprRig) add(n notices.Notice) int64 {
	a.t.Helper()
	id, _, err := a.feed.Add(a.ctx, n)
	if err != nil {
		a.t.Fatal(err)
	}
	return id
}

// The bell has what the Telegram says, with workspace and table or "Sistema", the time
// and a link; each user reads on his own; the new one reaches the bell that is open.
func TestTheBellKeepsWhatEachUserSeesAndWhatHeRead(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	srv := a.wsServer()
	c, _, err := a.dial(srv, a.anaTok, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, panel.TypeUISubscribe, "S1", panel.UISubscribe{Topics: []string{"notices:" + a.ana.ID, "notices:" + a.admin.ID}})
	typ, ev := readEvent(t, c)
	var snap panel.NoticesSnapshot
	mustJSON(t, ev.Data, &snap)
	if typ != panel.TypeUIEvent || ev.Event != panel.EventSnapshot || snap.Unread != 0 {
		t.Fatalf("snapshot %s %+v %+v", typ, ev, snap)
	}
	read(t, c) // the answer to the subscription, which also rejects the topic of another user
	first := a.add(notices.Notice{Kind: notices.Notify, Workspace: "loja", Mesa: "42-login", Audience: notices.Workspace, Text: "a mesa terminou",
		LinkKind: notices.LinkTable, LinkRef: "42-login"})
	a.add(notices.Notice{Kind: notices.System, Audience: notices.Owner, Text: "O PIN de Ana foi bloqueado"})
	a.add(notices.Notice{Kind: notices.Notify, Workspace: "blog", Audience: notices.Workspace, Text: "outro workspace"})

	_, ev = readEvent(t, c)
	mustJSON(t, ev.Data, &snap)
	if ev.Event != "unread" || ev.Topic != "notices:"+a.ana.ID || snap.Unread != 1 {
		t.Fatalf("event %+v %+v", ev, snap)
	}
	ana := a.notices(a.anaTok)
	if len(ana.Notices) != 1 || ana.Unread != 1 {
		t.Fatalf("Ana sees %+v", ana)
	}
	n := ana.Notices[0]
	if n.ID != first || n.Workspace != "loja" || n.Table != "42-login" || n.Link.Kind != "table" || n.Link.Ref != "42-login" || n.Read || n.CreatedAt.IsZero() {
		t.Fatalf("notice %+v", n)
	}
	// the Owner sees his own and the one of the workspace he acts in (all of them)
	if own := a.notices(a.ownerTok); len(own.Notices) != 3 || own.Unread != 3 {
		t.Fatalf("Owner sees %+v", own)
	}
	// Ana reads hers: it is read for her and still new for the Administrator
	if w := a.call("POST", "/api/notices/read", panel.NoticeReadRequest{IDs: []int64{first}}, opts{cookie: a.anaTok}); w.Code != 204 {
		t.Fatalf("read: %d %s", w.Code, w.Body)
	}
	if got := a.notices(a.anaTok); got.Unread != 0 || !got.Notices[0].Read {
		t.Fatalf("Ana after %+v", got)
	}
	if got := a.notices(a.adminTok); got.Unread != 1 {
		t.Fatalf("the Administrator after %+v", got)
	}
	// marking with no ids marks every notice the user sees, never one of another
	if w := a.call("POST", "/api/notices/read", panel.NoticeReadRequest{}, opts{cookie: a.ownerTok}); w.Code != 204 {
		t.Fatalf("read all: %d", w.Code)
	}
	if got := a.notices(a.ownerTok); got.Unread != 0 {
		t.Fatalf("Owner after %+v", got)
	}
	if got := a.notices(a.adminTok); got.Unread != 1 {
		t.Fatalf("the Administrator after the Owner read %+v", got)
	}
	// the bell forgets what is older than the window
	a.clk.Advance(notices.Window + time.Hour)
	if got := a.notices(a.login2("bia", otherPass)); len(got.Notices) != 0 {
		t.Fatalf("old notices %+v", got)
	}
}

// Part of CA-CANAL-07 (the part of the panel): the alert of an unknown sender is for the
// Owner and the Administrators, and the count of tries edits the same notice.
func TestTheAlertOfAnUnknownSenderIsForTheOwnerAndTheAdministrators(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	id := a.add(notices.Notice{Kind: notices.Stranger, Audience: notices.Admins, Text: "Remetente não autorizado\nID: 99\n1 tentativa (um alerta por hora)"})
	for tok, want := range map[string]int{a.ownerTok: 1, a.adminTok: 1, a.anaTok: 0, a.obsTok: 0} {
		if got := a.notices(tok); len(got.Notices) != want {
			t.Fatalf("a user sees %d, want %d", len(got.Notices), want)
		}
	}
	if err := a.feed.Update(a.ctx, id, "Remetente não autorizado\nID: 99\n3 tentativas desde 09:00 (um alerta por hora)"); err != nil {
		t.Fatal(err)
	}
	got := a.notices(a.adminTok)
	if len(got.Notices) != 1 || !strings.Contains(got.Notices[0].Text, "3 tentativas") {
		t.Fatalf("the notice after the update %+v", got)
	}
}

// A notice is masked before it is kept: the Telegram masks what it sends, and the panel does too.
func TestANoticeIsMaskedBeforeItIsKept(t *testing.T) {
	t.Parallel()
	a := newApprRig(t)
	token := "ghp_" + strings.Repeat("a1B2", 9)
	a.add(notices.Notice{Kind: notices.Notify, Workspace: "loja", Audience: notices.Workspace, Text: "o token " + token + " vazou"})
	if got := a.notices(a.anaTok); len(got.Notices) != 1 || strings.Contains(got.Notices[0].Text, token) {
		t.Fatalf("notice %+v", got)
	}
	if _, _, err := a.feed.Add(a.ctx, notices.Notice{Kind: notices.Notify, Text: "  "}); err == nil {
		t.Fatal("an empty notice was kept")
	}
}

// Without the engine the bell and the central say so, not "nothing here".
func TestWithoutTheEngineTheCentralAndTheBellAreUnavailable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	r.wantErr(r.call("GET", "/api/approvals", nil, opts{cookie: tok}), 503, panel.CodeUnavailable)
	r.wantErr(r.call("GET", "/api/notices", nil, opts{cookie: tok}), 503, panel.CodeUnavailable)
}

// login2 signs a user in again, for a test that moved the clock past his session.
func (a *apprRig) login2(login, password string) string {
	a.t.Helper()
	w := a.login(login, password, opts{})
	if w.Code != 200 {
		a.t.Fatalf("login: %d %s", w.Code, w.Body)
	}
	return token(a.t, w)
}
