package panel_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/protocol"
	"github.com/mestre-ia/mestre/internal/users"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// fakeTerminals is the Citadel behind the panel: one terminal of the workspace
// "loja", whose output the test writes by hand.
type fakeTerminals struct {
	mu      sync.Mutex
	info    panel.TerminalInfo
	typed   []string
	follows []*followCall
	typeErr error
	screen  string
	offset  uint64
	// followed gets a tick at each Follow (the restart of a slow browser is async)
	followed  chan struct{}
	stoppedCh chan struct{}
	// infoFailsAfter makes Info fail after that many calls (0: never)
	infoFailsAfter, infoCalls int
}

type followCall struct {
	from    uint64
	sink    panel.TerminalSink
	stopped bool
}

func newFakeTerminals() *fakeTerminals {
	id := protocol.NewIDGen(clock.Real{}).NewString() + "-1" // the terminals of the characters are <character>-<n>
	return &fakeTerminals{followed: make(chan struct{}, 64), stoppedCh: make(chan struct{}, 64), info: panel.TerminalInfo{TerminalID: id, Character: "c1", Name: "Dev-1", Machine: "t1", Workspace: "loja", State: "idle", Signal: true}}
}

func (f *fakeTerminals) ByCharacter(_ context.Context, id string) (panel.TerminalInfo, error) {
	if id != f.info.Character {
		return panel.TerminalInfo{}, panel.ErrNoTerminal
	}
	return f.info, nil
}

func (f *fakeTerminals) Info(_ context.Context, id string) (panel.TerminalInfo, error) {
	f.mu.Lock()
	f.infoCalls++
	fail := f.infoFailsAfter > 0 && f.infoCalls > f.infoFailsAfter
	f.mu.Unlock()
	if fail {
		return panel.TerminalInfo{}, panel.ErrNoTerminal
	}
	if id != f.info.TerminalID {
		return panel.TerminalInfo{}, panel.ErrNoTerminal
	}
	return f.info, nil
}

func (f *fakeTerminals) Follow(_ context.Context, _ string, from uint64, sink panel.TerminalSink) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c := &followCall{from: from, sink: sink}
	f.follows = append(f.follows, c)
	f.followed <- struct{}{}
	return func() {
		f.mu.Lock()
		c.stopped = true
		f.mu.Unlock()
		f.stoppedCh <- struct{}{}
	}, nil
}

func (f *fakeTerminals) Screen(context.Context, string) (string, uint64, error) {
	return f.screen, f.offset, nil
}

func (f *fakeTerminals) Type(_ context.Context, _ string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.typeErr != nil {
		return f.typeErr
	}
	f.typed = append(f.typed, string(data))
	return nil
}

func (f *fakeTerminals) typedSoFar() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.typed...)
}

func (f *fakeTerminals) followCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.follows)
}

func (f *fakeTerminals) lastFollow() *followCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.follows[len(f.follows)-1]
}

// terminalRig is a rig with the fake terminals and a browser connected as a user.
type terminalRig struct {
	*rig
	ft *fakeTerminals
}

func newTerminalRig(t *testing.T) *terminalRig {
	t.Helper()
	r := newRig(t)
	ft := newFakeTerminals()
	r.panel.SetTerminals(ft)
	return &terminalRig{rig: r, ft: ft}
}

// browser opens the socket of a session and subscribes to the terminal topic;
// it returns the socket after the snapshot and the acknowledgement.
func (r *terminalRig) browser(token string) *websocket.Conn {
	r.t.Helper()
	srv := r.wsServer()
	c, _, err := r.dial(srv, token, okOrigin(srv))
	if err != nil {
		r.t.Fatal(err)
	}
	c.SetReadLimit(1 << 20)
	r.t.Cleanup(func() { _ = c.CloseNow() })
	send(r.t, c, panel.TypeUISubscribe, "sub", panel.UISubscribe{Topics: []string{"terminal:" + r.ft.info.TerminalID}})
	snap, ack := read(r.t, c), read(r.t, c)
	if snap.Type != panel.TypeUIEvent || ack.Type != panel.TypeUISubscribed {
		r.t.Fatalf("snapshot %+v ack %+v", snap, ack)
	}
	var sub panel.UISubscribed
	_ = json.Unmarshal(ack.Payload, &sub)
	if len(sub.Topics) != 1 {
		r.t.Fatalf("the terminal was not accepted: %+v", sub)
	}
	return c
}

