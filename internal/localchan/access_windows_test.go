package localchan_test

import (
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
	"github.com/lucasndas/mestre/internal/winsvc"
)

// The ACL itself, with real SIDs and a socket, is internal/winsvc's test.
//
// CA-LOCAL-15
// The ACL is for the account of the agents of its own instance: mestre-agente
// for the principal one, mestre-agente-<inst> for the others.
func TestTheDefaultAccessIsTheOneOfTheInstance(t *testing.T) {
	t.Parallel()
	for instance, account := range map[string]string{
		"": "mestre-agente", localchan.DefaultInstance: "mestre-agente", "x": "mestre-agente-x", "ci_2": "mestre-agente-ci_2",
	} {
		a, ok := localchan.DefaultAccess(instance).(winsvc.CLIAccess)
		if !ok || a.AgentAccount != account || a.AgentAccount != winsvc.AgentAccountName(instance) {
			t.Errorf("access of instance %q = %#v, want the account %s", instance, localchan.DefaultAccess(instance), account)
		}
	}
}
