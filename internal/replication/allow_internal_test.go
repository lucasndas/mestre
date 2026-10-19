package replication

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// Part of CA-REPL-17: a folder that carries only some paths sends and takes
// only those.
func TestAFolderThatCarriesOnlySomePathsSendsAndTakesOnlyThose(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, p := range []string{"ws/principal/.mestre/a.md", "ws/principal/src/b.go", "ws/mesas/c.txt", "ws/d.txt"} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, filepath.FromSlash(p))), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(p)), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var askedDirs []string
	f, err := NewFiles(FileRoot{Name: "ws", Dir: dir, Walk: true, Allow: func(rel string, isDir bool) bool {
		if isDir {
			askedDirs = append(askedDirs, rel)
			return rel == "ws" || rel == "ws/principal" || strings.HasPrefix(rel, "ws/principal/.mestre")
		}
		return strings.HasPrefix(rel, "ws/principal/.mestre/")
	}})
	if err != nil {
		t.Fatal(err)
	}
	refs, err := f.walk("ws")
	if err != nil || len(refs) != 1 || refs[0].Path != "ws/principal/.mestre/a.md" {
		t.Fatalf("walk = %v, %v; want only the allowed file", refs, err)
	}
	if slices.Contains(askedDirs, "ws/mesas") && slices.ContainsFunc(askedDirs, func(d string) bool { return strings.HasPrefix(d, "ws/mesas/") }) {
		t.Fatalf("a folder that is not allowed was entered: %v", askedDirs)
	}
	for _, p := range []string{"ws/principal/src/b.go", "ws/mesas/c.txt", "ws/d.txt"} {
		if _, _, err := f.checkRef(FileRef{Root: "ws", Path: p}); !errors.Is(err, ErrPathEscapes) {
			t.Errorf("%s: %v; want the path refused", p, err)
		}
	}
	if _, _, err := f.checkRef(FileRef{Root: "ws", Path: "ws/principal/.mestre/a.md"}); err != nil {
		t.Errorf("an allowed path was refused: %v", err)
	}
}
