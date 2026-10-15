package characters

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lucasndas/mestre/internal/protocol"
	"github.com/lucasndas/mestre/internal/providers"
	"github.com/lucasndas/mestre/internal/safefile"
)

// The deliveries of the inbox on the machine of the character
// (docs/orquestracao/esperas-e-caixa-de-entrada.md#caixa-de-entrada).
const (
	// DefaultLongText is the size, in characters, above which a text goes to
	// a file and only the header and the request to read it are typed.
	DefaultLongText = 2000
	// DefaultStallAfter is how long a delivery may wait for the start of a
	// turn before the key Enter is sent once; after the same time again the
	// character is stuck.
	DefaultStallAfter = 30 * time.Second
)

// itemID is what the id of an item of the inbox may be: it names a file.
var itemID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// InboxFile is where the text of a long item is kept, relative to the
// worktree of the character (.mestre/agents/<id>/inbox/<item>.md).
func InboxFile(characterID, item string) string {
	return path.Join(".mestre", "agents", characterID, "inbox", item+".md")
}

// DeliverInbox types an item of the inbox into the terminal of the character
// it names (`inbox.deliver`, docs/conectividade/protocolo.md). A character
// that is not free (anything but idle or waiting) is not typed into: the
// result is busy and the item goes back to the queue of the Citadel. The
// result of a delivery is "delivered" even though the turn did not begin yet:
// the supervision confirms the start of the turn within 10 s, or the delivery
// is recorded as unconfirmed and never typed again.
//
// What is typed is the header, a line, and the text. A text above the limit
// (2000 characters by default), or one the adapter cannot type as it is (more
// than 8 KiB, or several lines for a version of the provider that does not take
// a pasted text as one message), is written in a file of the folder of the
// character and only the header and the request to read the file are typed;
// the last resort, for the provider that takes no line break at all, is the
// two in one line.
func (rt *Runtime) DeliverInbox(ctx context.Context, d protocol.InboxDeliver) (protocol.InboxResult, error) {
	rt.mu.Lock()
	s := rt.byTerm[d.TerminalID]
	rt.mu.Unlock()
	if s == nil {
		return protocol.InboxResult{}, fmt.Errorf("characters: no character on the terminal %s of this machine", d.TerminalID)
	}
	if !itemID.MatchString(d.ItemID) {
		return protocol.InboxResult{}, fmt.Errorf("characters: %q is not an id of an item of the inbox", d.ItemID)
	}
	busy := protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxBusy}
	if !s.status().State.Receives() || s.holdsFirst() {
		return busy, nil
	}
	long := rt.o.LongText
	if long <= 0 {
		long = DefaultLongText
	}
	body := d.Text
	if upd := rt.update(s); upd != "" {
		body = upd + "\n" + body
	}
	typed, err := rt.compose(s, d, body, long)
	if err != nil {
		return protocol.InboxResult{}, err
	}
	switch err := rt.deliverRaw(ctx, s, d.ItemID, typed); {
	case errors.Is(err, ErrBusy):
		return busy, nil
	case err != nil:
		return protocol.InboxResult{}, err
	}
	s.armStall()
	return protocol.InboxResult{ItemID: d.ItemID, Status: protocol.InboxDelivered}, nil
}

// compose chooses what is typed, writing the file when it has to.
func (rt *Runtime) compose(s *session, d protocol.InboxDeliver, body string, long int) (string, error) {
	head := d.Header
	lines := func(parts ...string) string {
		var out []string
		for _, p := range parts {
			if p != "" {
				out = append(out, p)
			}
		}
		return strings.Join(out, "\n")
	}
	fits := func(text string) bool {
		_, err := s.adapter.TypeMessage(text)
		return err == nil
	}
	if utf8.RuneCountInString(body) <= long {
		if inline := lines(head, body); fits(inline) {
			return inline, nil
		}
	}
	if s.launch.Dir == "" {
		return "", fmt.Errorf("characters: %s has no folder to keep a long text in", s.id)
	}
	rel := InboxFile(s.id, d.ItemID)
	if err := safefile.Write(filepath.Join(s.launch.Dir, filepath.FromSlash(rel)), []byte(body), safefile.Options{}); err != nil {
		return "", fmt.Errorf("characters: the long text of %s was not kept: %w", d.ItemID, err)
	}
	request := "O texto é longo: leia o arquivo " + rel + " e trate o pedido."
	if two := lines(head, request); fits(two) {
		return two, nil
	}
	return strings.TrimSpace(head + " " + request), nil
}

// InboxHandler is the handler of `inbox.deliver` in the router of the engine
// of a machine (docs/conectividade/protocolo.md): the command is accepted (the
// ack says so), and the result `inbox.result` says delivered or busy; a busy
// terminal gives the item back to the queue of the Citadel.
func (rt *Runtime) InboxHandler() protocol.Handler {
	return protocol.HandlerFunc(func(ctx context.Context, m *protocol.Message) (*protocol.Reply, error) {
		d, ok := m.Payload.(*protocol.InboxDeliver)
		if !ok {
			return nil, fmt.Errorf("characters: a payload %T is not an inbox.deliver", m.Payload)
		}
		res, err := rt.DeliverInbox(ctx, *d)
		if err != nil {
			return nil, err
		}
		return &protocol.Reply{Type: protocol.TypeInboxResult, Payload: &res}, nil
	})
}

// armStall starts the wait for the turn a delivery must provoke: after
// DefaultStallAfter without it the key Enter is sent once, and after the same
// time again the character is stuck. A character without hooks is confirmed
// by its screen, so it is not watched this way.
func (s *session) armStall() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.info.Hooks || s.reduced == string(providers.ReducedNoHooks) {
		return
	}
	stop(s.stallTimer)
	s.stallGen++
	gen := s.stallGen
	s.stallTimer = s.rt.clk.AfterFunc(s.rt.o.StallAfter, func() { s.stalled(gen, false) })
}

// turnBegan ends the wait of a delivery: the turn started.
func (s *session) turnBegan() {
	s.mu.Lock()
	stop(s.stallTimer)
	s.stallTimer = nil
	s.stallGen++
	s.mu.Unlock()
}

func (s *session) stalled(gen int, enterSent bool) {
	ctx := context.Background()
	s.mu.Lock()
	if gen != s.stallGen || s.gone || s.st.State != Busy {
		s.mu.Unlock()
		return
	}
	if enterSent {
		s.mu.Unlock()
		s.do(ctx, TrigNoSignal, "the delivery did not start a turn, not even after Enter")
		return
	}
	p := s.proc
	s.stallTimer = s.rt.clk.AfterFunc(s.rt.o.StallAfter, func() { s.stalled(gen, true) })
	s.mu.Unlock()
	if p != nil {
		if err := p.Input(ctx, []byte("\r")); err != nil {
			s.rt.log.Warn("characters: Enter was not sent after a delivery that began no turn", "id", s.id, "err", err)
		}
	}
}
