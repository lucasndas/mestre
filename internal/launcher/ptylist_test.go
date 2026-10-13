package launcher_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/lucasndas/mestre/internal/launcher"
)

func TestPTYList(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "state", "ptys.json")
	l, err := launcher.OpenPTYList(path)
	if err != nil || len(l.Entries()) != 0 {
		t.Fatalf("open = %v, %v", l.Entries(), err)
	}
	b := launcher.PTYEntry{ID: "b", PID: 20, Start: "133"}
	a := launcher.PTYEntry{ID: "a", PID: 10, Start: "132"}
	for _, e := range []launcher.PTYEntry{b, a} {
		if err := l.Add(e); err != nil {
			t.Fatal(err)
		}
	}
	// A new launcher reads what the old one wrote.
	again, err := launcher.OpenPTYList(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := again.Entries(); !slices.Equal(got, []launcher.PTYEntry{a, b}) {
		t.Errorf("entries = %v", got)
	}
	if err := again.Remove("a"); err != nil {
		t.Fatal(err)
	}
	if err := again.Remove("zzz"); err != nil {
		t.Fatal(err)
	}
	third, _ := launcher.OpenPTYList(path)
	if got := third.Entries(); !slices.Equal(got, []launcher.PTYEntry{b}) {
		t.Errorf("entries after remove = %v", got)
	}
	// No temporary file is left behind.
	if es, _ := os.ReadDir(filepath.Dir(path)); len(es) != 1 {
		t.Errorf("folder has %d files", len(es))
	}
}

func TestPTYListCorruptFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "ptys.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	l, err := launcher.OpenPTYList(path)
	if err == nil {
		t.Error("a corrupt list was read without a word")
	}
	if l == nil || len(l.Entries()) != 0 {
		t.Errorf("the list must be usable and empty: %v", l)
	}
	if err := l.Add(launcher.PTYEntry{ID: "a", PID: 1, Start: "s"}); err != nil {
		t.Errorf("a corrupt file must not block the next write: %v", err)
	}
}
