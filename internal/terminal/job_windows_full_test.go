//go:build full && windows

package terminal_test

import (
	"os"
	"path/filepath"
	"testing"
)

// CA-TERM-05 (Windows: nothing leaves the Job Object). The Job of the terminal
// has no JOB_OBJECT_LIMIT_BREAKAWAY_OK, so a process inside it that asks for
// CREATE_BREAKAWAY_FROM_JOB is refused, instead of being born outside what
// TerminateJobObject reaches and what identifies the caller.
func TestNoProcessLeavesTheJobObject(t *testing.T) {
	t.Parallel()
	h := newHarness(t, harnessOptions{})
	result := filepath.Join(h.cfg.Root, "break")
	h.create(h.selfSpec("t5j", helperBreak, result))
	out, _ := h.observe(h.record("t5j"))
	out.waitFor(t, "READY")

	got, err := os.ReadFile(result)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "denied" {
		t.Fatalf("CREATE_BREAKAWAY_FROM_JOB inside the Job of the terminal: %q, want it refused with access denied", got)
	}
}
