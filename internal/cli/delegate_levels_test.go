package cli_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/cli"
)

// CA-CLI-GM-12 (`approval delegate`): a common character and the Mestre da mesa
// receive 3 level_denied; only the Grand Master delegates.
func TestApprovalDelegateIsOnlyForTheGrandMaster(t *testing.T) {
	t.Parallel()
	spec, ok := cli.Default().Get("approval delegate")
	if !ok {
		t.Fatal("approval delegate is not defined")
	}
	for role, want := range map[cli.Role]bool{cli.RoleCharacter: false, cli.RoleTableMaster: false, cli.RoleGrandMaster: true} {
		refusal, allowed := spec.Check(cli.Profile{Role: role})
		if allowed != want || (!allowed && (refusal.Error == nil || refusal.Error.Code != "level_denied" || refusal.ExitCode() != 3)) {
			t.Errorf("role %v: allowed=%v %+v, want allowed=%v", role, allowed, refusal.Error, want)
		}
	}
}
