package classes_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
)

// The native files are read by the agents, which run as another account than
// the engine; agy writes its settings.json 0600, and the renamed file belongs
// to the engine, so it must stay readable by the group and the others
// (docs/classes/reforco-nativo.md#quando-as-regras-mudam).
func TestOSNativeFilesStayReadableByTheAgentsAccount(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("file modes are Unix permissions; on Windows the folder's ACL decides")
	}
	var f classes.OSNativeFiles
	dir := t.TempDir()
	changed := filepath.Join(dir, "changed.json")
	same := filepath.Join(dir, "same.json")
	for _, p := range []string{changed, same} {
		if err := os.WriteFile(p, []byte("a"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if c, err := f.Update(t.Context(), changed, func([]byte) ([]byte, error) { return []byte("b"), nil }); err != nil || !c {
		t.Fatalf("changed = %v, %v", c, err)
	}
	if c, err := f.Update(t.Context(), same, func(cur []byte) ([]byte, error) { return cur, nil }); err != nil || c {
		t.Fatalf("same = %v, %v; want unchanged", c, err)
	}
	for _, p := range []string{changed, same} {
		st, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm()&0o644 != 0o644 {
			t.Errorf("%s has mode %o, want at least 0644", filepath.Base(p), st.Mode().Perm())
		}
	}
}
