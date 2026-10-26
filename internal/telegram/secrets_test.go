package telegram_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mestre-ia/mestre/internal/localchan"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/telegram"
	"github.com/mestre-ia/mestre/internal/testkit/faketelegram"
	"github.com/mestre-ia/mestre/internal/users"
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
	sw.clk.Advance(6 * time.Minute) // the message after a held phrase is held too, for a while
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

// rebuild makes the bot again over the same database, with a change of options.
func (sw *secretWorld) rebuild(mut func(*telegram.Options)) {
	sw.t.Helper()
	mut(&sw.opts)
	var err error
	if sw.bot, err = telegram.New(sw.opts); err != nil {
		sw.t.Fatal(err)
	}
	sw.routing.Bind(sw.bot)
}

// CA-COFRE-15: the secret of an update recorded and not handled yet (the Citadel
// could not act when it came) is not in the queue of updates, and is blocked when
// the bot may act: deleted, audited and the author told.
func TestAnUpdateWithASecretRecordedAndNotHandledIsStillBlocked(t *testing.T) {
	t.Parallel()
	sw := newSecretWorld(t)
	g := &stopAfter{n: 100}
	sw.rebuild(func(o *telegram.Options) { o.Gate = g })
	for i, secret := range []string{"Abc123", "ghp_abcdefghijklmnopqrstuvwxyz0123456789"} {
		text := "a senha é Abc123!"
		if i == 1 {
			text = "token " + secret
		}
		g.n = 1 // the poll starts, and the gate closes right after the update is recorded
		if _, err := sw.fake.UserSendsText(sw.ana, faketelegram.Private(sw.ana), text); err != nil {
			t.Fatal(err)
		}
		sw.poll()
		var pending int
		if err := sw.db.Reader().QueryRowContext(sw.ctx, `SELECT COUNT(*) FROM telegram_updates WHERE state = 'new'`).Scan(&pending); err != nil || pending != 1 {
			t.Fatalf("pending updates = %d, %v", pending, err)
		}
		// the queue of updates, before anything marks it done: the copy that was kept
		sw.nothingOf(secret, true)
		if len(sw.fake.Deleted()) != i {
			t.Fatalf("deleted %+v while the Citadel could not act", sw.fake.Deleted())
		}
		g.n = 100 // the Citadel acts again
		sw.poll()
		sw.drain()
		if del := sw.fake.Deleted(); len(del) != i+1 || del[i].ChatID != sw.ana.ID {
			t.Fatalf("deleted %+v, want the message of the chat", del)
		}
		sw.nothingOf(secret, true)
	}
	if rows := sw.audits(); len(rows) != 2 {
		t.Fatalf("audit = %q, want one row per message", rows)
	}
	if got := sw.toOwner(); len(got) != 2 || !strings.Contains(got[0], "troque essa senha") || !strings.Contains(got[1], "Troque esse segredo") {
		t.Fatalf("the Owner was told %q", got)
	}
	if len(sw.term.texts()) != 0 {
		t.Fatalf("the Grand Master was typed %q", sw.term.texts())
	}
}

// CA-COFRE-15: the phrases of a password are read after the text is normalized
// (full-width letters, zero-width characters) and cover the common ways to say it.
func TestThePasswordPhrasesAreNotEasyToBypass(t *testing.T) {
	t.Parallel()
	sw := newSecretWorld(t)
	phrases := []string{
		"a senha do Gmail é Zk93!qx",
		"senha=Zk93!qx",
		"pwd Zk93!qx",
		"password is Zk93!qx",
		"\U0000FF4D\U0000FF49\U0000FF4E\U0000FF48\U0000FF41 senha Zk93!qx",
		"a sen\U0000200bha é Zk93!qx",
		"minha\U000000a0senha\U0000FF1A Zk93!qx",
		"senha\U0000FF1A Zk93!qx",
	}
	for i, ph := range phrases {
		sw.clk.Advance(6 * time.Minute) // each phrase on its own, not as the follow-up of the one before
		sw.said(ph)
		if got := len(sw.fake.Deleted()); got != i+1 {
			t.Fatalf("%q: %d messages deleted, want %d", ph, got, i+1)
		}
		typed := sw.term.texts()
		if len(typed) != i+1 || !strings.Contains(typed[i], telegram.HeldNotice) {
			t.Fatalf("%q: the Grand Master was typed %q, want only the notice", ph, typed)
		}
	}
	sw.nothingOf("Zk93", true)
}

