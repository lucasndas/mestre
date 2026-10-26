package app

import (
	"context"
	"log/slog"

	"github.com/mestre-ia/mestre/internal/localchan"
)

// QueuedToInbox is the OnQueuedResult that delivers the result of a call that
// waited in the outbox of a Tower to the inbox of whoever made it, with the
// op_id the caller was given (inbox.Service.QueuedResult). A result that
// cannot be delivered is logged: the call already ran, and running it again is
// worse than losing the notice.
func QueuedToInbox(deliver func(ctx context.Context, opID, agentID, cmd string, resp localchan.Response) error, log *slog.Logger) func(QueuedResult) {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return func(q QueuedResult) {
		if err := deliver(context.Background(), q.OpID, q.AgentID, q.Cmd, q.Response); err != nil {
			log.Warn("app: the result of a queued call was not put in the inbox", "op_id", q.OpID, "agent", q.AgentID, "cmd", q.Cmd, "err", err)
		}
	}
}
