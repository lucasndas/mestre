//go:build full

package characters_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/orchestration/characters"
	"github.com/lucasndas/mestre/internal/providers"
)

// CA-CLI-CONV-04: two characters on two machines of the Guild (the Citadel's
// and a Tower's), connected: the one asks the other with `mestre ask` and gets
// the answer without saying a machine. Each machine has its own runtime and
// terminal; the question is typed on the machine of the target, and the answer
// comes from the end of its turn there.
func TestAnAskBetweenCharactersOfTwoMachinesIsAnsweredWithoutSayingAMachine(t *testing.T) {
	t.Parallel()
	r := newRig(t, rigOptions{stage7: true, machines: []machineSpec{linux("linux"), linux("linux-torre")}})
	dev, err := r.create(characters.CreateRequest{Name: "Dev", Sheet: "Dev Claude", Table: "3-login", Machine: "linux"})
	if err != nil {
		t.Fatal(err)
	}
	rev, err := r.create(characters.CreateRequest{Name: "Revisor", Sheet: "Dev Claude", Table: "3-login", Machine: "linux-torre"})
	if err != nil {
		t.Fatal(err)
	}
	d, v := dev.Character, rev.Character
	if d.Machine == v.Machine {
		t.Fatalf("both on %s", d.Machine)
	}
	r.ready(d)
	r.ready(v)
	if err := r.conns.Link(r.ctx, workspace, "3-login", connectionsChar(d), connectionsChar(v), ""); err != nil {
		t.Fatal(err)
	}
	r.hook(d, providers.EventTurnStart) // the Dev is inside its shell tool, in the call of the CLI
	fwd := localchan.NewLocalForwarder()
	r.wt.Register(fwd)
	done := make(chan localchan.Response, 1)
	// A wait that nothing ends would hold the whole test binary until its
	// timeout: it ends with the test's own limit, or when the ask returns before
	// the question was typed (then it says why).
	wait, stop := context.WithTimeout(t.Context(), 30*time.Second)
	defer stop()
	go func() {
		// no machine anywhere in the call: the engine finds the target
		resp := fwd.Forward(r.ctx, &localchan.Call{Request: localchan.Request{Cmd: "ask", Args: localchan.TextArgs("Revisor", "revise o PR 12")},
			Caller: localchan.Caller{Identified: true, AgentID: d.ID}})
		if !resp.OK {
			t.Logf("ask returned before the answer: %+v", resp)
			stop()
		}
		done <- resp
	}()
	// the question is typed on the other machine: wait for it, not for a
	// number of yields, which a loaded runner can exhaust first
	typed := r.mach[v.Machine].host.proc(v.ID).waitTyped(t, wait, 1)
	if len(typed) != 1 || !strings.Contains(plain(t, typed[0]), "revise o PR 12") {
		t.Fatalf("typed on the Tower: %q", typed)
	}
	if other := r.mach[d.Machine].host.proc(v.ID); other != nil {
		t.Fatalf("something of the target runs on the machine of the asker: %+v", other)
	}
	r.hook(v, providers.EventTurnStart)
	if err := r.svc.Done(r.ctx, v.ID, "PR 12 aprovado, sem ressalvas"); err != nil {
		t.Fatal(err)
	}
	r.hook(v, providers.EventTurnEnd)
	var resp localchan.Response
	select {
	case resp = <-done:
	case <-wait.Done():
		t.Fatalf("the ask did not return: %v", wait.Err())
	}
	var data map[string]any
	if err := resp.DecodeData(&data); err != nil || !resp.OK || data["response"] != "PR 12 aprovado, sem ressalvas" || data["status"] != "answered" {
		t.Fatalf("ask = %+v (%v) %v", resp, err, data)
	}
	for k := range data {
		if strings.Contains(k, "machine") || strings.Contains(k, "host") {
			t.Errorf("the answer tells a machine: %v", data)
		}
	}
}
