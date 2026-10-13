//go:build full && windows

package localchan_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lucasndas/mestre/internal/localchan"
)

// CA-LOCAL-13, Windows only because the Job Object is (its pair on Linux is
// CA-LOCAL-11, with the cgroup): a real terminal whose process calls
// `mestre list` directly, through `cmd /c`, through `powershell -c` and as an
// orphan of `cmd /c start "" /b` whose cmd already left; all four are
// accepted with the right terminal, by the Job Object asked of the
// mestre-pty. A process of the same account outside the terminal is refused.
//
// CA-CLI-CONV-03 (the outside process, on Windows)
func TestTheJobObjectIdentifiesTheCallersOfATerminal(t *testing.T) {
	t.Parallel()
	e := newFullEngine(t, engineOptions{})
	const agent = "char-13"
	base := filepath.Join(e.root, "calls")
	modes := []string{"direct", "cmd", "powershell", "orphan"}
	for _, m := range modes {
		if err := os.MkdirAll(filepath.Join(base, m), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e.spawn("job", agent, self, []string{helperCaller, builds.mestre, base, "direct,cmd,powershell,orphan"}, e.env(agent), e.root)
	out := e.watch("job")

	seen := map[string]bool{}
	for range modes {
		c := e.nextCall("a call from inside the terminal (" + out.String() + ")")
		if !c.Caller.Identified || c.Caller.AgentID != agent || c.Caller.TerminalID != "job" {
			t.Fatalf("call from %s: %+v", c.Cwd, c.Caller)
		}
		seen[filepath.Base(c.Cwd)] = true
	}
	for _, m := range modes {
		if !seen[m] {
			t.Errorf("no accepted call from %q: %v\n%s", m, seen, out.String())
		}
	}

	exit, stdout := e.outside(agent)
	if exit != localchan.ExitDenied {
		t.Fatalf("a process outside the terminal: exit %d, %s", exit, stdout)
	}
	if a := e.nextRefusal("the outside process"); a.AgentID != agent {
		t.Fatalf("audit = %+v", a)
	}
	e.noMoreCalls()
}
