package safefile_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mestre-ia/mestre/internal/safefile"
)

// Part of CA-SZ-WS-09 (the files of regras/): Tighten takes the write of the group and of the
// others off a file an earlier version wrote 0664, keeps the rest of the mode, does
// not follow a link and leaves alone one that is already tight.
func TestTightenTakesTheWriteOfTheGroupAndTheOthersOff(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "politica-de-aprovacao.yaml")
	if err := os.WriteFile(path, []byte("versao: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	if err := safefile.Tighten(path); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %o, want 644", st.Mode().Perm())
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := safefile.Tighten(path); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o640 {
		t.Errorf("a tight file changed to %o", st.Mode().Perm())
	}
	link := filepath.Join(dir, "link.yaml")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if err := safefile.Tighten(link); err == nil {
		t.Error("Tighten followed a link")
	}
}

// RulesMode is the mode the engine writes the files the agents read and do not write.
func TestRulesModeIsReadOnlyForTheGroup(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "regras", "teto.yaml")
	if err := safefile.Write(path, []byte("x"), safefile.Options{Mode: safefile.RulesMode}); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm()&0o022 != 0 {
		t.Errorf("mode = %o: the group or the others can write it", st.Mode().Perm())
	}
}
