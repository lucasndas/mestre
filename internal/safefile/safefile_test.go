package safefile_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/mestre-ia/mestre/internal/safefile"
)

// secretFile is an engine file the agents must never get: 0600, with a
// content no test may find anywhere else.
func secretFile(t *testing.T) (path string, content []byte) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "engine.key")
	content = []byte("-----BEGIN PRIVATE KEY-----\nsecret\n")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, content
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestReadAndWriteAPlainFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a", "b", "settings.json")
	if _, _, err := safefile.Read(path, 0); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read of a missing file = %v, want fs.ErrNotExist", err)
	}
	if info, err := safefile.Stat(path); err != nil || info.Exists {
		t.Fatalf("stat of a missing file = %+v, %v", info, err)
	}
	if err := safefile.Write(path, []byte("one"), safefile.Options{}); err != nil {
		t.Fatal(err)
	}
	data, info, err := safefile.Read(path, 0)
	if err != nil || string(data) != "one" || !info.Exists || info.Size != 3 {
		t.Fatalf("read = %q, %+v, %v", data, info, err)
	}
	st, err := safefile.Stat(path)
	if err != nil || !st.Same(info) {
		t.Fatalf("stat = %+v, %v; want %+v", st, err, info)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(path); fi.Mode().Perm() != safefile.Mode {
			t.Errorf("mode = %o, want %o", fi.Mode().Perm(), safefile.Mode)
		}
	}
	// The temporary file never stays.
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestWriteGuardStopsTheRename(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	stop := errors.New("rewritten")
	err := safefile.Write(path, []byte("new"), safefile.Options{Guard: func(cur safefile.Info) error {
		if !cur.Exists || cur.Size != 3 {
			t.Errorf("guard saw %+v", cur)
		}
		return stop
	}})
	if !errors.Is(err, stop) {
		t.Fatalf("write = %v, want the guard's error", err)
	}
	if b := mustRead(t, path); string(b) != "old" {
		t.Fatalf("file = %q, want it untouched", b)
	}
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".*")); len(left) != 0 {
		t.Errorf("temporary files left: %v", left)
	}
}

func TestLimitAndKinds(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	big := filepath.Join(dir, "teto.yaml")
	if err := os.WriteFile(big, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := safefile.Read(big, 99); !errors.Is(err, safefile.ErrTooLarge) {
		t.Fatalf("read over the limit = %v, want ErrTooLarge", err)
	}
	if _, _, err := safefile.Read(big, 100); err != nil {
		t.Fatalf("read at the limit = %v", err)
	}
	if _, _, err := safefile.Read(dir, 0); !errors.Is(err, safefile.ErrNotRegular) {
		t.Fatalf("read of a folder = %v, want ErrNotRegular", err)
	}
	if _, err := safefile.Stat(dir); !errors.Is(err, safefile.ErrNotRegular) {
		t.Fatalf("stat of a folder = %v, want ErrNotRegular", err)
	}
	if _, _, err := safefile.Read("relative/file", 0); !errors.Is(err, safefile.ErrPath) {
		t.Fatalf("relative path = %v, want ErrPath", err)
	}
	if !safefile.Refused(safefile.ErrLink) || safefile.Refused(fs.ErrNotExist) {
		t.Fatal("Refused does not tell the refusals")
	}
}

// symlink makes a symbolic link, or skips where the account may not (on
// Windows it takes a privilege or the developer mode; the junction test
// covers that system).
func symlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symbolic links need a privilege here: %v", err)
		}
		t.Fatal(err)
	}
}

// An agent puts a link to an engine file in place of a native file: the
// engine neither reads the engine file nor writes over it, and the write
// replaces the link itself.
func TestLinkInPlaceOfTheFile(t *testing.T) {
	t.Parallel()
	secret, content := secretFile(t)
	dir := t.TempDir()
	link := filepath.Join(dir, "settings.json")
	symlink(t, secret, link)
	if _, _, err := safefile.Read(link, 0); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("read through a link = %v, want ErrLink", err)
	}
	if _, err := safefile.Stat(link); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("stat of a link = %v, want ErrLink", err)
	}
	if err := safefile.Write(link, []byte("{}"), safefile.Options{Guard: func(safefile.Info) error { return nil }}); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("guarded write over a link = %v, want ErrLink", err)
	}
	if err := safefile.Write(link, []byte("{}"), safefile.Options{}); err != nil {
		t.Fatalf("write over a link = %v", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&fs.ModeSymlink != 0 || string(mustRead(t, link)) != "{}" {
		t.Fatalf("the link was not replaced by a file: %v, %v", fi, err)
	}
	checkSecret(t, secret, content)
}

// An agent puts a link to an engine folder in place of a folder of the
// path: nothing is read from it or written into it.
func TestLinkInPlaceOfAFolder(t *testing.T) {
	t.Parallel()
	secret, content := secretFile(t)
	wt := t.TempDir()
	symlink(t, filepath.Dir(secret), filepath.Join(wt, ".mestre"))
	inside := filepath.Join(wt, ".mestre", filepath.Base(secret))
	if _, _, err := safefile.Read(inside, 0); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("read through a linked folder = %v, want ErrLink", err)
	}
	if err := safefile.Write(inside, []byte("x"), safefile.Options{}); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("write through a linked folder = %v, want ErrLink", err)
	}
	if err := safefile.Write(filepath.Join(wt, ".mestre", "new", "f"), []byte("x"), safefile.Options{}); !errors.Is(err, safefile.ErrLink) {
		t.Fatalf("write that creates folders through a link = %v, want ErrLink", err)
	}
	checkSecret(t, secret, content)
	if _, err := os.Stat(filepath.Join(filepath.Dir(secret), "new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("a folder was created in the engine's folder: %v", err)
	}
}

func checkSecret(t *testing.T, secret string, content []byte) {
	t.Helper()
	if b := mustRead(t, secret); string(b) != string(content) {
		t.Fatalf("the engine file changed: %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(secret); fi.Mode().Perm() != 0o600 {
			t.Fatalf("the engine file's mode changed: %o", fi.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(filepath.Dir(secret))
	if len(entries) != 1 {
		t.Fatalf("files appeared next to the engine file: %v", entries)
	}
}
