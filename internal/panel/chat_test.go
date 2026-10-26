package panel_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http/httptest"
	"net/textproto"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/panelchat"
	"github.com/mestre-ia/mestre/internal/users"
)

const chatLimit = 4096

type chatRig struct {
	*rig
	svc  *panelchat.Service
	ib   *inbox.Store
	root string
	bia  users.User
	ana  users.User
	otto users.User

	biaTok, anaTok, ottoTok string

	mu     sync.Mutex
	sealed []string
}

// newChatRig is a panel with the conversation over the real service: a Grão-Mestre that is
// always idle, the inbox of the Guild database, and the attachments of a folder of the test.
func newChatRig(t *testing.T) *chatRig {
	t.Helper()
	c := &chatRig{rig: newRig(t), root: t.TempDir()}
	for _, ws := range []string{"loja", "blog"} {
		if _, err := c.db.Writer().ExecContext(c.ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES (?, ?, ?, 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, ws, "lucas/"+ws, "lucas/"+ws); err != nil {
			t.Fatal(err)
		}
	}
	c.ib = &inbox.Store{DB: c.db}
	files, err := attachments.New(attachments.Options{DB: c.db, Clock: c.clk, Fetcher: nofetch{}, Limit: chatLimit, Root: func(context.Context, string) (string, error) { return c.root, nil }})
	if err != nil {
		t.Fatal(err)
	}
	gm := characters.Character{ID: "gm-loja", Role: characters.RoleGrandMaster, Workspace: "loja", State: characters.Idle}
	c.svc, err = panelchat.New(panelchat.Options{DB: c.db, Clock: c.clk, Users: c.users, Inbox: c.ib,
		Enqueue: func(ctx context.Context, n inbox.NewItem) (inbox.Item, error) {
			return c.ib.Insert(ctx, n, c.clk.Now())
		},
		GrandMaster: func(_ context.Context, ws string) (characters.Character, bool, error) { return gm, ws == "loja", nil },
		Character:   func(context.Context, string) (characters.Character, error) { return gm, nil },
		Pending:     func(ctx context.Context, id string) (int, error) { p, err := c.ib.Pending(ctx, id); return len(p), err },
		Receive:     files.ReceiveData, Limit: files.Limit, Mask: logging.NewMasker().Mask,
		Open: func(_ context.Context, _ string, o inbox.Outbound, i int) (panel.ChatDownload, error) {
			return panel.ChatDownload{Name: path.Base(o.Attachments[i]) + `"; x="y`, Size: 3, Body: io.NopCloser(strings.NewReader("pdf"))}, nil
		},
		Discard: files.Discard,
		AuditSecret: func(_ context.Context, user, _ string) {
			c.mu.Lock()
			defer c.mu.Unlock()
			c.sealed = append(c.sealed, user)
		}})
	if err != nil {
		t.Fatal(err)
	}
	c.panel.Close()
	c.panel, err = panel.New(panel.Options{DB: c.db, Clock: c.clk, Users: c.users, Alerts: c.al, Assets: c.assets, Machine: "m1", Chat: c.svc})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.panel.Close)
	c.bia, c.biaTok = c.member("Bia", "bia", users.Administrator)
	c.ana, c.anaTok = c.member("Ana", "ana", users.Approver)
	c.otto, c.ottoTok = c.member("Otto", "otto", users.Observer)
	return c
}

type nofetch struct{}

func (nofetch) GetFile(context.Context, string) (string, int64, error)  { return "", 0, io.EOF }
func (nofetch) Download(context.Context, string, int64) ([]byte, error) { return nil, io.EOF }

// part is a piece of a message: a field or a file.
type part struct{ name, filename, value string }

// post sends a multipart message to the route, with the parts in the order given.
func (c *chatRig) post(path string, tok string, parts ...part) *httptest.ResponseRecorder {
	c.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, p := range parts {
		if p.filename == "" {
			_ = mw.WriteField(p.name, p.value)
			continue
		}
		h := textproto.MIMEHeader{}
		h.Set("Content-Disposition", `form-data; name="`+p.name+`"; filename="`+p.filename+`"`)
		h.Set("Content-Type", "application/pdf")
		w, _ := mw.CreatePart(h)
		_, _ = w.Write([]byte(p.value))
	}
	_ = mw.Close()
	req := c.request("POST", path, nil, opts{cookie: tok})
	req.Body = io.NopCloser(&body)
	req.ContentLength = int64(body.Len())
	req.Header.Set("Content-Type", mw.FormDataContentType())
	w := httptest.NewRecorder()
	c.panel.ServeHTTP(w, req)
	return w
}

