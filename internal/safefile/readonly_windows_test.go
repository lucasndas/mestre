package safefile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/safefile"
)

// The READONLY attribute that is cleared to replace a file is the file's own: a
// link put where the copy was (by an agent) is replaced as a link, and what it
// pointed at keeps its content and its attribute.
func TestReplacingALinkDoesNotClearTheReadOnlyOfItsTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	target := filepath.Join(dir, "alvo.txt")
	if err := safefile.Write(target, []byte("do alvo"), safefile.Options{Mode: 0o444}); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "copia.md")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("no privilege to make a link: %v", err)
	}
	if err := safefile.Write(link, []byte("nova"), safefile.Options{Mode: 0o444}); err != nil {
		t.Fatalf("replace the link: %v", err)
	}
	if b, _ := os.ReadFile(target); string(b) != "do alvo" {
		t.Errorf("the target changed: %q", b)
	}
	if f, err := os.OpenFile(target, os.O_WRONLY|os.O_APPEND, 0); err == nil {
		_ = f.Close()
		t.Error("the target is writable: its READONLY was cleared through the link")
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink != 0 {
		t.Errorf("the copy is still a link: %v %v", st, err)
	}
}
