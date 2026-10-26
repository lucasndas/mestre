// Package fakechannel is the channel of the questions of the flow of approval
// (approvals.Messenger) for the tests: it keeps what was sent to each user and
// how each message was edited.
package fakechannel

import (
	"context"
	"fmt"
	"sync"

	"github.com/mestre-ia/mestre/internal/approvals"
)

// Channel records the questions and the edits.
type Channel struct {
	mu    sync.Mutex
	sent  map[string]approvals.Message // by ref
	order []string
	text  map[string]string
	keep  map[string]bool
	n     int
	noway map[string]bool
}

// New returns an empty channel.
func New() *Channel {
	return &Channel{sent: map[string]approvals.Message{}, text: map[string]string{}, keep: map[string]bool{}, noway: map[string]bool{}}
}

var _ approvals.Messenger = (*Channel)(nil)

// Unreachable makes the channel answer approvals.ErrNoChannel to the user.
func (c *Channel) Unreachable(user string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.noway[user] = true
}

// Send implements approvals.Messenger.
func (c *Channel) Send(_ context.Context, m approvals.Message) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.noway[m.UserID] {
		return "", approvals.ErrNoChannel
	}
	c.n++
	ref := fmt.Sprintf("m%d", c.n)
	c.sent[ref], c.text[ref], c.keep[ref] = m, m.Text, true
	c.order = append(c.order, ref)
	return ref, nil
}

// Edit implements approvals.Messenger.
func (c *Channel) Edit(_ context.Context, e approvals.MessageEdit) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.text[e.Ref], c.keep[e.Ref] = e.Text, len(e.Answers) > 0
	return nil
}

// Sent is how many questions were sent.
func (c *Channel) Sent() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.order)
}

// To is the last message sent to a user.
func (c *Channel) To(user string) (ref string, m approvals.Message, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, r := range c.order {
		if c.sent[r].UserID == user {
			ref, m, ok = r, c.sent[r], true
		}
	}
	return
}

// Read is the text now of a message and whether it still has buttons.
func (c *Channel) Read(ref string) (text string, buttons bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.text[ref], c.keep[ref]
}
