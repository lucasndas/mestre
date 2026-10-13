package app

import (
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
)

// The CLI is told why the Citadel cannot answer: away, or another machine
// taking its place (docs/arquitetura/canal-local.md#encaminhamento-à-cidadela).
func TestTheCLIIsToldTheCitadelIsAwayOrInFailover(t *testing.T) {
	t.Parallel()
	e := &Engine{local: localchan.NewLocalForwarder()}
	call := &localchan.Call{Request: localchan.Request{Cmd: "list"}}
	if r := (forwarder{e}).Forward(t.Context(), call); r.Error == nil || r.Error.Code != localchan.CodeCitadelUnavailable {
		t.Fatalf("with no Citadel: %+v", r.Error)
	}
	e.failover = true
	if r := (forwarder{e}).Forward(t.Context(), call); r.Error == nil || r.Error.Code != localchan.CodeCitadelInFailover || r.ExitCode() != localchan.ExitUnavailable {
		t.Fatalf("during a promotion: %+v", r.Error)
	}
}
