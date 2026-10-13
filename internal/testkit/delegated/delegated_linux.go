// Package delegated runs a test again in a systemd scope with a delegated
// cgroup (Delegate=yes), which is what the unit of the mestre-launcher has and
// what lets a terminal get a cgroup of its own. The full suite runs as ordinary
// processes, usually with no delegated cgroup, so the parts of CA-TERM-05,
// CA-TERM-07 and CA-LOCAL-11 that need one re-execute themselves under
// `systemd-run --user --scope -p Delegate=yes` instead of being skipped. Where
// there is no systemd user manager (a CI runner, a container), the test is
// skipped with the reason, and that part runs on a machine that has one
// (docs/qualidade/testes.md#cgroup-delegado-na-completa).
package delegated

import (
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"
)

// envInScope tells the test binary it is the re-execution, in the scope.
const envInScope = "MESTRE_TEST_IN_SCOPE"

// InScope reports whether this process is the re-execution.
func InScope() bool { return os.Getenv(envInScope) != "" }

// Rerun runs t (and only t, subtests included in its name) again in a
// delegated scope and reports what that run said: it fails t when the run
// failed, skips it when the scope is not available or the run skipped, and
// returns when it passed. The caller returns right after. env is added to the
// environment of the run (the test binaries it has built, for one).
//
// In the re-execution itself it skips: the scope it was given has no
// delegated cgroup either.
func Rerun(t *testing.T, env ...string) {
	t.Helper()
	if InScope() {
		t.Skip("the scope systemd gave this test has no delegated cgroup for the user")
	}
	if _, err := exec.LookPath("systemd-run"); err != nil {
		t.Skip("no systemd-run on this machine: a delegated cgroup needs a systemd user manager")
	}
	scope := func(args ...string) *exec.Cmd {
		return exec.CommandContext(t.Context(), "systemd-run",
			append([]string{"--user", "--scope", "--quiet", "-p", "Delegate=yes", "--"}, args...)...)
	}
	if out, err := scope("true").CombinedOutput(); err != nil {
		t.Skipf("no systemd user manager to delegate a cgroup (systemd-run --user --scope: %v: %s)", err, strings.TrimSpace(string(out)))
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := scope(self, "-test.run", pattern(t.Name()), "-test.count=1", "-test.v")
	cmd.Env = append(append(os.Environ(), envInScope+"=1"), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("in the delegated scope: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "--- SKIP") {
		t.Skipf("skipped in the delegated scope:\n%s", out)
	}
	t.Logf("in the delegated scope:\n%s", out)
}

// pattern is the -test.run value that selects exactly the test of that name,
// one anchored element per level.
func pattern(name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = "^" + regexp.QuoteMeta(p) + "$"
	}
	return strings.Join(parts, "/")
}
