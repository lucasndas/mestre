package classes_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lucasndas/mestre/internal/classes"
	"github.com/lucasndas/mestre/internal/safefile"
)

// The native files are read by the agents, which run as another account than
// the engine; agy writes its settings.json 0600, and the renamed file belongs
// to the engine, so it always gets the mode of the rule (0664, the group of
// the folder), never a copy of the old mode, and an old file with another
// mode is written again even with the same content (no chmod by path)
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
		if st.Mode().Perm() != safefile.Mode {
			t.Errorf("%s has mode %o, want %o", filepath.Base(p), st.Mode().Perm(), safefile.Mode)
		}
	}
}

// CA-NATIVO-10
//
// C2: an agent puts a link to an engine file in place of a native file, or
// of a folder of its path (.mestre, .agents, .claude). The engine does not
// copy the engine file into a file the agents read, does not write over it,
// and does not change its mode.
func TestOSNativeFilesRefuseLinks(t *testing.T) {
	t.Parallel()
	secretDir := t.TempDir()
	secret := filepath.Join(secretDir, "engine.key")
	if err := os.WriteFile(secret, []byte("-----BEGIN PRIVATE KEY-----\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	wt := t.TempDir()
	settings := filepath.Join(wt, ".mestre", "agents", "a1", "claude-settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	link := func(target, at string) {
		t.Helper()
		if err := os.Symlink(target, at); err != nil {
			if runtime.GOOS == "windows" {
				t.Skipf("symbolic links need a privilege here (internal/safefile tests the junctions): %v", err)
			}
			t.Fatal(err)
		}
	}
	link(secret, settings)
	link(secretDir, filepath.Join(wt, ".agents"))
	var f classes.OSNativeFiles
	merge := func(cur []byte) ([]byte, error) {
		if strings.Contains(string(cur), "PRIVATE") {
			t.Error("the merge got the engine file")
		}
		return append(cur, "{}"...), nil
	}
	for _, p := range []string{settings, filepath.Join(wt, ".agents", "engine.key"), filepath.Join(wt, ".agents", "agents", "mestre-a1.md")} {
		if _, err := f.Update(t.Context(), p, merge); !errors.Is(err, safefile.ErrLink) {
			t.Errorf("update of %s = %v, want ErrLink", p, err)
		}
	}
	b, err := os.ReadFile(secret)
	if err != nil || string(b) != "-----BEGIN PRIVATE KEY-----\n" {
		t.Fatalf("the engine file changed: %q, %v", b, err)
	}
	if entries, _ := os.ReadDir(secretDir); len(entries) != 1 {
		t.Fatalf("files appeared in the engine's folder: %v", entries)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(secret); st.Mode().Perm() != 0o600 {
			t.Fatalf("the engine file's mode changed to %o", st.Mode().Perm())
		}
	}
}

// C6: ReadDir lists the folder and then reads each file; a file swapped for
// a link, or a folder of the path that is a link, is refused at the reading.
func TestReadDirRefusesALinkOnThePathOfAFile(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("internal/safefile tests the junctions")
	}
	secretDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(secretDir, "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(secretDir, "qa", "CLASS.md"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.Symlink(secretDir, filepath.Join(root, "classes")); err != nil {
		t.Fatal(err)
	}
	if _, err := classes.ReadDir(filepath.Join(root, "classes")); err == nil {
		t.Fatal("a class folder that is a link was read")
	}
	// The folder of the class is a real folder, but under a linked one: the
	// listing sees a regular CLASS.md, and the reading refuses it.
	if _, err := classes.ReadDir(filepath.Join(root, "classes", "qa")); !errors.Is(err, classes.ErrPackage) || !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("a class under a linked folder = %v, want ErrPackage and ErrLink", err)
	}
	inner := filepath.Join(root, "real")
	if err := os.MkdirAll(filepath.Join(inner, "qa"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secretDir, filepath.Join(inner, "qa", "hooks")); err != nil {
		t.Fatal(err)
	}
	if _, err := classes.ReadDir(filepath.Join(inner, "qa")); !errors.Is(err, classes.ErrPackage) {
		t.Fatalf("a package with a linked folder = %v, want ErrPackage", err)
	}
}