// CA-COFRE-15: after a held phrase, the next message of the author is held too,
// for a few minutes ("minha senha:" and then the value).
func TestTheMessageAfterAHeldPasswordPhraseIsHeldToo(t *testing.T) {
	t.Parallel()
	sw := newSecretWorld(t)
	sw.said("minha senha:")
	sw.said("Qw7-pelicano-azul")
	if got := len(sw.fake.Deleted()); got != 2 {
		t.Fatalf("%d messages deleted, want both", got)
	}
	sw.nothingOf("pelicano", true)
	sw.said("obrigado, pode seguir") // the wait ended with one message
	if typed := sw.term.texts(); len(typed) != 3 || !strings.HasSuffix(typed[2], "obrigado, pode seguir") {
		t.Fatalf("the Grand Master was typed %q", typed)
	}
	sw.said("a senha é X1")
	sw.clk.Advance(6 * time.Minute)
	sw.said("fora do prazo")
	if typed := sw.term.texts(); !strings.HasSuffix(typed[len(typed)-1], "fora do prazo") {
		t.Fatalf("a message after the wait was held: %q", typed)
	}
}

// CA-COFRE-15: the filter comes before every branch: a command, the free text of
// a capture, a user who may not talk to the Grand Master and a stranger.
func TestTheSecretFilterComesBeforeEveryBranch(t *testing.T) {
	t.Parallel()
	var captured []string
	var mu sync.Mutex
	strangers := &strangerLog{}
	sw := newSecretWorld(t)
	sw.rebuild(func(o *telegram.Options) {
		o.Strangers = strangers
		o.Captures = map[string]telegram.CaptureFunc{"free": func(_ context.Context, _ users.User, _, text string) (string, bool, error) {
			mu.Lock()
			defer mu.Unlock()
			captured = append(captured, text)
			return "ok", false, nil
		}}
	})
	say := func(u faketelegram.User, text string) {
		t.Helper()
		if _, err := sw.fake.UserSendsText(u, faketelegram.Private(u), text); err != nil {
			t.Fatal(err)
		}
		sw.poll()
		sw.drain()
	}

	// a command with a secret: not run, not echoed, deleted and audited
	say(sw.ana, "/status senha: Cmd9pass")
	if got := len(sw.fake.Deleted()); got != 1 {
		t.Fatalf("%d messages deleted, want 1", got)
	}
	for _, s := range sw.toOwner() {
		if strings.Contains(s, "Cmd9pass") {
			t.Fatalf("the secret was echoed: %q", s)
		}
	}
	if len(sw.audits()) != 1 {
		t.Fatalf("audit = %q", sw.audits())
	}

	// the free text of a capture that is not a PIN
	if err := sw.bot.Ask(sw.ctx, sw.owner.ID, "free", "r", "Diga", time.Hour); err != nil {
		t.Fatal(err)
	}
	sw.drain()
	sw.clk.Advance(6 * time.Minute)
	say(sw.ana, "a senha é Cap7word")
	mu.Lock()
	got := captured
	mu.Unlock()
	for _, c := range got {
		if strings.Contains(c, "Cap7word") {
			t.Fatalf("the capture received the secret: %q", c)
		}
	}
	if len(sw.audits()) != 2 {
		t.Fatalf("audit = %q, want the capture too", sw.audits())
	}

	// a user who may not talk to the Grand Master
	_, obs := sw.user("Obs", users.Observer)
	say(obs, "minha senha é Obs5word")
	if len(sw.audits()) != 3 || len(sw.fake.Deleted()) != 3 {
		t.Fatalf("audit = %q, deleted %+v, want the observer's message too", sw.audits(), sw.fake.Deleted())
	}

	// a stranger: the Owner never reads the raw text
	str := faketelegram.User{ID: 9001, FirstName: "Estranho"}
	say(str, "o login é eu e a senha é Str3word")
	say(str, "ghp_abcdefghijklmnopqrstuvwxyz0123456789")
	for _, tx := range strangers.texts() {
		if strings.Contains(tx, "Str3word") || strings.Contains(tx, "ghp_abcdef") {
			t.Fatalf("the Owner was given the secret of a stranger: %q", tx)
		}
	}
	if len(strangers.texts()) != 2 || len(sw.audits()) != 5 {
		t.Fatalf("strangers %q, audit %q", strangers.texts(), sw.audits())
	}
	sw.nothingOf("Cmd9pass", true)
	sw.nothingOf("Cap7word", true)
	sw.nothingOf("Obs5word", true)
	sw.nothingOf("Str3word", true)
}

type strangerLog struct {
	mu  sync.Mutex
	got []string
}

func (l *strangerLog) Stranger(_ context.Context, s telegram.Stranger) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.got = append(l.got, s.Text)
}

