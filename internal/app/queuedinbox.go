package app

import (
	"context"

	"github.com/mestre-ia/mestre/internal/localchan"
)

// queuedToInbox is what the commands of GitHub tell when a command they answered
// `queued` went out: the result goes to the inbox of whoever made it, with the
// op_id it was given (docs/cli/convencoes.md#saída---json).
func (r *stdRun) queuedToInbox(orc *Orchestration) func(context.Context, string, string, string, localchan.Response) {
	return func(ctx context.Context, opID, agentID, cmd string, resp localchan.Response) {
		if err := orc.Inbox.QueuedResult(ctx, opID, agentID, cmd, resp); err != nil {
			r.env.Log.Warn("app: the result of a queued command of GitHub was not put in the inbox", "op_id", opID, "agent", agentID, "cmd", cmd, "err", err)
		}
	}
}
