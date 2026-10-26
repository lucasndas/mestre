package cli_test

import (
	"testing"

	"github.com/mestre-ia/mestre/internal/cli"
)

// CA-CLI-GM-12 (the level of the commands of approval): a common character is
// refused `approval decide`, `approval list` and `approval autonomy` with 3
// level_denied; the Mestre da mesa decides and lists but does not pause the
// autonomy, which is the Grand Master's; `approval reply` is for whoever asked.
func TestTheCommandsOfApprovalAreForTheLevelsTheDocumentGives(t *testing.T) {
	t.Parallel()
	reg := cli.Default()
	for _, c := range []struct {
		cmd  string
		role cli.Role
		ok   bool
	}{
		{"approval decide", cli.RoleCharacter, false}, {"approval decide", cli.RoleTableMaster, true}, {"approval decide", cli.RoleGrandMaster, true},
		{"approval list", cli.RoleCharacter, false}, {"approval list", cli.RoleTableMaster, true}, {"approval list", cli.RoleGrandMaster, true},
		{"approval autonomy", cli.RoleCharacter, false}, {"approval autonomy", cli.RoleTableMaster, false}, {"approval autonomy", cli.RoleGrandMaster, true},
		{"approval reply", cli.RoleCharacter, true}, {"approval reply", cli.RoleTableMaster, true}, {"approval reply", cli.RoleGrandMaster, true},
	} {
		spec, ok := reg.Get(c.cmd)
		if !ok {
			t.Fatalf("%s is not defined", c.cmd)
		}
		refusal, allowed := spec.Check(cli.Profile{Role: c.role})
		if allowed != c.ok || (!allowed && (refusal.Error == nil || refusal.Error.Code != "level_denied" || refusal.ExitCode() != 3)) {
			t.Errorf("%s by %v: allowed=%v %+v, want allowed=%v", c.cmd, c.role, allowed, refusal.Error, c.ok)
		}
	}
}
