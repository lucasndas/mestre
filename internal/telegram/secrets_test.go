package telegram_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/logging"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/telegram"
	"github.com/lucasndas/mestre/internal/testkit/faketelegram"
	"github.com/lucasndas/mestre/internal/users"
)

// typing is the terminal of the Grand Master: it records what the engine types in it.
type typing struct {
	mu  sync.Mutex
	got []protocol.InboxDeliver
}

func (f *typing) Deliver(_ context.Context, _ string, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.got = append(f.got, d)
	return protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxDelivered}, nil
}

func (f *typing) texts() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.got))
	for i, d := range f.got {
		out[i] = d.Header + "\n" + d.Text
	}
	return out
}

// toInbox is what the engine does with a routed message: it goes to the inbox of
// the Grand Master (the inboxDeliverer of internal/app).
type toInbox struct {
	svc *inbox.Service
	gm  string
}

func (d toInbox) Deliver(ctx context.Context, m telegram.Delivery) error {
	_, err := d.svc.Enqueue(ctx, telegram.NewItem(m, d.gm))
	return err
}

// secretWorld is the Telegram (fake) over the real routing, the real inbox and the
// fake terminal of the Grand Master.
type secretWorld struct {
	*world
	svc  *inbox.Service
	gm   characters.Character
	term *typing
	logs *bytes.Buffer
	ana  faketelegram.User
}

