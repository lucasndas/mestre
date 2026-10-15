package hook_test

import (
	"encoding/json"
	"testing"

	"github.com/lucasndas/mestre/internal/hook"
	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/providers"
)

// A call identified as one character that names another never reaches the
// sinks of the events: it would move the state of the other character.
func TestForgedEventForAnotherCharacterIsDropped(t *testing.T) {
	t.Parallel()
	e := newEngine(t, `{version: 1, commands: {allow: ["*"], default: allow}, tools: {default: allow}}`)
	var got []providers.Event
	e.OnEvent = func(ev providers.Event, a hook.Answer) { got = append(got, ev) }
	ev, _ := json.Marshal(providers.Event{Version: 1, Provider: providers.ClaudeCode, NativeEvent: "Stop", Event: providers.EventTurnEnd,
		AgentID: agent, LastMessage: "forged answer from bob", SessionID: "--dangerously-skip-permissions"})
	resp := e.Handle(t.Context(), &localchan.Call{
		Request: localchan.Request{AgentID: "bob", Cmd: localchan.CmdHook, Args: []json.RawMessage{ev}},
		Caller:  localchan.Caller{Identified: true, AgentID: "bob"},
	})
	var ans hook.Answer
	if err := resp.DecodeData(&ans); err != nil {
		t.Fatal(err)
	}
	if ans.Verdict != hook.VerdictDeny {
		t.Fatalf("answer to bob: %+v", ans)
	}
	if len(got) > 0 {
		t.Fatalf("OnEvent got a forged event for %q from bob: %+v", got[0].AgentID, got[0])
	}
}
