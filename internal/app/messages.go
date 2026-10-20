package app

import (
	"context"
	"fmt"

	"github.com/lucasndas/mestre/internal/orchestration/inbox"
	"github.com/lucasndas/mestre/internal/orchestration/tables"
)

// messages gives the messages of the users to `mestre table open
// --from-message`: the ones given, or the messages of the users that reached an
// inbox of the workspace, whole.
func (o OrchestrationOptions) messages() tables.Messages {
	if o.Messages != nil {
		return o.Messages
	}
	return inboxMessages{&inbox.Store{DB: o.Guild}}
}

// inboxMessages reads the messages of the users in the inbox they arrived in.
type inboxMessages struct{ st *inbox.Store }

var _ tables.Messages = inboxMessages{}

// Message implements tables.Messages: a message of a user of the workspace, with
// its text byte for byte and where it came from. What is not a message of a user
// (an item of an agent, a system notice) is not one the table opens from. The
// files it carries are copied by the attachments of the table, not here.
func (m inboxMessages) Message(ctx context.Context, workspace, id string) (tables.Message, error) {
	it, err := m.st.Get(ctx, id)
	if err != nil {
		return tables.Message{}, fmt.Errorf("no message %s", id)
	}
	if it.Workspace != workspace || it.Kind != inbox.KindUser || it.Origin == nil {
		return tables.Message{}, fmt.Errorf("%s is not a message of a user of this workspace", id)
	}
	return tables.Message{ID: it.ID, Text: it.Text, Origin: *it.Origin}, nil
}
