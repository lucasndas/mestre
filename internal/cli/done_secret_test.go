package cli_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mestre-ia/mestre/internal/cli"
	"github.com/mestre-ia/mestre/internal/localchan"
)

type countNext struct{ n atomic.Int32 }

func (c *countNext) Forward(context.Context, *localchan.Call) localchan.Response {
	c.n.Add(1)
	return localchan.OK(map[string]any{})
}

// CA-CLI-CONV-12
func TestADoneWithATokenInTheProofOrTheReturnIsRefusedAndNothingIsRegistered(t *testing.T) {
	t.Parallel()
	fakeToken := "ghp_" + "0123456789abcdefghijklmnopqrstuvwxyz"
	next := &countNext{}
	g := cli.NewGate(cli.GateConfig{Commands: cli.Default(), Next: next,
		Profiles: cli.ProfilesFunc(func(context.Context, string) cli.Profile { return allowing(cli.RoleCharacter) })})
	ok := json.RawMessage(`"ok"`)
	for name, c := range map[string]struct {
		flags map[string]any
		where string
	}{
		"proof":  {map[string]any{"proof": []any{"C1=teste", "C2=token " + fakeToken}}, "--proof"},
		"return": {map[string]any{"return": "falhou com " + fakeToken}, "--return"},
	} {
		resp := g.Forward(t.Context(), &localchan.Call{Request: localchan.Request{Cmd: "done", Args: []json.RawMessage{ok}, Flags: c.flags}, Caller: localchan.Caller{Identified: true, AgentID: "dev"}})
		if resp.OK || resp.Error == nil || resp.Error.Code != "secret_detected" || resp.ExitCode() != 3 {
			t.Errorf("%s: %+v; want secret_detected with exit 3", name, resp)
			continue
		}
		if !strings.Contains(resp.Error.Message, c.where) || strings.Contains(resp.Error.Message, fakeToken) {
			t.Errorf("%s: the message %q must say where and never what", name, resp.Error.Message)
		}
	}
	if n := next.n.Load(); n != 0 {
		t.Errorf("%d calls passed the Gate", n)
	}
}
