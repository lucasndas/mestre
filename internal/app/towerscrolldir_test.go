package app

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mestre-ia/mestre/internal/orchestration/scrolls"
	"github.com/mestre-ia/mestre/internal/protocol"
)

// The worktree a Tower is told about is judged by its real path, whatever the
// spelling of the folder of the workspaces (a link here; on Windows the case or a
// short name): the Citadel gets the dir from table.where in that spelling and the
// Tower must not refuse its own worktree, nor accept another table's.
func TestAScrollIsAcceptedInTheWorktreeOfItsTableWhateverTheSpellingOfTheWorkspacesFolder(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	if r, err := filepath.EvalSymlinks(base); err == nil {
		base = r
	}
	real := filepath.Join(base, "real")
	table := "3-login"
	tree := filepath.Join(real, "meu-app", "mesas", table)
	if err := os.MkdirAll(tree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(real, "meu-app", "mesas", "9-outra"), 0o755); err != nil {
		t.Fatal(err)
	}
	spelled := real
	if runtime.GOOS == "windows" {
		spelled = strings.ToUpper(real) // same folder, another spelling
	} else {
		spelled = filepath.Join(base, "link")
		if err := os.Symlink(real, spelled); err != nil {
			t.Skip("no links here:", err)
		}
	}
	e := &Engine{paths: Paths{Workspaces: spelled}}
	rel := scrolls.Rel(table, "p0001")
	if err := e.checkScrollDir(protocol.TypeScrollPut, filepath.Join(spelled, "meu-app", "mesas", table), rel); err != nil {
		t.Errorf("the worktree in the spelling of the workspaces folder was refused: %v", err)
	}
	if err := e.checkScrollDir(protocol.TypeScrollPut, tree, rel); err != nil {
		t.Errorf("the worktree in its real spelling was refused: %v", err)
	}
	// still only the worktree of that table
	for name, dir := range map[string]string{
		"another table":  filepath.Join(spelled, "meu-app", "mesas", "9-outra"),
		"the workspaces": spelled,
		"a folder out":   base,
	} {
		var pe *protocol.Error
		if err := e.checkScrollDir(protocol.TypeScrollPut, dir, rel); !errors.As(err, &pe) || pe.Code != protocol.CodeForbidden {
			t.Errorf("%s: %v, want forbidden", name, err)
		}
	}
}