func (l *strangerLog) texts() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string(nil), l.got...)
}

// reply runs `mestre reply` as the Grand Master.
func (sw *secretWorld) reply(text string, flags map[string]any) localchan.Response {
	return sw.svc.Handlers()["reply"].Handle(sw.ctx, &localchan.Call{
		Caller:  localchan.Caller{Identified: true, AgentID: sw.gm.ID},
		Request: localchan.Request{Cmd: "reply", Args: localchan.TextArgs(text), Flags: flags}})
}

// CA-COFRE-15: `reply --redact-source` always names the message; it is done once
// (a repeat finds nothing to delete); its answer never quotes the secret and the
// engine adds the advice to rotate it.
func TestRedactSourceNeedsTheMessageIsDoneOnceAndDoesNotQuoteTheSecret(t *testing.T) {
	t.Parallel()
	sw := newSecretWorld(t)
	a := sw.said("o acesso do site y é lucas com a palavra bananaAzul")
	b := sw.said("pode abrir a mesa de testes, por favor")
	item, ok, err := sw.svc.ByKey(sw.ctx, fmt.Sprintf("telegram:%d:%d", sw.ana.ID, a))
	if err != nil || !ok {
		t.Fatalf("the item: %v, %v", ok, err)
	}

	// without --to-message the "current" message would be the last one (b): refused
	resp := sw.reply("Recuse", map[string]any{"redact-source": true})
	if resp.OK || resp.Error == nil || resp.Error.Code != localchan.CodeUsage {
		t.Fatalf("reply --redact-source without --to-message = %+v", resp)
	}
	if len(sw.fake.Deleted()) != 0 || len(sw.audits()) != 0 {
		t.Fatal("something was deleted or audited by a refused call")
	}

	// the answer that quotes what was said: the run is taken out
	resp = sw.reply("Vi a palavra bananaAzul no seu texto; cadastre pelo painel.", map[string]any{"redact-source": true, "to-message": item.ID})
	if !resp.OK {
		t.Fatalf("reply = %+v", resp)
	}
	sw.drain()
	told := sw.toOwner()
	if len(told) == 0 || strings.Contains(told[len(told)-1], "bananaAzul") || !strings.Contains(told[len(told)-1], "Troque essa senha") {
		t.Fatalf("the Owner was told %q, want no quote and the advice to rotate", told)
	}
	sw.nothingOf("bananaAzul", false)

	// the same request again: nothing more is deleted or audited
	resp = sw.reply("Recuse", map[string]any{"redact-source": true, "to-message": item.ID})
	if resp.OK {
		t.Fatalf("the repeated request worked: %+v", resp)
	}
	if del := sw.fake.Deleted(); len(del) != 1 || del[0].MessageID != a {
		t.Fatalf("deleted %+v, want only the first message", del)
	}
	if len(sw.audits()) != 1 {
		t.Fatalf("audit = %q, want one row", sw.audits())
	}
	// the message after it is still there
	if _, ok, _ := sw.svc.ByKey(sw.ctx, fmt.Sprintf("telegram:%d:%d", sw.ana.ID, b)); !ok {
		t.Fatal("the other message left the inbox")
	}
}

// CA-COFRE-15: DeleteSource checks what Send checks.
func TestDeleteSourceChecksTheChannelAndTheChatOfTheUser(t *testing.T) {
	t.Parallel()
	sw := newSecretWorld(t)
	r := &telegram.Replies{Users: sw.users}
	origin := inbox.Origin{Channel: inbox.ChannelTelegram, User: sw.owner.ID, Conversation: strconv.FormatInt(sw.ana.ID, 10), Message: "5"}
	if err := r.DeleteSource(sw.ctx, origin); !errors.Is(err, inbox.ErrChannelDown) {
		t.Fatalf("unbound: %v", err)
	}
	r.Bind(sw.bot)
	other := origin
	other.Channel = "painel"
	if err := r.DeleteSource(sw.ctx, other); err == nil {
		t.Fatal("a channel that is not the Telegram was deleted from")
	}
	other = origin
	other.Conversation = "31337" // the chat of someone else
	if err := r.DeleteSource(sw.ctx, other); err == nil {
		t.Fatal("a message of a chat that is not the user's was deleted")
	}
	if len(sw.fake.Deleted()) != 0 {
		t.Fatalf("deleted %+v", sw.fake.Deleted())
	}
	if err := r.DeleteSource(sw.ctx, origin); err != nil {
		t.Fatal(err)
	}
	if len(sw.fake.Deleted()) != 1 {
		t.Fatalf("deleted %+v, want the one", sw.fake.Deleted())
	}
}
