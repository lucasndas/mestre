//go:build full && linux

package localchan_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/localchan"
)

// CA-CLI-CONV-03 on Linux as the installed engine identifies, by the
// cgroup of the terminal (a real delegated one): the process inside the
// terminal is accepted, and a process of the same account with the id of
// the character, outside the cgroup of the terminal, is refused with 3 and
// not_identified. TestAnOutsideProcessWithTheIdOfACharacterIsRefused is the
// same case by the process tree, on both systems.
//
// CA-CLI-CONV-03
func TestAnOutsideProcessIsRefusedByTheCgroupOfTheTerminal(t *testing.T) {
	t.Parallel()
	cgroup, inside := inDelegatedScope(t, "TestAnOutsideProcessIsRefusedByTheCgroupOfTheTerminal")
	if !inside {
		return
	}
	e := newFullEngine(t, engineOptions{cgroup: cgroup, mode: localchan.ModeCgroup})
	const agent = "char-3c"
	base := filepath.Join(e.root, "calls")
	if err := os.MkdirAll(filepath.Join(base, "direct"), 0o755); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	e.spawn("t3c", agent, self, []string{helperCaller, builds.mestre, base, "direct"}, e.env(agent), e.root)
	e.watch("t3c").waitFor(t, "DONE")
	if c := e.nextCall("the process inside the cgroup of the terminal"); !c.Caller.Identified || c.Caller.TerminalID != "t3c" {
		t.Fatalf("inside: %+v", c.Caller)
	}

	exit, stdout := e.outside(agent)
	var v struct {
		OK    bool
		Error struct{ Code string }
	}
	if err := json.Unmarshal([]byte(stdout), &v); err != nil {
		t.Fatalf("stdout %q: %v", stdout, err)
	}
	if exit != 3 || v.OK || v.Error.Code != "not_identified" {
		t.Fatalf("outside the cgroup: exit %d, %s", exit, stdout)
	}
	if a := e.nextRefusal("the process outside the cgroup"); a.AgentID != agent {
		t.Fatalf("audit = %+v", a)
	}
	e.noMoreCalls()
}
