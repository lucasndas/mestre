package panel_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/coder/websocket"

	"github.com/mestre-ia/mestre/internal/panel"
	"github.com/mestre-ia/mestre/internal/users"
)

func send(t *testing.T, c *websocket.Conn, typ, id string, payload any) {
	t.Helper()
	b, _ := json.Marshal(payload)
	m, _ := json.Marshal(panel.UIEnvelope{ID: id, Type: typ, TS: "2026-10-26T09:00:00.000Z", Payload: b})
	if err := c.Write(t.Context(), websocket.MessageText, m); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, c *websocket.Conn) panel.UIEnvelope {
	t.Helper()
	typ, b, err := c.Read(t.Context())
	if err != nil || typ != websocket.MessageText {
		t.Fatalf("read: %v (%v)", err, typ)
	}
	var env panel.UIEnvelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestTheBrowserSubscribesGetsTheSnapshotThenTheEvents(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.panel.Hub().Provide("workspace", panel.Topic{
		Authorize: func(u users.User, id string) bool { return u.Acts(id) },
		Snapshot: func(_ context.Context, _ users.User, id string) (any, error) {
			return map[string]string{"workspace": id, "state": "active"}, nil
		},
	})
	srv := r.wsServer()
	_, tok := r.member("Ana", "ana", users.Approver) // acts in "loja"
	c, _, err := r.dial(srv, tok, okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()

	send(t, c, panel.TypeUISubscribe, "01A", panel.UISubscribe{Topics: []string{"workspace:loja", "workspace:blog", "table:9", "lixo"}})
	var snap, ack panel.UIEnvelope
	snap, ack = read(t, c), read(t, c)
	if snap.Type != panel.TypeUIEvent || snap.ReplyTo != "01A" || ack.Type != panel.TypeUISubscribed || ack.ReplyTo != "01A" {
		t.Fatalf("snapshot %+v ack %+v", snap, ack)
	}
	var ev panel.UIEvent
	_ = json.Unmarshal(snap.Payload, &ev)
	if ev.Topic != "workspace:loja" || ev.Event != panel.EventSnapshot || string(ev.Data) != `{"state":"active","workspace":"loja"}` {
		t.Fatalf("event = %+v %s", ev, ev.Data)
	}
	var sub panel.UISubscribed
	_ = json.Unmarshal(ack.Payload, &sub)
	if len(sub.Topics) != 1 || sub.Topics[0] != "workspace:loja" || len(sub.Rejected) != 3 {
		t.Fatalf("subscribed = %+v", sub)
	}
	codes := map[string]panel.Code{}
	for _, rj := range sub.Rejected {
		codes[rj.Topic] = rj.Code
	}
	if codes["workspace:blog"] != panel.CodeForbiddenLevel || codes["table:9"] != panel.CodeUnknownTopic || codes["lixo"] != panel.CodeUnknownTopic {
		t.Fatalf("rejected = %v", codes)
	}

	// an event of a topic goes only to those who follow it
	if err := r.panel.Hub().Publish("workspace:blog", "changed", map[string]int{"n": 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.panel.Hub().Publish("workspace:loja", "changed", map[string]int{"n": 2}); err != nil {
		t.Fatal(err)
	}
	got := read(t, c)
	_ = json.Unmarshal(got.Payload, &ev)
	if got.Type != panel.TypeUIEvent || ev.Topic != "workspace:loja" || ev.Event != "changed" || string(ev.Data) != `{"n":2}` {
		t.Fatalf("event = %+v %+v", got, ev)
	}
	// a frame of terminal goes as binary, to those who follow
	r.panel.Hub().PublishFrame("workspace:loja", []byte{0x01, 'x'})
	typ, b, err := c.Read(t.Context())
	if err != nil || typ != websocket.MessageBinary || len(b) != 2 {
		t.Fatalf("frame: %v %v %v", typ, b, err)
	}
	send(t, c, panel.TypeUIUnsubscribe, "01B", panel.UISubscribe{Topics: []string{"workspace:loja"}})
	read(t, c)
	if n := r.panel.Hub().Subscribers("workspace:loja"); n != 0 {
		t.Fatalf("%d subscribers after leaving", n)
	}
}

func TestTheBrowserChannelRefusesWhatIsNotAMessage(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	srv := r.wsServer()
	c, _, err := r.dial(srv, r.ownerSession(), okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	if err := c.Write(t.Context(), websocket.MessageText, []byte("não é json")); err != nil {
		t.Fatal(err)
	}
	var b panel.APIError
	env := read(t, c)
	_ = json.Unmarshal(env.Payload, &b)
	if env.Type != panel.TypeUIError || b.Code != panel.CodeBadRequest {
		t.Fatalf("answer = %+v %+v", env, b)
	}
	send(t, c, "terminal.input", "01C", map[string]string{})
	env = read(t, c)
	if env.Type != panel.TypeUIError || env.ReplyTo != "01C" {
		t.Fatalf("answer = %+v", env)
	}
}

// CA-CRESC-14 (the portrait of the Guild): the topic guild:main is refused to an Observer, to an
// Approver and to an Administrator, who get no portrait of it; the Owner follows it and gets the portrait.
func TestOnlyTheOwnerFollowsTheGuildTopic(t *testing.T) {
	t.Parallel()
	r, _ := guildRig(t)
	srv := r.wsServer()
	for name, level := range map[string]users.Level{"observer": users.Observer, "approver": users.Approver, "administrator": users.Administrator} {
		_, tok := r.member(name, name, level)
		c, _, err := r.dial(srv, tok, okOrigin(srv))
		if err != nil {
			t.Fatal(err)
		}
		send(t, c, panel.TypeUISubscribe, "01A", panel.UISubscribe{Topics: []string{panel.GuildTopic}})
		ack := read(t, c) // a portrait would come before the acknowledgement
		var sub panel.UISubscribed
		_ = json.Unmarshal(ack.Payload, &sub)
		if ack.Type != panel.TypeUISubscribed || len(sub.Topics) != 0 || len(sub.Rejected) != 1 || sub.Rejected[0].Code != panel.CodeForbiddenLevel {
			t.Errorf("%s: the answer is %s %+v; want the topic refused for the level", name, ack.Type, sub)
		}
		if n := r.panel.Hub().Subscribers(panel.GuildTopic); n != 0 {
			t.Errorf("%s: %d followers of the topic", name, n)
		}
		_ = c.CloseNow()
	}
	c, _, err := r.dial(srv, r.ownerSession(), okOrigin(srv))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.CloseNow() }()
	send(t, c, panel.TypeUISubscribe, "01B", panel.UISubscribe{Topics: []string{panel.GuildTopic}})
	var ev panel.UIEvent
	snap := read(t, c)
	_ = json.Unmarshal(snap.Payload, &ev)
	if snap.Type != panel.TypeUIEvent || ev.Topic != panel.GuildTopic || ev.Event != panel.EventSnapshot {
		t.Fatalf("the Owner got %s %+v; want the portrait", snap.Type, ev)
	}
}