func (r *terminalRig) input(c *websocket.Conn, id, data string) panel.UIEnvelope {
	r.t.Helper()
	send(r.t, c, panel.TypeTerminalInput, id, panel.TerminalInputMessage{TerminalID: r.ft.info.TerminalID, Data: data})
	return read(r.t, c)
}

// settle returns when the server has handled everything the socket sent before:
// the messages of a socket run one at a time, so the answer to a bad one comes
// after the effects of the earlier ones.
func (r *terminalRig) settle(c *websocket.Conn) {
	r.t.Helper()
	send(r.t, c, panel.TypeTerminalTypingEnd, "settle", map[string]string{})
	if e := r.errorOf(read(r.t, c)); e.Code != panel.CodeBadRequest {
		r.t.Fatalf("settle: %+v", e)
	}
}

func (r *terminalRig) errorOf(env panel.UIEnvelope) panel.APIError {
	r.t.Helper()
	var e panel.APIError
	if env.Type != panel.TypeUIError {
		r.t.Fatalf("expected an error, got %+v", env)
	}
	_ = json.Unmarshal(env.Payload, &e)
	return e
}

// CA-CANVAS-03: an Approver (and an Observer) who sends input to a terminal over
// the socket is refused, at every message, and nothing reaches the PTY; an
// Administrator types, and the session is in the audit without what was typed.
func TestOnlyAnAdministratorTypesAndTheServerChecksEveryMessage(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	_, approver := r.member("Ana", "ana", users.Approver)
	_, observer := r.member("Obi", "obi", users.Observer)
	_, admin := r.member("Adm", "adm", users.Administrator)

	for name, tok := range map[string]string{"approver": approver, "observer": observer} {
		c := r.browser(tok)
		for i := range 2 { // every message, not only the first
			e := r.errorOf(r.input(c, "in"+string(rune('a'+i)), "rm -rf /\r"))
			if e.Code != panel.CodeForbiddenLevel {
				t.Fatalf("%s: code = %s, want forbidden_level", name, e.Code)
			}
		}
	}
	if got := r.ft.typedSoFar(); len(got) != 0 {
		t.Fatalf("a refused user reached the PTY with %q", got)
	}
	if n := r.audited("panel.terminal_input_refused"); n != 4 {
		t.Fatalf("%d refusals audited, want 4", n)
	}

	c := r.browser(admin)
	send(t, c, panel.TypeTerminalInput, "ok1", panel.TerminalInputMessage{TerminalID: r.ft.info.TerminalID, Data: "ls\r"})
	send(t, c, panel.TypeTerminalInput, "ok2", panel.TerminalInputMessage{TerminalID: r.ft.info.TerminalID, Data: "segredo-digitado\r"})
	// a message of another type, sent after, answers after the inputs: they ran in order
	send(t, c, panel.TypeTerminalInput, "bad", panel.TerminalInputMessage{})
	if e := r.errorOf(read(t, c)); e.Code != panel.CodeBadRequest {
		t.Fatalf("an empty input: %+v", e)
	}
	if got := r.ft.typedSoFar(); len(got) != 2 || got[0] != "ls\r" || got[1] != "segredo-digitado\r" {
		t.Fatalf("typed = %q", got)
	}
	// the session: one start, and one end when the keyboard is released
	if n := r.audited("panel.terminal_typing_started"); n != 1 {
		t.Fatalf("%d typing sessions started", n)
	}
	send(t, c, panel.TypeTerminalTypingEnd, "end", panel.TerminalTypingEndMessage{TerminalID: r.ft.info.TerminalID})
	r.settle(c)
	if n := r.audited("panel.terminal_typing_ended"); n != 1 {
		t.Fatalf("%d typing sessions ended", n)
	}
	var detail string
	if err := r.db.Reader().QueryRowContext(r.ctx, `SELECT detail FROM audit_log WHERE action = 'panel.terminal_typing_ended'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"character":"c1"`, `"started_at"`, `"ended_at"`, `"inputs":2`} {
		if !strings.Contains(detail, want) {
			t.Errorf("the audit of the session lacks %s: %s", want, detail)
		}
	}
	var all string
	rows, err := r.db.Reader().QueryContext(r.ctx, `SELECT detail FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var d string
		_ = rows.Scan(&d)
		all += d
	}
	if strings.Contains(all, "segredo-digitado") || strings.Contains(all, "rm -rf") {
		t.Fatal("what was typed is in the audit")
	}
}

// A user who lost the level between two messages is refused at the second: the
// level is read again at each one.
func TestTheLevelIsReadAgainAtEveryInput(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	u, admin := r.member("Adm", "adm", users.Administrator)
	c := r.browser(admin)
	send(t, c, panel.TypeTerminalInput, "1", panel.TerminalInputMessage{TerminalID: r.ft.info.TerminalID, Data: "a"})
	send(t, c, panel.TypeTerminalTypingEnd, "e", panel.TerminalTypingEndMessage{TerminalID: r.ft.info.TerminalID})
	r.settle(c)
	// demoted by the database (the service would end the session and the socket at once)
	err := r.db.WriteDurable(r.ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(r.ctx, `UPDATE users SET level = 'approver' WHERE id = ?`, u.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e := r.errorOf(r.input(c, "2", "b"))
	if e.Code != panel.CodeForbiddenLevel {
		t.Fatalf("code = %s", e.Code)
	}
	if got := r.ft.typedSoFar(); len(got) != 1 {
		t.Fatalf("typed = %q", got)
	}
}

// Typing without having opened the terminal, and into a terminal of a workspace
// the user does not act in, types nothing.
func TestInputNeedsTheTerminalToBeOpen(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	_, admin := r.member("Adm", "adm", users.Administrator)
	srv := r.wsServer()
	c, _, err := r.dial(srv, admin, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	if e := r.errorOf(r.input(c, "x", "oi")); e.Code != panel.CodeBadRequest {
		t.Fatalf("code = %s", e.Code)
	}
	if got := r.ft.typedSoFar(); len(got) != 0 {
		t.Fatalf("typed = %q", got)
	}
	// a terminal of another workspace is not even opened
	r.ft.info.Workspace = "blog"
	send(t, c, panel.TypeUISubscribe, "s", panel.UISubscribe{Topics: []string{"terminal:" + r.ft.info.TerminalID}})
	var ack panel.UISubscribed
	env := read(t, c)
	_ = json.Unmarshal(env.Payload, &ack)
	if len(ack.Rejected) != 1 || ack.Rejected[0].Code != panel.CodeForbiddenLevel {
		t.Fatalf("subscribed = %+v", ack)
	}
}

// A machine without signal answers the input as unavailable; the panel says so.
func TestTypingIntoAMachineWithoutSignalIsUnavailable(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	_, admin := r.member("Adm", "adm", users.Administrator)
	c := r.browser(admin)
	r.ft.typeErr = panel.ErrNoSignal
	e := r.errorOf(r.input(c, "n", "x"))
	if e.Code != panel.CodeUnavailable || e.Params["reason"] != "no_signal" {
		t.Fatalf("error = %+v", e)
	}
}

// The output goes to the browser as binary frames 0x01 with the id of the
// terminal and the offset, the events as ui.event, and a browser that leaves
// stops the stream.
func TestTheOutputReachesTheBrowserAsFramesAndLeavingStopsTheStream(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	_, obs := r.member("Obi", "obi", users.Observer)
	c := r.browser(obs)
	f := r.ft.lastFollow()
	if f.from != 0 {
		t.Fatalf("the stream starts at %d, want 0", f.from)
	}
	f.sink.Output(0, []byte("olá"))
	f.sink.Replayed(0, 4, false)
	f.sink.Signal(false, time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC))
	typ, b, err := c.Read(t.Context())
	if err != nil || typ != websocket.MessageBinary {
		t.Fatalf("frame: %v %v", typ, err)
	}
	df, err := protocol.DecodeData(b)
	sum := sha256.Sum256([]byte(r.ft.info.TerminalID))
	want := protocol.ULID(sum[:16])
	if err != nil || df.Type != protocol.DataTerminal || df.ID != want || df.Pos != 0 || string(df.Data) != "olá" {
		t.Fatalf("frame = %+v %v", df, err)
	}
	if binary.BigEndian.Uint64(b[17:25]) != 0 {
		t.Fatal("the position is not at the documented place")
	}
	var ev panel.UIEvent
	env := read(t, c)
	_ = json.Unmarshal(env.Payload, &ev)
	if ev.Event != panel.EventReplayed || string(ev.Data) != `{"from":0,"to":4,"lost":false}` {
		t.Fatalf("event = %+v %s", ev, ev.Data)
	}
	env = read(t, c)
	_ = json.Unmarshal(env.Payload, &ev)
	if ev.Event != panel.EventSignal || !strings.Contains(string(ev.Data), `"signal":false`) || !strings.Contains(string(ev.Data), "2026-10-26T08:00:00Z") {
		t.Fatalf("event = %+v %s", ev, ev.Data)
	}
	send(t, c, panel.TypeUIUnsubscribe, "u", panel.UISubscribe{Topics: []string{"terminal:" + r.ft.info.TerminalID}})
	read(t, c)
	r.ft.mu.Lock()
	stopped := r.ft.follows[0].stopped
	r.ft.mu.Unlock()
	if !stopped {
		t.Fatal("the stream goes on after the browser left")
	}
}

// A browser that does not read gets the picture of the screen instead of the queue:
// the stream restarts from the offset of the picture.
func TestASlowBrowserGetsTheScreenInsteadOfTheQueue(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	r.ft.screen, r.ft.offset = "tela atual", 9<<20
	_, obs := r.member("Obi", "obi", users.Observer)
	c := r.browser(obs)
	f := r.ft.lastFollow()
	chunk := make([]byte, 32<<10)
	// the browser reads nothing: the kernel buffers fill, then the queue of the socket
	for i := 0; r.ft.followCount() == 1 && i < 4000; i++ {
		f.sink.Output(uint64(i)*uint64(len(chunk)), chunk)
	}
	<-r.ft.followed // the first follow
	<-r.ft.followed // the restart
	if got := r.ft.lastFollow().from; got != 9<<20 {
		t.Fatalf("the stream restarted from %d, want the offset of the picture", got)
	}
	// the picture comes in the socket among the frames that were queued
	for {
		typ, b, err := c.Read(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		if typ != websocket.MessageText {
			continue
		}
		var env panel.UIEnvelope
		_ = json.Unmarshal(b, &env)
		var ev panel.UIEvent
		_ = json.Unmarshal(env.Payload, &ev)
		if ev.Event == panel.EventScreen {
			if !strings.Contains(string(ev.Data), `"text":"tela atual"`) || !strings.Contains(string(ev.Data), `"offset":9437184`) {
				t.Fatalf("screen = %s", ev.Data)
			}
			break
		}
	}
}

// A user who loses the workspace of a terminal stops getting its output: the socket is closed
// (the sessions end, as in a change of level), and what he had open is not followed any more.
func TestAUserWhoLosesTheWorkspaceStopsFollowingItsTerminal(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	u, obs := r.member("Obi", "obi", users.Observer)
	c := r.browser(obs)
	none := []string{}
	if _, err := r.users.Edit(r.ctx, r.owner, u.ID, users.Edit{Workspaces: &none}); err != nil {
		t.Fatal(err)
	}
	if code := closedWith(t, c); code != panel.CloseSessionEnded {
		t.Fatalf("closed with %d, want the end of the session", code)
	}
	<-r.ft.stoppedCh // the stream stops when the socket is gone
}

// A terminal whose information cannot be read opens no stream and takes no typing.
func TestATerminalWhoseInfoFailsOpensNoStreamAndTakesNoTyping(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	_, admin := r.member("Adm", "adm", users.Administrator)
	r.ft.infoFailsAfter = 1 // the authorization reads it, the opening does not
	srv := r.wsServer()
	c, _, err := r.dial(srv, admin, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, panel.TypeUISubscribe, "s", panel.UISubscribe{Topics: []string{"terminal:" + r.ft.info.TerminalID}})
	read(t, c) // the signal event
	read(t, c) // the answer to the subscription
	if r.ft.followCount() != 0 {
		t.Fatal("a stream was opened without the terminal")
	}
	if e := r.errorOf(r.input(c, "x", "ls\r")); e.Code != panel.CodeBadRequest {
		t.Fatalf("code = %s", e.Code)
	}
	if got := r.ft.typedSoFar(); len(got) != 0 {
		t.Fatalf("typed = %q", got)
	}
}

// One user follows at most 16 terminals at once: the 17th is refused, and a place is
// back when a socket closes.
func TestOneUserFollowsAtMostSixteenTerminals(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	_, obs := r.member("Obi", "obi", users.Observer)
	var socks []*websocket.Conn
	for range 16 {
		socks = append(socks, r.browser(obs))
	}
	srv := r.wsServer()
	c, _, err := r.dial(srv, obs, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, panel.TypeUISubscribe, "s", panel.UISubscribe{Topics: []string{"terminal:" + r.ft.info.TerminalID}})
	read(t, c) // snapshot
	if e := r.errorOf(read(t, c)); e.Code != panel.CodeTooManyStreams {
		t.Fatalf("code = %s", e.Code)
	}
	if n := r.ft.followCount(); n != 16 {
		t.Fatalf("%d streams, want 16", n)
	}
	_ = socks
}

// GET /api/characters/{id}/terminal: the terminal of a character, with whether
// the user may type; a character with no terminal, or of another workspace, is 404.
func TestTheTerminalOfACharacterIsReadWithWhatTheUserMayDo(t *testing.T) {
	t.Parallel()
	r := newTerminalRig(t)
	_, adm := r.member("Adm", "adm", users.Administrator)
	_, obs := r.member("Obi", "obi", users.Observer)
	got := decodeInto[panel.TerminalInfo](t, r.call("GET", "/api/characters/c1/terminal", nil, opts{cookie: adm}))
	if got.TerminalID != r.ft.info.TerminalID || !got.CanType || got.Cols != 200 || got.Rows != 50 || !got.Signal {
		t.Fatalf("info = %+v", got)
	}
	got = decodeInto[panel.TerminalInfo](t, r.call("GET", "/api/characters/c1/terminal", nil, opts{cookie: obs}))
	if got.CanType {
		t.Fatalf("an observer may type: %+v", got)
	}
	r.wantErr(r.call("GET", "/api/characters/outro/terminal", nil, opts{cookie: adm}), 404, panel.CodeNotFound)
	r.ft.info.Workspace = "blog"
	r.wantErr(r.call("GET", "/api/characters/c1/terminal", nil, opts{cookie: adm}), 404, panel.CodeNotFound)
	r.wantErr(r.call("GET", "/api/characters/c1/terminal", nil, opts{}), 401, panel.CodeUnauthenticated)
}
