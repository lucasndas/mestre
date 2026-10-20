package providers

import (
	"strings"
	"testing"
)

// Part of CA-AGY-06 (the shape of the Windows line, checked on any system): no quote
// in the line, the neutral answer of PreToolUse built by PowerShell without
// running the mestre, {} for the other events, and {} (blocks) when the
// binary is gone.
func TestTheWindowsHookLineHasNoQuoteAndAnswersAllowWithoutTheAgentID(t *testing.T) {
	t.Parallel()
	a := &antigravity{}
	pre, err := a.hookCommand("windows", `C:\mestre\mestre.exe`, "PreToolUse")
	if err != nil {
		t.Fatal(err)
	}
	stop, err := a.hookCommand("windows", `C:\mestre\mestre.exe`, "Stop")
	if err != nil {
		t.Fatal(err)
	}
	for name, line := range map[string]string{"PreToolUse": pre, "Stop": stop} {
		if strings.Contains(line, `"`) {
			t.Errorf("%s: the line has a quote: %s", name, line)
		}
		if !strings.HasSuffix(line, "else (echo {})") || !strings.HasPrefix(line, "if not defined MESTRE_AGENT_ID (") {
			t.Errorf("%s: unexpected shape: %s", name, line)
		}
	}
	if !strings.Contains(pre, `'{'+[char]34+'decision'+[char]34+':'+[char]34+'allow'+[char]34+'}'`) {
		t.Errorf("PreToolUse without the agent id does not print allow: %s", pre)
	}
	if !strings.HasPrefix(stop, "if not defined MESTRE_AGENT_ID (echo {}) else") {
		t.Errorf("Stop without the agent id: %s", stop)
	}
}
