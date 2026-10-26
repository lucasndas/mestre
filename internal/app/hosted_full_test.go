//go:build full

package app_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/app"
	"github.com/mestre-ia/mestre/pkg/clock"
)

// CA-CIDADELA-15: `mestre run` hosts the orchestration. The process of the
// Citadel (app.Main) answers the commands of the orchestration, which it did
// not before: a command that only the orchestration registers is no longer
// unknown, and it reaches the registry of characters of the main database.
func TestMestreRunHostsTheOrchestrationOfTheCitadel(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	ports := freePorts(t, 1)
	a := w.add("a", ports[0], nil)
	founded, rk, err := app.Found(t.Context(), a.cfg, app.DirPaths(a.dir), clock.Real{})
	if err != nil {
		t.Fatal(err)
	}
	rk.Destroy()
	if err := os.WriteFile(filepath.Join(w.lock, "epoca-1"), []byte(founded.MachineID), 0o600); err != nil {
		t.Fatal(err)
	}
	w.start(a)
	w.waitForCitadel(a)

	// `inbox` is a command of the orchestration; "ghost" is no character of the Guild
	got := w.must(a, map[string]string{"op": "call", "cmd": "inbox", "agent": "ghost"})
	if got.Call == nil || got.Call.Error == nil {
		t.Fatalf("the command of a character that does not exist: %+v", got.Call)
	}
	if strings.Contains(got.Call.Error.Message, "unknown command") {
		t.Fatalf("the engine does not host the orchestration: %+v", got.Call.Error)
	}
	// a command that does not exist is still unknown
	if got := w.must(a, map[string]string{"op": "call", "cmd": "nothing-here", "agent": "ghost"}); got.Call == nil || got.Call.Error == nil || !strings.Contains(got.Call.Error.Message, "unknown command") {
		t.Fatalf("a command nobody registers: %+v", got.Call)
	}
}