func newSecretWorld(t *testing.T) *secretWorld {
	t.Helper()
	w := newWorld(t, rigOpts{routing: true, mask: logging.NewMasker().Mask})
	sw := &secretWorld{world: w, term: &typing{}, logs: &bytes.Buffer{}}
	// the bot again, with a log to look at and the machine of the audit
	w.opts.Logger = slog.New(slog.NewTextHandler(sw.logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	w.opts.Machine = "m1"
	var err error
	if w.bot, err = telegram.New(w.opts); err != nil {
		t.Fatal(err)
	}
	w.routing.Bind(w.bot)
	replies := &telegram.Replies{}
	replies.Bind(w.bot)
	reg := characters.Registry{DB: w.db}
	sw.gm = characters.Character{ID: "gm1", Name: characters.NameGrandMaster, Kind: characters.KindCharacter, Role: characters.RoleGrandMaster, Workspace: "loja",
		Machine: "m1", TerminalID: "t1", Sheet: "Dev Claude", Base: "claude-code", Class: "Dev", State: characters.Idle, Lifecycle: characters.Persistent,
		CreatedBy: characters.Creator{Kind: characters.CreatedByEngine}, CreatedAt: w.clk.Now()}
	if err := reg.Insert(w.ctx, sw.gm); err != nil {
		t.Fatal(err)
	}
	if sw.svc, err = inbox.NewService(inbox.Options{DB: &inbox.Store{DB: w.db}, Clock: w.clk, Registry: reg, Deliverer: sw.term, Channels: replies, Logger: slog.New(slog.NewTextHandler(sw.logs, nil))}); err != nil {
		t.Fatal(err)
	}
	w.routing.To = toInbox{sw.svc, sw.gm.ID}
	// the Owner, with his Telegram, working in the workspace of the Grand Master
	code, err := w.users.NewCode(w.ctx, w.owner, w.owner.ID)
	if err != nil {
		t.Fatal(err)
	}
	sw.ana = faketelegram.User{ID: 77, FirstName: "Lucas", Username: "lucas"}
	if _, err := w.users.Pair(w.ctx, users.PairRequest{Text: code.Code, TelegramID: sw.ana.ID, Private: true}); err != nil {
		t.Fatal(err)
	}
	if err := w.bot.Store().SetActive(w.ctx, w.owner.ID, "loja"); err != nil {
		t.Fatal(err)
	}
	return sw
}

// said makes the Owner write; the message is the one the Grand Master is given
// the terminal of, and the turn that handles it begins.
func (sw *secretWorld) said(text string) (messageID int64) {
	sw.t.Helper()
	if _, err := sw.fake.UserSendsText(sw.ana, faketelegram.Private(sw.ana), text); err != nil {
		sw.t.Fatal(err)
	}
	sw.poll()
	items, err := sw.svc.Store().All(sw.ctx, sw.gm.ID)
	if err != nil {
		sw.t.Fatal(err)
	}
	if len(items) == 0 || items[len(items)-1].Origin == nil {
		sw.t.Fatalf("the message of the Owner is not in the inbox: %+v", items)
	}
	id, err := strconv.ParseInt(items[len(items)-1].Origin.Message, 10, 64)
	if err != nil {
		sw.t.Fatal(err)
	}
	for _, it := range items {
		if it.State == inbox.Delivering {
			if err := sw.svc.Confirmed(sw.ctx, it.ID); err != nil {
				sw.t.Fatal(err)
			}
		}
	}
	sw.drain()
	return id
}

// nothingOf says the text is in no table of the engine and no log, and in no
// terminal when it never reached one (what the Grand Master was already typed
// cannot be taken back).
func (sw *secretWorld) nothingOf(secret string, terminalToo bool) {
	sw.t.Helper()
	for _, table := range []string{"inbox_messages", "outbound_messages", "telegram_updates", "telegram_held", "telegram_pending", "telegram_outbox", "bot_messages", "audit_log"} {
		rows, err := sw.db.Reader().QueryContext(sw.ctx, "SELECT * FROM "+table)
		if err != nil {
			sw.t.Fatal(err)
		}
		cols, _ := rows.Columns()
		for rows.Next() {
			vals := make([]sql.RawBytes, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				sw.t.Fatal(err)
			}
			for i, v := range vals {
				if bytes.Contains(v, []byte(secret)) {
					sw.t.Errorf("%s.%s holds the secret %q", table, cols[i], secret)
				}
			}
		}
		rows.Close()
	}
	if strings.Contains(sw.logs.String(), secret) {
		sw.t.Errorf("the log holds the secret %q", secret)
	}
	for _, typed := range sw.term.texts() {
		if terminalToo && strings.Contains(typed, secret) {
			sw.t.Errorf("the Grand Master received the secret %q: %q", secret, typed)
		}
	}
}

// audits are the rows of the audit trail, as "action detail".
func (sw *secretWorld) audits() []string {
	sw.t.Helper()
	rows, err := sw.db.Reader().QueryContext(sw.ctx, `SELECT action, detail FROM audit_log ORDER BY at, id`)
	if err != nil {
		sw.t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a, d string
		if err := rows.Scan(&a, &d); err != nil {
			sw.t.Fatal(err)
		}
		out = append(out, a+" "+d)
	}
	return out
}

// toOwner are the texts the bot sent to the Owner, in order.
func (sw *secretWorld) toOwner() []string {
	var out []string
	for _, s := range sw.sent("sendMessage") {
		if s.ChatID == sw.ana.ID {
			out = append(out, s.Text)
		}
	}
	return out
}

// CA-COFRE-15
func TestASecretSentByTheOwnerNeverReachesTheGrandMasterOrTheHistory(t *testing.T) {
	t.Parallel()
	sw := newSecretWorld(t)

	// a password in a phrase: the message is deleted, nothing of it is kept, the
	// Grand Master gets only the notice
	first := sw.said("cadastre o site x, a senha é Abc123!")
	if del := sw.fake.Deleted(); len(del) != 1 || del[0].MessageID != first || del[0].ChatID != sw.ana.ID {
		t.Fatalf("deleted %+v, want the message %d", del, first)
	}
	typed := sw.term.texts()
	if len(typed) != 1 || !strings.Contains(typed[0], telegram.HeldNotice) {
		t.Fatalf("the Grand Master was typed %q, want only the notice", typed)
	}
	sw.nothingOf("Abc123", true)
	wantAudit := func(n int) {
		t.Helper()
		rows := sw.audits()
		if len(rows) != n {
			t.Fatalf("audit = %q, want %d rows", rows, n)
		}
		for _, r := range rows {
			if !strings.HasPrefix(r, inbox.AuditSecretBlocked+" ") || !strings.Contains(r, "tentativa de envio de segredo bloqueada") || !strings.Contains(r, sw.owner.ID) {
				t.Fatalf("audit row %q is not only the blocked attempt of the Owner", r)
			}
		}
	}
	wantAudit(1)
	if got := sw.toOwner(); len(got) != 1 || !strings.Contains(got[0], "troque essa senha") {
		t.Fatalf("the Owner was told %q, want the advice to change the password", got)
	}

	// a token of a known format: the Grand Master gets it without the piece, the
	// author the advice to rotate it
	sw.said("token ghp_abcdefghijklmnopqrstuvwxyz0123456789")
	typed = sw.term.texts()
	if len(typed) != 2 || !strings.HasSuffix(typed[1], "token [segredo removido]") {
		t.Fatalf("the Grand Master was typed %q, want `token [segredo removido]`", typed)
	}
	if del := sw.fake.Deleted(); len(del) != 2 {
		t.Fatalf("deleted %+v, want both messages", del)
	}
	sw.nothingOf("ghp_abcdefghijklmnopqrstuvwxyz0123456789", true)
	wantAudit(2)
	if got := sw.toOwner(); len(got) != 2 || !strings.Contains(got[1], "Troque esse segredo") {
		t.Fatalf("the Owner was told %q, want the advice to rotate the token", got)
	}

	// a password in free text only the Grand Master notices: his fake agent
	// answers it with `reply --redact-source`
	free := sw.said("o acesso do site y é lucas com a palavra bananaAzul")
	if len(sw.term.texts()) != 3 || !strings.Contains(sw.term.texts()[2], "bananaAzul") {
		t.Fatalf("the free text was not delivered as it came: %q", sw.term.texts())
	}
	item, ok, err := sw.svc.ByKey(sw.ctx, fmt.Sprintf("telegram:%d:%d", sw.ana.ID, free))
	if err != nil || !ok {
		t.Fatalf("the item of the message: %v, %v", ok, err)
	}
	refusal := "Não aceito credenciais por aqui: cadastre pelo painel e troque essa senha, que já passou pelo Telegram e pela IA."
	resp := sw.svc.Handlers()["reply"].Handle(sw.ctx, &localchan.Call{
		Caller: localchan.Caller{Identified: true, AgentID: sw.gm.ID},
		Request: localchan.Request{Cmd: "reply", Args: localchan.TextArgs(refusal),
			Flags: map[string]any{"redact-source": true, "to-message": item.ID}}})
	if !resp.OK {
		t.Fatalf("reply --redact-source = %+v", resp)
	}
	sw.drain()
	if del := sw.fake.Deleted(); len(del) != 3 || del[2].MessageID != free || del[2].ChatID != sw.ana.ID {
		t.Fatalf("deleted %+v, want the free text too", del)
	}
	if _, ok, _ := sw.svc.ByKey(sw.ctx, fmt.Sprintf("telegram:%d:%d", sw.ana.ID, free)); ok {
		t.Error("the message is still in the history of the inbox")
	}
	sw.nothingOf("bananaAzul", false)
	wantAudit(3)
	if got := sw.toOwner(); len(got) != 3 || !strings.Contains(got[2], refusal) {
		t.Fatalf("the Owner was told %q, want the refusal with the advice to change the password", got)
	}
	if strings.Contains(sw.logs.String(), "level=ERROR") {
		t.Errorf("the log has errors: %s", sw.logs)
	}
}