func msg(id, text string, more ...part) []part {
	return append([]part{{name: "text", value: text}, {name: "client_id", value: id}}, more...)
}

func (c *chatRig) conversation(tok, ws string) panel.ChatConversation {
	c.t.Helper()
	w := c.call("GET", "/api/chat/"+ws, nil, opts{cookie: tok})
	if w.Code != 200 {
		c.t.Fatalf("conversation: %d %s", w.Code, w.Body)
	}
	return decodeInto[panel.ChatConversation](c.t, w)
}

// The side panel: who is shown which workspaces, and the input disabled, with the reason, for the
// level that may not write.
func TestTheConversationShowsEachUserHisWorkspacesAndWhoMayWrite(t *testing.T) {
	t.Parallel()
	c := newChatRig(t)
	for tok, want := range map[string]bool{c.biaTok: true, c.anaTok: true, c.ottoTok: false} {
		w := c.call("GET", "/api/chat", nil, opts{cookie: tok})
		ov := decodeInto[panel.ChatOverview](t, w)
		if w.Code != 200 || len(ov.Workspaces) != 1 || ov.Workspaces[0].Slug != "loja" || ov.Workspaces[0].CanTalk != want || ov.Limit != chatLimit || ov.MaxFiles != panelchat.MaxFiles {
			t.Fatalf("overview %d %+v", w.Code, ov)
		}
	}
	if conv := c.conversation(c.ottoTok, "loja"); conv.CanTalk || conv.Status.State != "idle" {
		t.Fatalf("the Observer sees %+v", conv)
	}
	// a workspace the user does not act in is not there, whether it exists or not
	for _, ws := range []string{"blog", "nada"} {
		c.wantErr(c.call("GET", "/api/chat/"+ws, nil, opts{cookie: c.biaTok}), 404, panel.CodeNotFound)
	}
	// the Owner acts in every workspace
	ov := decodeInto[panel.ChatOverview](t, c.call("GET", "/api/chat", nil, opts{cookie: c.ownerSession()}))
	if len(ov.Workspaces) != 2 {
		t.Fatalf("the Owner sees %+v", ov)
	}
	c.wantErr(c.call("GET", "/api/chat", nil, opts{}), 401, panel.CodeUnauthenticated)
}

// The server checks the level, the workspace and the origin of every send; nothing is queued by a
// request that is refused.
func TestTheServerChecksLevelWorkspaceAndOriginOnEverySend(t *testing.T) {
	t.Parallel()
	c := newChatRig(t)
	c.wantErr(c.post("/api/chat/loja/messages", c.ottoTok, msg("msg-route-001", "oi")...), 403, panel.CodeForbiddenLevel)
	c.wantErr(c.post("/api/chat/blog/messages", c.biaTok, msg("msg-route-002", "oi")...), 404, panel.CodeNotFound)
	c.wantErr(c.post("/api/chat/loja/messages", "", msg("msg-route-003", "oi")...), 401, panel.CodeUnauthenticated)
	// the origin of the request: a page of another site cannot write in the name of the user
	req := c.request("POST", "/api/chat/loja/messages", nil, opts{cookie: c.biaTok, origin: "https://evil.example"})
	w := httptest.NewRecorder()
	c.panel.ServeHTTP(w, req)
	c.wantErr(w, 403, panel.CodeBadOrigin)
	if its, _ := c.ib.All(c.ctx, "gm-loja"); len(its) != 0 {
		t.Fatalf("%d messages got in", len(its))
	}
}

