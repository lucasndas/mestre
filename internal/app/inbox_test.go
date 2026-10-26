package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/internal/localchan"
)

// The result of a call that waited in the outbox of a Tower goes to the inbox of
// whoever made it, with the op_id the caller was given; a result that cannot be
// delivered is only logged, never run again.
func TestAQueuedResultIsHandedToTheInboxWithItsOpID(t *testing.T) {
	t.Parallel()
	type got struct{ op, agent, cmd string }
	var seen []got
	fail := false
	deliver := func(_ context.Context, op, agent, cmd string, resp localchan.Response) error {
		seen = append(seen, got{op, agent, cmd})
		if !resp.OK {
			t.Errorf("the response lost its failure: %+v", resp)
		}
		if fail {
			return errors.New("no such character")
		}
		return nil
	}
	on := app.QueuedToInbox(deliver, nil)
	on(app.QueuedResult{OpID: "op-1", AgentID: "dev", Cmd: "done", Response: localchan.OK(nil)})
	fail = true
	on(app.QueuedResult{OpID: "op-2", AgentID: "ghost", Cmd: "done", Response: localchan.OK(nil)}) // does not panic
	if len(seen) != 2 || seen[0] != (got{"op-1", "dev", "done"}) || seen[1].op != "op-2" {
		t.Fatalf("seen = %+v", seen)
	}
}
