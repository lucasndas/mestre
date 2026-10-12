//go:build !full

package testkit_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/testkit"
)

// CA-TREINO-04
func TestNewProjectFastSuiteUsesFakeGit(t *testing.T) {
	t.Parallel()
	a := testkit.NewProject(t, "go-basico")
	b := testkit.NewProject(t, "go-basico")

	if _, err := os.Stat(filepath.Join(a.Dir, ".git")); !os.IsNotExist(err) {
		t.Fatalf("fast-suite copy has a real repository: %v", err)
	}
	if a.Git == nil || !slices.Equal(a.Git.Branches(), []string{testkit.ProjectBranch}) {
		t.Fatalf("fake git = %v", a.Git)
	}
	for _, rel := range copyFiles(t, a.Dir) {
		disk, err := os.ReadFile(filepath.Join(a.Dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		if got, err := a.Git.ReadFile(testkit.ProjectBranch, rel); err != nil || got != string(disk) {
			t.Fatalf("fake git %s = %q, %v; disk has %q", rel, got, err, disk)
		}
	}
	if a.Head == "" || a.Head != b.Head {
		t.Fatalf("heads %q and %q differ", a.Head, b.Head)
	}
}
