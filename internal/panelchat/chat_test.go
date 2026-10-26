package panelchat_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/attachments"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/panelchat"
	"github.com/mestre-ia/mestre/internal/store"
	"github.com/mestre-ia/mestre/internal/testkit/guildtpl"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

const pdf = "%PDF-1.4\n1 0 obj\n<< >>\nendobj\n"

type rig struct {
	t     *testing.T
	ctx   context.Context
	clk   *clock.Fake
	db    *store.DB
	users *users.Service
	ib    *inbox.Store
	svc   *panelchat.Service
	root  string

	owner, bia, ana, otto, rui users.User

	mu      sync.Mutex
	gm      characters.Character
	hasGM   bool
	locked  bool
	limit   int64
	audited []string
	events  []panel.ChatEvent
}

func (r *rig) setGM(state characters.State, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gm.State, r.gm.Reason = state, reason
}

func (r *rig) setLocked(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.locked = v
}

func (r *rig) heard() []panel.ChatEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]panel.ChatEvent(nil), r.events...)
}

func newRig(t *testing.T, mod ...func(*panelchat.Options)) *rig {
	t.Helper()
	ctx := t.Context()
	clk := clock.NewFake(time.Date(2026, 10, 26, 9, 0, 0, 0, time.UTC))
	db, err := guildtpl.Open(ctx, filepath.Join(t.TempDir(), "mestre.db"), store.Options{Synchronous: "OFF", Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, ws := range []string{"loja", "blog"} {
		if _, err := db.Writer().ExecContext(ctx, `INSERT INTO workspaces (slug, repo, repo_key, clone_url, default_branch, state, stage, created_at, updated_at)
			VALUES (?, ?, ?, 'https://example.test/x.git', 'main', 'active', '', 1, 1)`, ws, "lucas/"+ws, "lucas/"+ws); err != nil {
			t.Fatal(err)
		}
	}
	cheap := users.PINCost{MemoryKiB: 8, Time: 1, Threads: 1}
	us, err := users.New(users.Options{DB: db, Clock: clk, PIN: cheap, Password: cheap})
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, ctx: ctx, clk: clk, db: db, users: us, ib: &inbox.Store{DB: db}, root: t.TempDir(), limit: 1 << 20, hasGM: true,
		gm: characters.Character{ID: "gm-loja", Role: characters.RoleGrandMaster, Workspace: "loja", State: characters.Idle}}
	r.owner, err = us.CreateOwner(ctx, "Lucas")
	if err != nil {
		t.Fatal(err)
	}
	mk := func(name string, level users.Level, ws ...string) users.User {
		u, err := us.Create(ctx, r.owner, users.NewUser{Name: name, Level: level, Workspaces: ws})
		if err != nil {
			t.Fatal(err)
		}
		return u
	}
	r.bia = mk("Bia", users.Administrator, "loja")
	r.ana = mk("Ana", users.Approver, "loja")
	r.otto = mk("Otto", users.Observer, "loja")
	r.rui = mk("Rui", users.Approver, "blog")
	files, err := attachments.New(attachments.Options{DB: db, Clock: clk, Fetcher: noFetch{}, Limit: r.limit, Root: func(context.Context, string) (string, error) { return r.root, nil }})
	if err != nil {
		t.Fatal(err)
	}
	opts := panelchat.Options{
		DB: db, Clock: clk, Users: us, Inbox: r.ib,
		Enqueue: func(ctx context.Context, n inbox.NewItem) (inbox.Item, error) { return r.ib.Insert(ctx, n, clk.Now()) },
		GrandMaster: func(_ context.Context, ws string) (characters.Character, bool, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.gm, r.hasGM && ws == r.gm.Workspace, nil
		},
		Character: func(_ context.Context, id string) (characters.Character, error) {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.gm, nil
		},
		Pending: func(ctx context.Context, id string) (int, error) {
			p, err := r.ib.Pending(ctx, id)
			return len(p), err
		},
		Receive: files.ReceiveData,
		Limit:   files.Limit,
		Locked: func(context.Context) bool {
			r.mu.Lock()
			defer r.mu.Unlock()
			return r.locked
		},
		Mask: logging.NewMasker().Mask,
		AuditSecret: func(_ context.Context, user string) {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.audited = append(r.audited, user)
		},
	}
	for _, m := range mod {
		m(&opts)
	}
	r.svc, err = panelchat.New(opts)
	if err != nil {
		t.Fatal(err)
	}
	stop := r.svc.Listen(func(e panel.ChatEvent) {
		r.mu.Lock()
		defer r.mu.Unlock()
		r.events = append(r.events, e)
	})
	t.Cleanup(stop)
	return r
}

type noFetch struct{}

func (noFetch) GetFile(context.Context, string) (string, int64, error) { return "", 0, os.ErrNotExist }
func (noFetch) Download(context.Context, string, int64) ([]byte, error) {
	return nil, os.ErrNotExist
}

// send gives a message the way the route does: the draft, the files, the send.
func (r *rig) send(u users.User, in panel.ChatDraftInput, files ...[2]string) (panel.ChatSent, error) {
	r.t.Helper()
	d, err := r.svc.Begin(r.ctx, u, in)
	if err != nil {
		return panel.ChatSent{}, err
	}
	for _, f := range files {
		if err := d.AddFile(r.ctx, f[0], "application/pdf", strings.NewReader(f[1])); err != nil {
			return panel.ChatSent{}, err
		}
	}
	return d.Send(r.ctx)
}

func in(ws, text, id string) panel.ChatDraftInput {
	return panel.ChatDraftInput{Workspace: ws, Text: text, ClientID: id, Session: "sess-1"}
}

func (r *rig) items(ws string, u users.User) []inbox.Item {
	r.t.Helper()
	its, err := r.ib.UserItems(r.ctx, ws, u.ID, 50)
	if err != nil {
		r.t.Fatal(err)
	}
	return its
}

func (r *rig) conv(u users.User, ws string) panel.ChatConversation {
	r.t.Helper()
	c, err := r.svc.Conversation(r.ctx, u, ws)
	if err != nil {
		r.t.Fatal(err)
	}
	return c
}

func (r *rig) reply(u users.User, channel, ws, text string, attach ...string) inbox.Outbound {
	r.t.Helper()
	o, err := r.ib.AddOutbound(r.ctx, inbox.Outbound{Workspace: ws, Channel: channel, User: u.ID, Text: text, Attachments: attach, CreatedAt: r.clk.Now()})
	if err != nil {
		r.t.Fatal(err)
	}
	r.clk.Advance(time.Second)
	return o
}

func wantCode(t *testing.T, err error, code panel.Code) {
	t.Helper()
	api, ok := err.(*panel.APIError)
	if !ok || api.Code != code {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}

// A message of the panel enters the inbox of the Grão-Mestre with the origin "painel" (the user,
// the session and the id of the message) and the priority of a message of a user; sent twice with
// the same id, it is in the inbox once.
func TestAMessageOfThePanelEntersTheInboxWithItsOrigin(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sent, err := r.send(r.bia, in("loja", "  abra a mesa do login  ", "msg-0000001"))
	if err != nil || sent.Held {
		t.Fatalf("sent %+v, %v", sent, err)
	}
	its := r.items("loja", r.bia)
	if len(its) != 1 {
		t.Fatalf("items %+v", its)
	}
	it := its[0]
	o := it.Origin
	if it.Target != "gm-loja" || it.Kind != inbox.KindUser || it.Text != "abra a mesa do login" || it.Priority != inbox.PriorityUsers ||
		o.Channel != inbox.ChannelPanel || o.User != r.bia.ID || o.UserName != "Bia" || o.Conversation != "sess-1" || o.Message != "msg-0000001" ||
		o.Level != characters.LevelAdministrator {
		t.Fatalf("item %+v origin %+v", it, o)
	}
	if !strings.HasPrefix(it.Header(), "[Mestre · user · de Bia (Administrador) · via painel · m") {
		t.Fatalf("header %q", it.Header())
	}
	if _, err := r.send(r.bia, in("loja", "outra vez", "msg-0000001")); err != nil || len(r.items("loja", r.bia)) != 1 {
		t.Fatalf("a repeat of the id put the message twice: %v", err)
	}
	if s, _ := r.send(r.owner, in("loja", "oi", "msg-0000002")); s.ID == "" || r.items("loja", r.owner)[0].Priority != inbox.PriorityOwner {
		t.Fatal("the Owner is the first priority")
	}
	// the screens of the user hear of it, to show it in the other tabs
	if ev := r.heard(); len(ev) == 0 || ev[0].User != r.bia.ID || ev[0].Kind != panel.EventChatChanged {
		t.Fatalf("events %+v", ev)
	}
}

// Who may talk, and where: the level and the workspace are checked by the service itself.
func TestOnlyWhoMayTalkAndActsInTheWorkspaceSends(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, err := r.send(r.otto, in("loja", "oi", "msg-0000003"))
	wantCode(t, err, panel.CodeForbiddenLevel)
	_, err = r.send(r.rui, in("loja", "oi", "msg-0000004")) // acts only in "blog"
	wantCode(t, err, panel.CodeNotFound)
	_, err = r.send(r.ana, in("loja", "oi", "curto"))
	wantCode(t, err, panel.CodeChatMalformed)
	_, err = r.send(r.ana, in("loja", "  ", "msg-0000005"))
	wantCode(t, err, panel.CodeChatEmpty)
	_, err = r.send(r.ana, in("loja", strings.Repeat("a", panelchat.MaxText+1), "msg-0000006"))
	wantCode(t, err, panel.CodeChatTooLong)
	r.mu.Lock()
	r.hasGM = false
	r.mu.Unlock()
	_, err = r.send(r.ana, in("loja", "oi", "msg-0000007"))
	wantCode(t, err, panel.CodeChatNoGrandMaster)
	if n := len(r.items("loja", r.ana)) + len(r.items("loja", r.otto)) + len(r.items("loja", r.rui)); n != 0 {
		t.Fatalf("%d messages got in", n)
	}
	c := r.conv(r.otto, "loja")
	if c.CanTalk || len(c.Entries) != 0 {
		t.Fatalf("the Observer sees %+v", c)
	}
	if _, err := r.svc.Conversation(r.ctx, r.rui, "loja"); err == nil {
		t.Fatal("a user of another workspace reads the conversation")
	}
}

// A secret never goes to the Grão-Mestre: the message is refused, the attempt is audited without
// the text, and nothing is queued.
func TestASecretInAMessageIsRefusedAndAudited(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	_, err := r.send(r.bia, in("loja", "use este token ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD", "msg-0000008"))
	wantCode(t, err, panel.CodeChatSecret)
	if len(r.items("loja", r.bia)) != 0 || len(r.audited) != 1 || r.audited[0] != r.bia.ID {
		t.Fatalf("items %d audited %v", len(r.items("loja", r.bia)), r.audited)
	}
}

// Quoting a message tells the Grão-Mestre its table; only a message of the same user and workspace
// can be quoted, and the table is the one the server knows, never one the browser says.
func TestQuotingAMessageTellsTheGrandMasterItsTable(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	id, err := r.svc.AskChoice(r.ctx, panelchat.Question{Workspace: "loja", Mesa: "42-login", Asker: "gm-loja", UserID: r.bia.ID, Text: "Qual caminho?", Options: []string{"a", "b"}}, 1)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := r.send(r.bia, panel.ChatDraftInput{Workspace: "loja", Text: "sobre essa", ClientID: "msg-0000009", Quote: "ask-" + itoa(id)})
	if err != nil {
		t.Fatal(err)
	}
	it := r.items("loja", r.bia)[0]
	if it.Origin.Table != "42-login" || it.Origin.RepliedTo != "ask-"+itoa(id) || !strings.Contains(it.Header(), " · mesa 42-login · ") {
		t.Fatalf("origin %+v header %q", it.Origin, it.Header())
	}
	// the table follows the quote of that message too: a reply that answers it carries the table
	o := r.reply(r.bia, inbox.ChannelPanel, "loja", "feito")
	_ = o
	// another user's message, another workspace's message and a made-up id are not there to quote
	other, err := r.send(r.ana, in("loja", "minha", "msg-0000010"))
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{other.ID, "in-999", "out-999", "ask-999", "lixo", "in-"} {
		_, err := r.send(r.bia, panel.ChatDraftInput{Workspace: "loja", Text: "x", ClientID: "msg-quote-" + itoa(int64(len(q))), Quote: q})
		wantCode(t, err, panel.CodeChatQuoteUnknown)
	}
	if _, err := r.send(r.bia, panel.ChatDraftInput{Workspace: "loja", Text: "x", ClientID: "msg-0000011", Quote: sent.ID}); err != nil {
		t.Fatalf("quoting her own message: %v", err)
	}
	c := r.conv(r.bia, "loja")
	var quoted *panel.ChatEntry
	for i := range c.Entries {
		if c.Entries[i].Text == "sobre essa" {
			quoted = &c.Entries[i]
		}
	}
	if quoted == nil || quoted.Quote == nil || quoted.Quote.Excerpt != "Qual caminho?" || quoted.Quote.Mine {
		t.Fatalf("quote %+v", quoted)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// Part of CA-CONV-05: a PDF sent from the panel is saved as an attachment of the workspace with the origin
// "painel" and reaches the Grão-Mestre with its path.
func TestAPdfSentFromThePanelIsSavedAndDeliveredWithItsPath(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if _, err := r.send(r.bia, in("loja", "veja o relatório", "msg-0000012"), [2]string{"relatório final.pdf", pdf}); err != nil {
		t.Fatal(err)
	}
	it := r.items("loja", r.bia)[0]
	if len(it.Attachments) != 1 || !strings.HasPrefix(it.Attachments[0], ".mestre/anexos/2026/10/") || !strings.HasSuffix(it.Attachments[0], ".pdf") {
		t.Fatalf("attachments %v", it.Attachments)
	}
	body := it.Body()
	if !strings.Contains(it.Text, "[anexo] "+it.Attachments[0]+" · application/pdf · ") || !strings.HasPrefix(body, "veja o relatório\n[anexo] ") {
		t.Fatalf("text %q body %q", it.Text, body)
	}
	data, err := os.ReadFile(filepath.Join(r.root, filepath.FromSlash(it.Attachments[0])))
	if err != nil || string(data) != pdf {
		t.Fatalf("file: %v %q", err, data)
	}
	if fi, _ := os.Stat(filepath.Join(r.root, filepath.FromSlash(it.Attachments[0]))); fi != nil && fi.Mode()&0o111 != 0 {
		t.Fatalf("the file is executable: %v", fi.Mode())
	}
	var channel, user, session, mime string
	if err := r.db.Reader().QueryRowContext(r.ctx, `SELECT channel, user_id, panel_session, mime_detected FROM attachments WHERE workspace = 'loja'`).Scan(&channel, &user, &session, &mime); err != nil {
		t.Fatal(err)
	}
	if channel != "panel" || user != r.bia.ID || session != "sess-1" || mime != "application/pdf" {
		t.Fatalf("row %s %s %s %s", channel, user, session, mime)
	}
	// the conversation shows the file by its name and not as a line of the engine
	c := r.conv(r.bia, "loja")
	if len(c.Entries) != 1 || c.Entries[0].Text != "veja o relatório" || len(c.Entries[0].Attachments) != 1 || !strings.HasSuffix(c.Entries[0].Attachments[0].Name, ".pdf") {
		t.Fatalf("entry %+v", c.Entries)
	}
}

// A file over the limit is refused with the limit, and nothing of it is kept; so are too many files.
func TestAFileOverTheLimitIsRefusedAndNothingIsKept(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	big := bytes.Repeat([]byte("a"), int(r.limit)+1)
	d, err := r.svc.Begin(r.ctx, r.bia, in("loja", "oi", "msg-0000013"))
	if err != nil {
		t.Fatal(err)
	}
	err = d.AddFile(r.ctx, "grande.bin", "application/octet-stream", bytes.NewReader(big))
	wantCode(t, err, panel.CodeChatFileTooLarge)
	if p := err.(*panel.APIError).Params["limit"]; p != r.limit {
		t.Fatalf("params %v", err.(*panel.APIError).Params)
	}
	var n int
	_ = r.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM attachments`).Scan(&n)
	if n != 0 || len(r.items("loja", r.bia)) != 0 {
		t.Fatalf("%d attachments, %d items", n, len(r.items("loja", r.bia)))
	}
	for i := 0; i < panelchat.MaxFiles; i++ {
		if err := d.AddFile(r.ctx, "a.pdf", "application/pdf", strings.NewReader(pdf)); err != nil {
			t.Fatal(err)
		}
	}
	wantCode(t, d.AddFile(r.ctx, "a.pdf", "application/pdf", strings.NewReader(pdf)), panel.CodeChatTooManyFiles)
}

// CA-CONV-02: an answer for a user who has no panel open is kept and shown when he opens it, with
// the unread counter; opening the conversation reads it, and nobody else's counter moves.
func TestAnAnswerKeptForAnAbsentUserShowsWithTheUnreadCounter(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	sent, err := r.send(r.bia, in("loja", "e então?", "msg-0000014"))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.svc.Channel().Send(r.ctx, r.reply(r.bia, inbox.ChannelPanel, "loja", "a mesa abriu")); err != nil {
		t.Fatal(err)
	}
	r.reply(r.bia, inbox.ChannelPanel, "loja", "e já tem dois testes")
	r.reply(r.ana, inbox.ChannelPanel, "loja", "isto é da Ana")
	ov, err := r.svc.Overview(r.ctx, r.bia)
	if err != nil || ov.Unread != 2 || len(ov.Workspaces) != 1 || ov.Workspaces[0].Slug != "loja" || ov.Workspaces[0].Unread != 2 || !ov.Workspaces[0].CanTalk {
		t.Fatalf("overview %+v, %v", ov, err)
	}
	if n, _ := r.svc.Unread(r.ctx, r.bia); n != 2 {
		t.Fatalf("unread %d", n)
	}
	c := r.conv(r.bia, "loja")
	if c.Unread != 2 || len(c.Entries) != 3 || c.Entries[0].ID != sent.ID || !c.Entries[2].Unread || c.Entries[2].Text != "e já tem dois testes" {
		t.Fatalf("conversation %+v", c)
	}
	if err := r.svc.Read(r.ctx, r.bia, "loja"); err != nil {
		t.Fatal(err)
	}
	if c := r.conv(r.bia, "loja"); c.Unread != 0 || c.Entries[2].Unread {
		t.Fatalf("after reading %+v", c)
	}
	if n, _ := r.svc.Unread(r.ctx, r.ana); n != 1 {
		t.Fatalf("Ana's unread %d", n)
	}
	r.reply(r.bia, inbox.ChannelPanel, "loja", "mais uma")
	if n, _ := r.svc.Unread(r.ctx, r.bia); n != 1 {
		t.Fatalf("a new answer after the read: %d", n)
	}
}

// Part of CA-CONV-01: the channel of the panel takes only the answers of the panel; the
// Telegram's answers are not its, and what is shown of the Telegram does not count as unread.
func TestThePanelChannelTakesOnlyTheAnswersOfThePanel(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	ch := r.svc.Channel()
	if err := ch.Send(r.ctx, inbox.Outbound{Channel: inbox.ChannelTelegram, User: r.bia.ID, Workspace: "loja"}); err != inbox.ErrChannelDown {
		t.Fatalf("a Telegram answer: %v", err)
	}
	if err := ch.Send(r.ctx, inbox.Outbound{Channel: inbox.ChannelPanel, User: r.bia.ID, Workspace: "loja"}); err != nil {
		t.Fatal(err)
	}
	if ev := r.heard(); len(ev) != 1 || ev[0].User != r.bia.ID || ev[0].Workspace != "loja" || ev[0].Kind != panel.EventChatChanged {
		t.Fatalf("events %+v", ev)
	}
}

// The history has also the messages the user exchanged on the Telegram with the same workspace,
// marked with their channel, and nothing of anybody else, of another workspace or of another kind.
func TestTheHistoryHasTheTelegramMessagesOfTheUserOnly(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	add := func(u users.User, ws, channel, text string) {
		t.Helper()
		_, err := r.ib.Insert(r.ctx, inbox.NewItem{Workspace: ws, Target: "gm-" + ws, Kind: inbox.KindUser, Sender: u.Name, Text: text,
			Origin: &inbox.Origin{Channel: channel, User: u.ID, UserName: u.Name, Level: characters.UserLevel(u.Level), Conversation: "77", Message: "5"}}, r.clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		r.clk.Advance(time.Second)
	}
	add(r.bia, "loja", inbox.ChannelTelegram, "pelo telegram")
	add(r.ana, "loja", inbox.ChannelTelegram, "da Ana")
	add(r.owner, "blog", inbox.ChannelTelegram, "do blog")
	if _, err := r.ib.Insert(r.ctx, inbox.NewItem{Workspace: "loja", Target: "gm-loja", Kind: inbox.KindSystem, Text: "aviso do motor"}, r.clk.Now()); err != nil {
		t.Fatal(err)
	}
	r.reply(r.bia, inbox.ChannelTelegram, "loja", "resposta no telegram")
	if _, err := r.send(r.bia, in("loja", "pelo painel", "msg-0000015")); err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(time.Second)
	r.reply(r.bia, inbox.ChannelPanel, "loja", "resposta no painel")
	c := r.conv(r.bia, "loja")
	var got []string
	for _, e := range c.Entries {
		got = append(got, e.Channel+":"+e.Text)
	}
	want := "telegram:pelo telegram|telegram:resposta no telegram|panel:pelo painel|panel:resposta no painel"
	if strings.Join(got, "|") != want {
		t.Fatalf("history %v", got)
	}
	if c.Unread != 1 || c.Entries[1].Unread {
		t.Fatalf("only the answer of the panel is unread: %+v", c)
	}
}

// What leaves is masked: the answer of the Grão-Mestre and the question, even a secret the agent
// wrote into them.
func TestEveryTextThatLeavesIsMasked(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	const token = "ghp_abcdefghijklmnopqrstuvwxyz0123456789ABCD"
	r.reply(r.bia, inbox.ChannelPanel, "loja", "o token é "+token)
	if _, err := r.svc.AskChoice(r.ctx, panelchat.Question{Workspace: "loja", Asker: "gm-loja", UserID: r.bia.ID, Text: "Usar " + token + "?", Options: []string{"sim " + token, "não"}}, 1); err != nil {
		t.Fatal(err)
	}
	if err := r.svc.SetProgress(r.ctx, "loja", "2 mesas\n- "+token); err != nil {
		t.Fatal(err)
	}
	c := r.conv(r.bia, "loja")
	var all strings.Builder
	for _, e := range c.Entries {
		all.WriteString(e.Text)
		if e.Question != nil {
			all.WriteString(strings.Join(e.Question.Options, " "))
		}
	}
	all.WriteString(strings.Join(c.Progress.Lines, " "))
	if strings.Contains(all.String(), token) {
		t.Fatalf("a secret left: %s", all.String())
	}
}

// CA-CONV-09: the question of `mestre choose` shows one button per option, only to the user asked;
// the click delivers the choice once and a second click is refused. The message of progress is one
// card, updated in place.
func TestAChoiceGoesBackOnceAndTheProgressIsOneCard(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	id, err := r.svc.AskChoice(r.ctx, panelchat.Question{Workspace: "loja", Mesa: "42-login", Asker: "gm-loja", UserID: r.bia.ID, Text: "Qual banco?", Options: []string{"sqlite", "postgres", "mysql"}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	c := r.conv(r.bia, "loja")
	if len(c.Entries) != 1 || c.Entries[0].From != "question" || c.Entries[0].Channel != "panel" || c.Entries[0].Table != "42-login" {
		t.Fatalf("entries %+v", c.Entries)
	}
	q := c.Entries[0].Question
	if q.ID != id || q.State != "open" || strings.Join(q.Options, ",") != "postgres,sqlite,mysql" { // the recommended first
		t.Fatalf("question %+v", q)
	}
	if got := r.conv(r.ana, "loja"); len(got.Entries) != 0 {
		t.Fatalf("another user sees the question: %+v", got.Entries)
	}
	wantCode(t, r.svc.Choose(r.ctx, r.ana, id, 0), panel.CodeChatChoiceUnknown)
	wantCode(t, r.svc.Choose(r.ctx, r.bia, id, 9), panel.CodeBadRequest)
	if err := r.svc.Choose(r.ctx, r.bia, id, 2); err != nil {
		t.Fatal(err)
	}
	wantCode(t, r.svc.Choose(r.ctx, r.bia, id, 1), panel.CodeChatChoiceTaken)
	var answers []inbox.Item
	for _, it := range r.items("loja", r.bia) {
		if it.Origin.Choice == id {
			answers = append(answers, it)
		}
	}
	if len(answers) != 1 || answers[0].Origin.Channel != inbox.ChannelPanel || answers[0].Origin.Table != "42-login" || !strings.Contains(answers[0].Text, `"mysql"`) ||
		!strings.Contains(answers[0].Text, "Qual banco?") {
		t.Fatalf("answers %+v", answers)
	}
	if q := r.conv(r.bia, "loja").Entries[0].Question; q == nil || q.State != "answered" || q.Answer != "mysql" {
		t.Fatalf("question after %+v", q)
	}
	// a question of the Telegram is not answered through the panel
	if _, err := r.db.Writer().ExecContext(r.ctx, `INSERT INTO choose_questions (workspace, mesa, asker, user_id, question, options, created_at, channel) VALUES ('loja', '', 'gm-loja', ?, 'q', '["a","b"]', 1, 'telegram')`, r.bia.ID); err != nil {
		t.Fatal(err)
	}
	var tg int64
	_ = r.db.Reader().QueryRowContext(r.ctx, `SELECT MAX(id) FROM choose_questions`).Scan(&tg)
	wantCode(t, r.svc.Choose(r.ctx, r.bia, tg, 0), panel.CodeChatChoiceUnknown)
	// the user who may not give orders, or does not act in the workspace, is not asked
	if _, err := r.svc.AskChoice(r.ctx, panelchat.Question{Workspace: "loja", UserID: r.otto.ID, Text: "q", Options: []string{"a", "b"}}, 1); err == nil {
		t.Fatal("the Observer was asked")
	}
	if _, err := r.svc.AskChoice(r.ctx, panelchat.Question{Workspace: "loja", UserID: r.rui.ID, Text: "q", Options: []string{"a", "b"}}, 1); err == nil {
		t.Fatal("a user of another workspace was asked")
	}

	// progress: one card, in place, only for the Owner and the Administrators
	if err := r.svc.SetProgress(r.ctx, "loja", "2 mesas, 1 trabalhando\n- Mesa 42: aguardando você"); err != nil {
		t.Fatal(err)
	}
	r.clk.Advance(time.Minute)
	if err := r.svc.SetProgress(r.ctx, "loja", "2 mesas, 2 trabalhando"); err != nil {
		t.Fatal(err)
	}
	p := r.conv(r.bia, "loja").Progress
	if p == nil || len(p.Lines) != 1 || p.Lines[0] != "2 mesas, 2 trabalhando" {
		t.Fatalf("progress %+v", p)
	}
	var cards int
	_ = r.db.Reader().QueryRowContext(r.ctx, `SELECT COUNT(*) FROM panel_chat_progress WHERE workspace = 'loja'`).Scan(&cards)
	if cards != 1 || r.conv(r.ana, "loja").Progress != nil || r.conv(r.owner, "loja").Progress == nil {
		t.Fatalf("cards %d", cards)
	}
	// the same text again does not touch the card
	before := len(r.heard())
	if err := r.svc.SetProgress(r.ctx, "loja", "2 mesas, 2 trabalhando"); err != nil || len(r.heard()) != before {
		t.Fatalf("a repeat of the message moved the card: %v", err)
	}
}

// The state of the Grão-Mestre at the top: idle, busy, with the items ahead, unavailable with the
// reason, and the lock; each change goes to those who follow the workspace.
func TestTheStateOfTheGrandMasterIsAtTheTop(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	if st := r.conv(r.bia, "loja").Status; st.State != "idle" || st.Ahead != 0 || st.Locked {
		t.Fatalf("status %+v", st)
	}
	r.setGM(characters.Busy, "")
	if _, err := r.send(r.ana, in("loja", "um", "msg-0000016")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.send(r.ana, in("loja", "dois", "msg-0000017")); err != nil {
		t.Fatal(err)
	}
	if st := r.conv(r.bia, "loja").Status; st.State != "busy" || st.Ahead != 2 {
		t.Fatalf("status %+v", st)
	}
	r.setGM(characters.Interrupted, "o provedor caiu")
	if st := r.conv(r.bia, "loja").Status; st.State != "unavailable" || st.Reason != "o provedor caiu" {
		t.Fatalf("status %+v", st)
	}
	r.setLocked(true)
	if st := r.conv(r.bia, "loja").Status; !st.Locked {
		t.Fatalf("status %+v", st)
	}
	sent, err := r.send(r.bia, in("loja", "guarde", "msg-0000018"))
	if err != nil || !sent.Held {
		t.Fatalf("with the system locked: %+v, %v", sent, err)
	}
	r.mu.Lock()
	r.events = nil
	r.mu.Unlock()
	r.setGM(characters.Idle, "")
	r.svc.Changed(r.ctx, "gm-loja")
	seen := map[string]bool{}
	for _, e := range r.heard() {
		if e.Kind != panel.EventChatStatus || e.Workspace != "loja" || e.Status.State != "idle" || !e.Status.Locked {
			t.Fatalf("event %+v", e)
		}
		seen[e.User] = true
	}
	if !seen[r.bia.ID] || !seen[r.ana.ID] || !seen[r.otto.ID] || !seen[r.owner.ID] || seen[r.rui.ID] {
		t.Fatalf("heard by %v", seen)
	}
}

// The files the Grão-Mestre sends are downloads only for the user they were sent to; without the
// way to open them yet, they show by name only.
func TestTheDownloadsOfTheGrandMasterAreOnlyForTheUserTheyWereSentTo(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	o := r.reply(r.bia, inbox.ChannelPanel, "loja", "veja", "docs/plano.pdf")
	if e := r.conv(r.bia, "loja").Entries[0]; len(e.Attachments) != 1 || e.Attachments[0].Name != "plano.pdf" || e.Attachments[0].Download != "" {
		t.Fatalf("entry %+v", e)
	}
	_, err := r.svc.File(r.ctx, r.bia, "loja", o.ID, 0)
	wantCode(t, err, panel.CodeChatFileGone)
	r = newRig(t, func(o *panelchat.Options) {
		o.Open = func(_ context.Context, _ string, out inbox.Outbound, i int) (panel.ChatDownload, error) {
			return panel.ChatDownload{Name: path.Base(out.Attachments[i]), Size: 3, Body: io.NopCloser(strings.NewReader("pdf"))}, nil
		}
	})
	o = r.reply(r.bia, inbox.ChannelPanel, "loja", "veja", "docs/plano.pdf")
	if e := r.conv(r.bia, "loja").Entries[0]; e.Attachments[0].Download != "/api/chat/loja/files/"+itoa(o.ID)+"/0" {
		t.Fatalf("entry %+v", e)
	}
	d, err := r.svc.File(r.ctx, r.bia, "loja", o.ID, 0)
	if err != nil || d.Name != "plano.pdf" {
		t.Fatalf("download %+v, %v", d, err)
	}
	_ = d.Body.Close()
	for _, u := range []users.User{r.ana, r.owner} {
		if _, err := r.svc.File(r.ctx, u, "loja", o.ID, 0); err == nil {
			t.Fatalf("%s downloaded the file of Bia", u.Name)
		}
	}
	if _, err := r.svc.File(r.ctx, r.bia, "loja", o.ID, 3); err == nil {
		t.Fatal("an index that is not there")
	}
}

// Too many messages in a minute are refused; the minute goes by and they are taken again.
func TestTooManyMessagesInAMinuteAreRefused(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	for i := 0; i < 30; i++ {
		if _, err := r.send(r.bia, in("loja", "m", "msg-rate-"+itoa(int64(i)+1000))); err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
	}
	_, err := r.send(r.bia, in("loja", "m", "msg-rate-final-1"))
	wantCode(t, err, panel.CodeRateLimited)
	if _, err := r.send(r.ana, in("loja", "m", "msg-rate-final-2")); err != nil {
		t.Fatalf("another user is limited too: %v", err)
	}
	r.clk.Advance(61 * time.Second)
	if _, err := r.send(r.bia, in("loja", "m", "msg-rate-final-3")); err != nil {
		t.Fatalf("after the minute: %v", err)
	}
}
