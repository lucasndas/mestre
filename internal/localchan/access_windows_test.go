package localchan_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/winsvc"
)

// The ACL itself, with real SIDs and a socket, is internal/winsvc's test.
func TestTheDefaultAccessIsTheOneOfTheDocument(t *testing.T) {
	t.Parallel()
	a, ok := localchan.DefaultAccess().(winsvc.CLIAccess)
	if !ok || a.AgentAccount != "mestre-agente" {
		t.Fatalf("default access = %#v", localchan.DefaultAccess())
	}
}
