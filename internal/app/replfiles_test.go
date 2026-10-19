package app

import (
	"os"
	"path/filepath"
	"testing"
)

// Part of CA-REPL-17: the folders the engine copies to the Towers.
func TestTheFoldersTheEngineCopiesToTheTowers(t *testing.T) {
	t.Parallel()
	if f, err := (Paths{}).replicationFiles(); f != nil || err != nil {
		t.Fatalf("no folder: %v, %v; want none", f, err)
	}
	root := t.TempDir()
	p := DirPaths(root)
	f, err := p.replicationFiles()
	if err != nil || f == nil {
		t.Fatalf("files: %v, %v", f, err)
	}
	// the files of each folder are found where the Citadel keeps them
	for name, dir := range map[string]string{filesClasses: p.ClassesGlobais, filesTemplates: p.Modelos, filesOperator: p.Operador, filesWorkspaces: p.Workspaces} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "x"), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// a folder with no path is left out, and a relative path is made absolute
	rel := DirPaths(".")
	rel.Workspaces, rel.Modelos, rel.Operador = "", "", ""
	if g, err := rel.replicationFiles(); err != nil || g == nil {
		t.Fatalf("a relative path: %v, %v", g, err)
	}
	if replicationCites() == nil {
		t.Fatal("no record cites a file")
	}
}
