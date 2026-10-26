package classes_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mestre-ia/mestre/internal/classes"
	"github.com/mestre-ia/mestre/internal/providers"
)

// A command argument link/../x is where the system writes it: on Linux next
// to the link's target, here outside the worktree.
func TestCommandArgumentThroughALinkAndDotDot(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(root, "wt")
	outside := filepath.Join(root, "secret", "deep")
	for _, d := range []string{filepath.Join(wt, "permitido"), outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(wt, "permitido", "lnk")); err != nil {
		t.Skipf("no symbolic links here: %v", err)
	}
	env := classes.Env{AgentID: agentID, Worktree: wt, Exceptions: newLedger(), Normalize: providers.Options{}.NormalizePath}
	r := rules(t, "version: 1\nfiles: {write: [\"permitido/**\"]}\ncommands: {default: allow}\n")
	ev := event(&providers.Tool{NativeName: "Bash", Kind: providers.KindShell, Command: "touch permitido/lnk/../x.txt"}, wt)
	if runtime.GOOS == "windows" {
		// Windows takes the ".." out of the text: x.txt is in permitido.
		want(t, decide(env, r, ev), classes.Allow, "")
	} else {
		want(t, decide(env, r, ev), classes.Deny, classes.LayerClass, "fora do worktree")
	}
	ev.Tool.Command = "touch permitido/novo/../x.txt"
	want(t, decide(env, r, ev), classes.Allow, "")
}
