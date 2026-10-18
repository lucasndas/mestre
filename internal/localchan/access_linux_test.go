package localchan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
)

// The mode and owner of the socket on Linux are file attributes, so this
// test is Linux only and needs no socket: the same Access is applied to
// the socket file by Listen (TestListenAppliesTheAccess, full suite, both
// systems). On Windows the ACL is internal/winsvc's.
func TestLinuxAccessSetsTheMode(t *testing.T) {
	t.Parallel()
	p := filepath.Join(t.TempDir(), "cli.sock")
	if err := os.WriteFile(p, nil, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := (localchan.LinuxAccess{Mode: 0o660}).Apply(p); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil || st.Mode().Perm() != 0o660 {
		t.Fatalf("mode = %v, %v", st.Mode(), err)
	}
	if err := (localchan.LinuxAccess{Mode: 0o660, Group: "no-such-group-mestre"}).Apply(p); err == nil {
		t.Fatal("an unknown group was accepted")
	}
}

// CA-LOCAL-15
// The socket is for the account of the agents of its own instance: mestre-agente
// for the principal one, mestre-agente-<inst> for the others (the agents of
// an instance reach its channel and not the channel of another).
func TestTheDefaultAccessIsTheOneOfTheInstance(t *testing.T) {
	t.Parallel()
	for instance, group := range map[string]string{
		"": "mestre-agente", localchan.DefaultInstance: "mestre-agente", "x": "mestre-agente-x", "ci_2": "mestre-agente-ci_2",
	} {
		a, ok := localchan.DefaultAccess(instance).(localchan.LinuxAccess)
		if !ok || a.Mode != 0o660 || a.Group != group {
			t.Errorf("access of instance %q = %#v, want mode 0660 and the group %s", instance, localchan.DefaultAccess(instance), group)
		}
	}
}
