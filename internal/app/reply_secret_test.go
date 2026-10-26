package app_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/orchestration/characters"
	"github.com/mestre-ia/mestre/internal/orchestration/inbox"
)

// sentReplies is the channel of the users of a world: it keeps what is sent.
type sentReplies struct {
	mu   sync.Mutex
	sent []string
}

func (c *sentReplies) Send(_ context.Context, o inbox.Outbound) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sent = append(c.sent, o.Text)
	return nil
}

func (c *sentReplies) all() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.sent...)
}

// CA-CLI-CONV-10
func TestTheGrandMasterReplyWithATokenIsRefusedWithSecretDetectedAndNothingIsSent(t *testing.T) {
	t.Parallel()
	ch := &sentReplies{}
	w := newCommWorld(t, func(_ *commWorld, oo *app.OrchestrationOptions) { oo.Channels = ch })
	// a message of a user that the Grand Master has in front of it: there is someone to answer
	if _, err := w.orc.Inbox.Enqueue(w.ctx, inbox.NewItem{Workspace: w.slug, Target: w.gm.ID, Kind: inbox.KindUser, Sender: "Lucas", Text: "como está o login?",
		Origin: &inbox.Origin{Channel: "telegram", User: "u1", UserName: "Lucas", Level: characters.LevelOwner}}); err != nil {
		t.Fatal(err)
	}
	token := "ghp_" + "0123456789abcdefghijklmnopqrstuvwxyz"
	r := w.run(w.gm, "reply", "use esta chave: "+token, "--json")
	v := decodeJSON(t, r)
	if r.code != 3 || v.OK || v.Error.Code != "secret_detected" || r.stderr != "" {
		t.Fatalf("mestre reply with a token = %+v; want exit 3 and secret_detected", r)
	}
	if strings.Contains(r.stdout, token) {
		t.Errorf("the refusal repeats the token: %s", r.stdout)
	}
	if got := ch.all(); len(got) != 0 {
		t.Fatalf("sent %q after the refusal", got)
	}
	// the same reply with no secret goes to the channel of the user
	if r := w.run(w.gm, "reply", "o login está pronto", "--json"); r.code != 0 {
		t.Fatalf("an ordinary reply = %+v", r)
	}
	if got := ch.all(); len(got) != 1 || got[0] != "o login está pronto" {
		t.Errorf("sent %q; want the ordinary reply", got)
	}
}