// A message goes in the name of the user the session says, with the origin "painel"; the same id
// twice is one message; the conversation is the user's own and nobody else sees it.
func TestAMessageSentThroughTheRouteIsTheUsersOwnAndPrivate(t *testing.T) {
	t.Parallel()
	c := newChatRig(t)
	w := c.post("/api/chat/loja/messages", c.biaTok, msg("msg-route-010", "abra a mesa do login")...)
	sent := decodeInto[panel.ChatSent](t, w)
	if w.Code != 201 || !strings.HasPrefix(sent.ID, "in-") || sent.Held {
		t.Fatalf("send %d %s", w.Code, w.Body)
	}
	if w := c.post("/api/chat/loja/messages", c.biaTok, msg("msg-route-010", "abra a mesa do login")...); w.Code != 201 {
		t.Fatalf("a repeat: %d %s", w.Code, w.Body)
	}
	its, _ := c.ib.All(c.ctx, "gm-loja")
	if len(its) != 1 || its[0].Origin.Channel != "panel" || its[0].Origin.User != c.bia.ID || its[0].Origin.Message != "msg-route-010" || its[0].Origin.Conversation == "" {
		t.Fatalf("inbox %+v", its)
	}
	if conv := c.conversation(c.biaTok, "loja"); len(conv.Entries) != 1 || conv.Entries[0].Text != "abra a mesa do login" || conv.Entries[0].ID != sent.ID {
		t.Fatalf("Bia's conversation %+v", conv)
	}
	if conv := c.conversation(c.anaTok, "loja"); len(conv.Entries) != 0 {
		t.Fatalf("Ana sees Bia's messages: %+v", conv.Entries)
	}
	// Ana cannot quote Bia's message either
	c.wantErr(c.post("/api/chat/loja/messages", c.anaTok, msg("msg-route-011", "e isso?", part{name: "quote", value: sent.ID})...), 404, panel.CodeChatQuoteUnknown)
	// the field comes before the files: a file first, then a field, is refused
	c.wantErr(c.post("/api/chat/loja/messages", c.biaTok, part{name: "text", value: "x"}, part{name: "client_id", value: "msg-route-012"},
		part{name: "files", filename: "a.pdf", value: "%PDF-1.4"}, part{name: "quote", value: sent.ID}), 400, panel.CodeChatMalformed)
	// a token has the piece taken out, is audited, and the answer tells the user
	w = c.post("/api/chat/loja/messages", c.biaTok, msg("msg-route-013", "token ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD")...)
	if got := decodeInto[panel.ChatSent](t, w); w.Code != 201 || got.Secret != "removed" {
		t.Fatalf("send with a token: %d %s", w.Code, w.Body)
	}
	if len(c.sealed) != 1 {
		t.Fatalf("audited %v", c.sealed)
	}
	if its, _ := c.ib.All(c.ctx, "gm-loja"); len(its) != 2 || strings.Contains(its[1].Text, "ghp_") {
		t.Fatalf("%d messages in the inbox: %+v", len(its), its)
	}
}

// A file goes with the message and is kept; one over the limit is refused with the limit, before the
// message is made (nothing is queued); so is a body of files that together pass what a message takes.
func TestFilesFollowTheAttachmentRulesAndTheLimitIsShown(t *testing.T) {
	t.Parallel()
	c := newChatRig(t)
	w := c.post("/api/chat/loja/messages", c.biaTok, msg("msg-route-020", "segue", part{name: "files", filename: "../../etc/contrato.pdf", value: "%PDF-1.4\nx"})...)
	if w.Code != 201 {
		t.Fatalf("send: %d %s", w.Code, w.Body)
	}
	its, _ := c.ib.All(c.ctx, "gm-loja")
	if len(its) != 1 || len(its[0].Attachments) != 1 || !strings.HasPrefix(its[0].Attachments[0], ".mestre/anexos/") || strings.Contains(its[0].Attachments[0], "..") ||
		!strings.HasSuffix(its[0].Attachments[0], "-contrato.pdf") {
		t.Fatalf("attachments %+v", its)
	}
	w = c.post("/api/chat/loja/messages", c.biaTok, msg("msg-route-021", "grande", part{name: "files", filename: "grande.pdf", value: strings.Repeat("a", chatLimit+1)})...)
	c.wantErr(w, 413, panel.CodeChatFileTooLarge)
	if e := decodeInto[panel.ErrorBody](t, w); e.Error.Params["limit"] != float64(chatLimit) {
		t.Fatalf("params %v", e.Error.Params)
	}
	var many []part
	for i := 0; i < panelchat.MaxFiles+1; i++ {
		many = append(many, part{name: "files", filename: "a.pdf", value: "%PDF-1.4"})
	}
	c.wantErr(c.post("/api/chat/loja/messages", c.biaTok, msg("msg-route-022", "muitos", many...)...), 422, panel.CodeChatTooManyFiles)
	// a body that passes the limit of the whole message is cut off by the server
	c.wantErr(c.post("/api/chat/loja/messages", c.biaTok, msg("msg-route-023", "enorme", part{name: "files", filename: "e.pdf", value: strings.Repeat("a", chatLimit*panelchat.MaxFiles+2<<20)})...), 413, panel.CodeChatFileTooLarge)
	if its, _ := c.ib.All(c.ctx, "gm-loja"); len(its) != 1 {
		t.Fatalf("%d messages in the inbox", len(its))
	}
}

