package sessionzero_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mestre-ia/mestre/internal/sessionzero"
)

// CA-SZ-WS-09 (docs/sessao-zero/workspace.md): the files of .mestre/regras/ are read by the
// agents and written only by the engine and the people. The structure is born without
// write for the group, and a file an earlier version left with it is tightened (what
// is in the file is not touched).
func TestTheFilesOfTheRulesAreNotWritableByTheGroupOfTheAgents(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("the ACL of the folder decides on Windows")
	}
	root := t.TempDir()
	old := filepath.Join(root, ".mestre", "regras", "politica-de-aprovacao.yaml")
	write(t, old, "versao: 1\n")
	if err := os.Chmod(old, 0o664); err != nil {
		t.Fatal(err)
	}
	if rep := sessionzero.EnsureStructure(root); len(rep.Invalid) != 0 {
		t.Fatalf("invalid = %v", rep.Invalid)
	}
	for _, rel := range []string{"regras/teto.yaml", "regras/politica-de-aprovacao.yaml", "regras/instrucoes/grao-mestre.md", "regras/instrucoes/mestre-da-mesa.md"} {
		st, err := os.Stat(filepath.Join(root, ".mestre", filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o022 != 0 {
			t.Errorf("%s has mode %o: the group of the agents can write it", rel, st.Mode().Perm())
		}
	}
	if got := read(t, old); got != "versao: 1\n" {
		t.Errorf("the policy was rewritten: %q", got)
	}
}
