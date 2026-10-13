//go:build !full

package testkit_test

import (
	"maps"
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
	// One commit on main, whose tree is exactly the files on disk.
	bundle, err := a.Git.Bundle(testkit.ProjectBranch)
	if err != nil {
		t.Fatal(err)
	}
	if len(bundle.Commits) != 1 {
		t.Fatalf("fake git has %d commits on %s, want exactly 1", len(bundle.Commits), testkit.ProjectBranch)
	}
	c := bundle.Commits[0]
	if string(c.Hash) != a.Head || c.Parent != "" || c.Message != testkit.ProjectCommitMessage {
		t.Fatalf("commit = %s (parent %q, %q), Head = %s", c.Hash, c.Parent, c.Message, a.Head)
	}
	onDisk := map[string]string{}
	for _, rel := range copyFiles(t, a.Dir) {
		disk, err := os.ReadFile(filepath.Join(a.Dir, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		onDisk[rel] = string(disk)
	}
	if !maps.Equal(c.Files, onDisk) {
		t.Fatalf("fake git tree differs from the disk\n tree: %v\n disk: %v", slices.Sorted(maps.Keys(c.Files)), slices.Sorted(maps.Keys(onDisk)))
	}
	if a.Head == "" || a.Head != b.Head {
		t.Fatalf("heads %q and %q differ", a.Head, b.Head)
	}
}
