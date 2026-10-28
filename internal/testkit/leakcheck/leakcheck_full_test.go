//go:build full && linux

package leakcheck_test

import (
	"bufio"
	"bytes"
	"os/exec"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/testkit/leakcheck"
)

// The guard of the rigs: a process that holds the needle (and its child) is
// found, makes the guard of a TestMain fail naming it, and is ended; with
// nothing alive the guard passes the code through.
func TestTheGuardFailsWhenAProcessOfTheTestIsLeftAlive(t *testing.T) {
	t.Parallel()
	needle := t.TempDir()
	var none bytes.Buffer
	if got := leakcheck.Main(&none, 0, needle); got != 0 || none.Len() != 0 {
		t.Fatalf("nothing alive: Main = %d, wrote %q", got, none.String())
	}

	// The shell stays alive until its stdin closes. It says it is running before
	// it waits: Start returns when the exec took the descriptors of the parent,
	// before the kernel has the command line in place (/proc/<pid>/cmdline is
	// empty in that window), so the test looks for the shell only after hearing it.
	cmd := exec.Command("sh", "-c", "echo up; cat >/dev/null; :", needle+"/leaked")
	in, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	up, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	if line, err := bufio.NewReader(up).ReadString('\n'); err != nil || line != "up\n" {
		t.Fatalf("the shell did not say it is up: %q, %v", line, err)
	}
	waited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(waited) }()
	t.Cleanup(func() { _ = in.Close(); <-waited })

	if ps := leakcheck.Find(needle[:len(needle)-1]); len(ps) != 0 {
		t.Fatalf("a folder that is a prefix of the needle matched: %v", ps)
	}
	if ps := leakcheck.Find(needle); len(ps) == 0 {
		t.Fatalf("Find = %v, want the shell", ps)
	}
	var out bytes.Buffer
	if got := leakcheck.Main(&out, 0, needle); got != 1 {
		t.Errorf("Main = %d, want 1", got)
	}
	if !strings.Contains(out.String(), needle) {
		t.Errorf("the report does not name the process:\n%s", out.String())
	}
	<-waited
	if ps := leakcheck.Find(needle); len(ps) != 0 {
		t.Errorf("still alive after the guard: %v", ps)
	}
}
