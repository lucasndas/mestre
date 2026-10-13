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

func TestTheDefaultAccessIsTheOneOfTheDocument(t *testing.T) {
	t.Parallel()
	a, ok := localchan.DefaultAccess().(localchan.LinuxAccess)
	if !ok || a.Mode != 0o660 || a.Group != "mestre-agente" {
		t.Fatalf("default access = %#v", localchan.DefaultAccess())
	}
}
