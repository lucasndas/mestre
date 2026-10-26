package app

import (
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/hook"
	"github.com/mestre-ia/mestre/internal/logging"
	"github.com/mestre-ia/mestre/internal/providers"
)

// What an event says crosses the network and waits in the outbox of the Tower:
// the secrets the masker knows do not go in clear, and the payload of the
// provider (Raw) does not go at all.
func TestAnEventSentToTheCitadelCarriesNoSecretInClear(t *testing.T) {
	t.Parallel()
	m := logging.NewMasker()
	m.Register("s3cr3t-token-value")
	ev := providers.Event{
		AgentID: "dev-1", Event: providers.EventTurnEnd, Prompt: "use s3cr3t-token-value", LastMessage: "ok s3cr3t-token-value",
		Tool:       &providers.Tool{Command: "curl -H s3cr3t-token-value x"},
		ToolResult: &providers.ToolResult{Output: "s3cr3t-token-value", Error: "s3cr3t-token-value"},
		Raw:        []byte(`{"secret":"s3cr3t-token-value"}`),
	}
	obj, err := agentEventPayload(ev, hook.Answer{}, m)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(obj["event"]); strings.Contains(got, "s3cr3t-token-value") {
		t.Errorf("the event carries the secret: %s", got)
	}
	if ev.Tool.Command != "curl -H s3cr3t-token-value x" {
		t.Error("the masking changed the event of the caller")
	}
}
