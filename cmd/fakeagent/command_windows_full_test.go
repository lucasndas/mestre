//go:build full && windows

package main

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/testkit/fakeagent"
)

// CA-DUBLES-13: the double runs an Antigravity hook as the real agy does,
// cmd /c with the line as one Go-quoted argument, not the cmd /s /c form.
func TestAntigravityHookRunsAsCmdSlashCWithGoQuoting(t *testing.T) {
	t.Parallel()
	const line = `echo "two words" & echo done`
	c := hookCommand(context.Background(), fakeagent.Antigravity, line, t.TempDir(), nil)
	if c.SysProcAttr != nil && c.SysProcAttr.CmdLine != "" {
		t.Fatalf("custom command line %q: the real agy lets Go quote the arguments", c.SysProcAttr.CmdLine)
	}
	if want := []string{"cmd", "/c", line}; !reflect.DeepEqual(c.Args, want) {
		t.Fatalf("Args = %q, want %q", c.Args, want)
	}
	for _, p := range []fakeagent.Provider{fakeagent.Claude, fakeagent.Codex} {
		if o := hookCommand(context.Background(), p, line, t.TempDir(), nil); len(o.Args) > 1 && o.Args[1] == "/c" {
			t.Fatalf("%v hook uses cmd /c", p)
		}
	}
	out, err := hookCommand(context.Background(), fakeagent.Antigravity, "echo hello", t.TempDir(), nil).CombinedOutput()
	if err != nil || !strings.Contains(string(out), "hello") {
		t.Fatalf("echo through cmd /c = %q, %v", out, err)
	}
}