// CA-CONV-02 through the screens: the answer for a user who has no panel open is kept; when he
// follows the topic he gets the unread counter in the snapshot, the new answer arrives as an event,
// and opening the conversation reads it. The topic of another user is refused.
func TestAnswersReachTheOpenPanelAndTheClosedOneFindsThemWithTheCounter(t *testing.T) {
	t.Parallel()
	c := newChatRig(t)
	// the answer comes while Bia has no panel open
	if _, err := c.ib.AddOutbound(c.ctx, inbox.Outbound{Workspace: "loja", Channel: "panel", User: c.bia.ID, Text: "a mesa abriu", CreatedAt: c.clk.Now()}); err != nil {
		t.Fatal(err)
	}
	srv := c.wsServer()
	conn, _, err := c.dial(srv, c.biaTok, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	send(t, conn, panel.TypeUISubscribe, "S1", panel.UISubscribe{Topics: []string{"chat:" + c.bia.ID, "chat:" + c.ana.ID}})
	typ, ev := readEvent(t, conn)
	var snap panel.ChatSnapshot
	mustJSON(t, ev.Data, &snap)
	if typ != panel.TypeUIEvent || ev.Event != panel.EventSnapshot || snap.Unread != 1 {
		t.Fatalf("snapshot %s %+v %+v", typ, ev, snap)
	}
	var sub panel.UISubscribed
	mustJSON(t, read(t, conn).Payload, &sub)
	if len(sub.Rejected) != 1 || sub.Rejected[0].Topic != "chat:"+c.ana.ID {
		t.Fatalf("subscribed %+v", sub)
	}
	conv := c.conversation(c.biaTok, "loja")
	if conv.Unread != 1 || len(conv.Entries) != 1 || !conv.Entries[0].Unread {
		t.Fatalf("conversation %+v", conv)
	}
	if w := c.call("POST", "/api/chat/loja/read", nil, opts{cookie: c.biaTok}); w.Code != 204 {
		t.Fatalf("read: %d %s", w.Code, w.Body)
	}
	_, ev = readEvent(t, conn) // the other tabs of the user learn it was read
	if ev.Event != panel.EventChatChanged || ev.Topic != "chat:"+c.bia.ID {
		t.Fatalf("event %+v", ev)
	}
	if conv := c.conversation(c.biaTok, "loja"); conv.Unread != 0 {
		t.Fatalf("after reading %+v", conv)
	}
	// a new answer reaches the panel that is open
	o, err := c.ib.AddOutbound(c.ctx, inbox.Outbound{Workspace: "loja", Channel: "panel", User: c.bia.ID, Text: "e já tem testes", CreatedAt: c.clk.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.svc.Channel().Send(c.ctx, o); err != nil {
		t.Fatal(err)
	}
	_, ev = readEvent(t, conn)
	var changed panel.ChatChangedEvent
	mustJSON(t, ev.Data, &changed)
	if ev.Event != panel.EventChatChanged || changed.Workspace != "loja" {
		t.Fatalf("event %+v", ev)
	}
	if conv := c.conversation(c.biaTok, "loja"); conv.Unread != 1 || conv.Entries[1].Text != "e já tem testes" {
		t.Fatalf("conversation %+v", conv)
	}
	// the status of the Grão-Mestre is told to those who act in the workspace
	c.svc.Changed(c.ctx, "gm-loja")
	_, ev = readEvent(t, conn)
	var st panel.ChatStatusEvent
	mustJSON(t, ev.Data, &st)
	if ev.Event != panel.EventChatStatus || st.Workspace != "loja" || st.Status.State != "idle" {
		t.Fatalf("event %+v", ev)
	}
}

// CA-CONV-09 through the route: the click goes back once, a second click is refused, and another user
// or a level that may not talk cannot answer.
func TestAClickOnAnOptionAnswersOnceThroughTheRoute(t *testing.T) {
	t.Parallel()
	c := newChatRig(t)
	id, err := c.svc.AskChoice(c.ctx, panelchat.Question{Workspace: "loja", Asker: "gm-loja", UserID: c.bia.ID, Text: "Qual banco?", Options: []string{"sqlite", "postgres", "mysql"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/chat-choices/" + itoa(id)
	c.wantErr(c.call("POST", path, panel.ChatChoice{Option: 1}, opts{cookie: c.ottoTok}), 403, panel.CodeForbiddenLevel)
	c.wantErr(c.call("POST", path, panel.ChatChoice{Option: 1}, opts{cookie: c.anaTok}), 404, panel.CodeChatChoiceUnknown)
	if w := c.call("POST", path, panel.ChatChoice{Option: 1}, opts{cookie: c.biaTok}); w.Code != 204 {
		t.Fatalf("click: %d %s", w.Code, w.Body)
	}
	c.wantErr(c.call("POST", path, panel.ChatChoice{Option: 0}, opts{cookie: c.biaTok}), 409, panel.CodeChatChoiceTaken)
	n := 0
	its, _ := c.ib.All(c.ctx, "gm-loja")
	for _, it := range its {
		if it.Origin != nil && it.Origin.Choice == id {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d answers in the inbox", n)
	}
	c.wantErr(c.call("POST", "/api/chat-choices/lixo", panel.ChatChoice{}, opts{cookie: c.biaTok}), 400, panel.CodeBadRequest)
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// The files the Grão-Mestre sent are a download, never shown in the page, and only for the user
// they were sent to.
func TestADownloadIsAnAttachmentOnlyForTheUserItWasSentTo(t *testing.T) {
	t.Parallel()
	c := newChatRig(t)
	o, err := c.ib.AddOutbound(c.ctx, inbox.Outbound{Workspace: "loja", Channel: "panel", User: c.bia.ID, Text: "veja", Attachments: []string{"docs/plano.pdf"}, CreatedAt: c.clk.Now()})
	if err != nil {
		t.Fatal(err)
	}
	conv := c.conversation(c.biaTok, "loja")
	link := conv.Entries[0].Attachments[0].Download
	if link != "/api/chat/loja/files/"+itoa(o.ID)+"/0" {
		t.Fatalf("link %q", link)
	}
	w := c.call("GET", link, nil, opts{cookie: c.biaTok})
	if w.Code != 200 || w.Body.String() != "pdf" {
		t.Fatalf("download: %d %s", w.Code, w.Body)
	}
	h := w.Header()
	if h.Get("Content-Type") != "application/octet-stream" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment;") || h.Get("X-Content-Type-Options") != "nosniff" ||
		!strings.Contains(h.Get("Content-Security-Policy"), "sandbox") || strings.Contains(h.Get("Content-Disposition"), `x="y`) && !strings.Contains(h.Get("Content-Disposition"), `\"`) {
		t.Fatalf("headers %v", h)
	}
	c.wantErr(c.call("GET", link, nil, opts{cookie: c.anaTok}), 404, panel.CodeChatFileGone)
	c.wantErr(c.call("GET", "/api/chat/blog/files/"+itoa(o.ID)+"/0", nil, opts{cookie: c.biaTok}), 404, panel.CodeNotFound)
	c.wantErr(c.call("GET", "/api/chat/loja/files/"+itoa(o.ID)+"/7", nil, opts{cookie: c.biaTok}), 404, panel.CodeChatFileGone)
	c.wantErr(c.call("GET", "/api/chat/loja/files/x/0", nil, opts{cookie: c.biaTok}), 400, panel.CodeBadRequest)
}

// Without the conversation (a Citadel that has none) the routes say so.
func TestWithoutTheConversationTheRoutesAnswerUnavailable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	tok := r.ownerSession()
	r.wantErr(r.call("GET", "/api/chat", nil, opts{cookie: tok}), 503, panel.CodeUnavailable)
	r.wantErr(r.call("GET", "/api/chat/loja", nil, opts{cookie: tok}), 503, panel.CodeUnavailable)
}

// The lists of the conversation are JSON lists, never null: the screen reads their length.
func TestAnEmptyConversationHasListsAndNotNulls(t *testing.T) {
	t.Parallel()
	c := newChatRig(t)
	w := c.call("GET", "/api/chat/loja", nil, opts{cookie: c.biaTok})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"entries":[]`) {
		t.Fatalf("conversation: %d %s", w.Code, w.Body)
	}
	w = c.call("GET", "/api/chat", nil, opts{cookie: c.biaTok})
	if !strings.Contains(w.Body.String(), `"workspaces":[`) || strings.Contains(w.Body.String(), "null") {
		t.Fatalf("overview: %s", w.Body)
	}
}
